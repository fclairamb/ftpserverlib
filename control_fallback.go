//go:build !linux && !freebsd && !darwin && !aix && !dragonfly && !netbsd && !openbsd && !solaris && !windows
// +build !linux,!freebsd,!darwin,!aix,!dragonfly,!netbsd,!openbsd,!solaris,!windows

package ftpserver // nolint

import (
	"syscall"
)

// Control defines the function to use as dialer Control to reuse the same port/address.
// This fallback implementation does nothing
func Control(network, address string, c syscall.RawConn) error {
	return nil
}
