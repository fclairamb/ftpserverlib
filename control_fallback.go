//go:build !linux && !freebsd && !darwin && !aix && !dragonfly && !netbsd && !openbsd && !windows
// +build !linux,!freebsd,!darwin,!aix,!dragonfly,!netbsd,!openbsd,!windows

package ftpserver

import (
	"syscall"
)

// Control defines the function to use as dialer Control to reuse the same port/address.
// This fallback implementation does nothing
func Control(network, address string, c syscall.RawConn) error {
	return nil
}
