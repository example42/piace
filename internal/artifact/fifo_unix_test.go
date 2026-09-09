//go:build unix

package artifact

import "syscall"

// makeFIFO creates a named pipe, so the non-regular-destination test can
// use a real one rather than a stand-in. syscall rather than
// golang.org/x/sys: this is the whole dependency, and PIACE's module
// requires one package today.
func makeFIFO(path string) error { return syscall.Mkfifo(path, 0o600) }
