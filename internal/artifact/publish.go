// Package artifact publishes PIACE's output files and checks the
// destinations a run was asked to write to before it starts working.
//
// # Why publication is not os.WriteFile
//
// Every file PIACE writes is read by something else: a CI job archives a
// report, a later comparison loads a snapshot, a reviewer opens an HTML
// artifact. os.WriteFile truncates the destination and then fills it, so
// a reader arriving in between sees an empty or half-written file that
// is indistinguishable from a real one, and a writer that dies halfway
// leaves exactly that behind. Write publishes through a same-directory
// temporary file and one atomic rename instead: a reader sees either the
// previous file or the complete new one.
//
// WriteNew adds refusal-to-replace to the same guarantee, for snapshots
// captured without --replace. Checking that a path does not exist and
// then renaming onto it is not that: two capture runs can both pass the
// check and the second silently wins. WriteNew links the temporary file
// into place instead, which the kernel refuses if the destination
// already exists, so the refusal and the publication are one operation.
//
// # Non-regular destinations
//
// A destination that resolves to something other than a regular file, a
// pipe or a device such as /dev/stdout, is written in place: it has no
// directory entry to rename over, and atomicity is not a property such a
// destination can offer. Symlinks are followed for this decision, so a
// symlink pointing at a regular file takes the atomic path, and the
// rename replaces the symlink itself rather than its target.
package artifact

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrExists reports that WriteNew refused to replace an existing
// destination. Callers distinguish it from any other publication failure
// with errors.Is.
var ErrExists = errors.New("artifact: destination already exists")

// Write publishes data at path, replacing whatever is there, and returns
// only after the new content is durable:
//
//   - a same-directory temporary file is created, chmod'd to perm before
//     any content reaches it, written, and fsynced;
//   - the temporary file is renamed onto path, which is atomic within
//     one directory on every platform PIACE targets;
//   - the containing directory is fsynced best-effort, so the rename
//     itself survives a crash. A platform or filesystem that refuses an
//     fsync on a directory descriptor is a pre-existing limitation, not
//     a failure of this write.
//
// perm is applied to the temporary file rather than to path afterwards,
// so the file is never briefly readable under the wrong mode.
//
// The containing directory is created (0700, with parents) when absent:
// the first capture of a repository would otherwise fail on a directory
// the same command exists to populate.
func Write(path string, data []byte, perm fs.FileMode) error {
	if inPlace, err := writeInPlaceIfNonRegular(path, data); inPlace {
		return err
	}
	tmp, err := stage(path, data, perm)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("artifact: publishing %s: %w", path, err)
	}
	syncDirBestEffort(filepath.Dir(path))
	return nil
}

// WriteNew publishes data at path with Write's durability guarantee and
// refuses to replace an existing destination, returning an error
// wrapping ErrExists.
//
// The refusal is the publication step itself: the staged file is linked
// into place, and the kernel refuses the link if path exists. Two
// concurrent writers therefore cannot both succeed, and neither can
// overwrite the other, which a separate existence check followed by a
// rename cannot promise however carefully it is written.
//
// A filesystem that cannot create hard links at all fails here with a
// diagnostic saying so. Falling back to check-then-rename would restore
// exactly the race this exists to close, silently.
func WriteNew(path string, data []byte, perm fs.FileMode) error {
	tmp, err := stage(path, data, perm)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrExists, path)
		}
		return fmt.Errorf("artifact: publishing %s without replacement: %w "+
			"(this filesystem cannot create hard links, which is how a no-replace publication stays atomic)", path, err)
	}
	syncDirBestEffort(filepath.Dir(path))
	return nil
}

// stage writes data to a new temporary file beside path, with perm
// applied and contents fsynced, and returns its name. The caller
// publishes it with rename or link, and removes it on any path that does
// not.
func stage(path string, data []byte, perm fs.FileMode) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("artifact: staging %s: creating directory %s: %w", path, dir, err)
	}
	f, err := os.CreateTemp(dir, ".piace-*.tmp")
	if err != nil {
		return "", fmt.Errorf("artifact: staging %s: creating a temporary file in %s: %w", path, dir, err)
	}
	name := f.Name()

	err = func() error {
		if err := f.Chmod(perm); err != nil {
			return fmt.Errorf("setting mode: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("writing: %w", err)
		}
		if err := f.Sync(); err != nil {
			return fmt.Errorf("fsyncing: %w", err)
		}
		return f.Close()
	}()
	if err != nil {
		f.Close()
		os.Remove(name)
		return "", fmt.Errorf("artifact: staging %s: %w", path, err)
	}
	return name, nil
}

// writeInPlaceIfNonRegular writes data directly to path when path
// already resolves to something that is not a regular file, and reports
// whether it did. See the package comment: a pipe or device has no
// directory entry to rename over.
func writeInPlaceIfNonRegular(path string, data []byte) (bool, error) {
	info, err := os.Stat(path)
	if err != nil || info.Mode().IsRegular() {
		return false, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return true, fmt.Errorf("artifact: opening %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return true, fmt.Errorf("artifact: writing %s: %w", path, err)
	}
	return true, nil
}

// syncDirBestEffort fsyncs dir so a preceding rename or link is durable.
// Any failure is ignored: the entry is already visible to every reader
// by the time this runs, and some platforms and filesystems do not
// support fsync on a directory descriptor at all.
func syncDirBestEffort(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
