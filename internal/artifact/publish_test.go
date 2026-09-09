package artifact

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWrite_ReplacesAtomicallyWithTheRequestedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := Write(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Write(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("Write (replacing): %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Errorf("content = %q, want the replacement", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWrite_LeavesNoTemporaryFilesBehind(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "report.json"), []byte("x"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "report.json" {
		t.Errorf("directory holds %v, want only the published artifact", entries)
	}
}

func TestWrite_CreatesTheContainingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshots", "catalogs", "web-01.json")
	if err := Write(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}

func TestWriteNew_RefusesAnExistingDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := WriteNew(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("WriteNew: %v", err)
	}
	err := WriteNew(path, []byte("second"), 0o600)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("error = %v, want one wrapping ErrExists", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first" {
		t.Errorf("content = %q, want the original", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("directory holds %v, want the refused write to have left nothing", entries)
	}
}

// TestWriteNew_ConcurrentWritersCannotOverwriteEachOther is the point of
// WriteNew. A publication that checks for an existing file and then
// renames onto it passes a sequential refusal test while still losing
// this one: both writers see no file, both rename, and the second
// silently replaces the first.
func TestWriteNew_ConcurrentWritersCannotOverwriteEachOther(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	const writers = 12

	var wg sync.WaitGroup
	results := make([]error, writers)
	payloads := make([][]byte, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, 4096)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = WriteNew(path, payloads[i], 0o600)
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrExists):
		default:
			t.Errorf("writer %d: %v, want success or ErrExists", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d writers succeeded, want exactly 1", succeeded)
	}

	published, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range payloads {
		if bytes.Equal(published, payload) {
			return
		}
	}
	t.Errorf("the published file matches no single writer's payload (len %d), so a write was torn", len(published))
}

// TestWrite_ConcurrentReplacementIsNeverPartial covers the other
// guarantee: a reader arriving at any moment sees one writer's complete
// content, never an empty or half-filled file.
func TestWrite_ConcurrentReplacementIsNeverPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	small := bytes.Repeat([]byte("a"), 1024)
	large := bytes.Repeat([]byte("b"), 512*1024)
	if err := Write(path, small, 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			payload := large
			if i%2 == 0 {
				payload = small
			}
			if err := Write(path, payload, 0o644); err != nil {
				t.Errorf("Write: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 200; i++ {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !bytes.Equal(data, small) && !bytes.Equal(data, large) {
			t.Fatalf("read %d bytes matching neither complete payload: a partial write was visible", len(data))
		}
	}
	wg.Wait()
}

func TestWrite_NonRegularDestinationIsWrittenInPlace(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := makeFIFO(fifo); err != nil {
		t.Skipf("this platform cannot create a FIFO for the test: %v", err)
	}

	read := make(chan []byte, 1)
	go func() {
		data, err := os.ReadFile(fifo)
		if err != nil {
			read <- nil
			return
		}
		read <- data
	}()

	if err := Write(fifo, []byte("streamed"), 0o644); err != nil {
		t.Fatalf("Write to a FIFO: %v", err)
	}
	if got := <-read; string(got) != "streamed" {
		t.Errorf("reader received %q, want the written bytes", got)
	}
	// The FIFO must still be a FIFO: a rename would have replaced it with
	// a regular file, which is exactly what the caller did not ask for.
	info, err := os.Lstat(fifo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeNamedPipe == 0 {
		t.Errorf("mode = %v, want the FIFO to survive", info.Mode())
	}
}

func TestWrite_SymlinkToARegularFileTakesTheAtomicPath(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(realPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, link); err != nil {
		t.Skipf("this platform cannot create symlinks: %v", err)
	}

	if err := Write(link, []byte("published"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Documented consequence of publishing by rename: the symlink itself
	// is replaced, and the file it pointed at is untouched.
	data, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Errorf("the symlink target was rewritten (%q); the rename should have replaced the link", data)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		t.Error("the destination is still a symlink, want it replaced by the published file")
	}
}

func TestWrite_ReportsAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	err := Write(filepath.Join(locked, "report.json"), []byte("x"), 0o644)
	if err == nil {
		t.Fatal("expected an error writing into an unwritable directory")
	}
	if !strings.Contains(err.Error(), "report.json") {
		t.Errorf("error = %v, want it to name the destination", err)
	}
}
