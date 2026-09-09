//go:build !unix

package artifact

import "errors"

// makeFIFO reports that this platform has no named pipes, which makes
// the non-regular-destination test skip rather than fail.
func makeFIFO(string) error { return errors.New("named pipes are not supported on this platform") }
