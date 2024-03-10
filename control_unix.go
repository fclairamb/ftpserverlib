//go:build linux || freebsd || darwin || aix || dragonfly || netbsd || openbsd
// +build linux freebsd darwin aix dragonfly netbsd openbsd

package ftpserver

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// Control defines the function to use as dialer Control to reuse the same port/address
func Control(_, _ string, c syscall.RawConn) error {
	var errSetOpts error

	err := c.Control(func(unixFd uintptr) {
		errSetOpts = unix.SetsockoptInt(int(unixFd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if errSetOpts != nil {
			return
		}

		errSetOpts = unix.SetsockoptInt(int(unixFd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		if errSetOpts != nil {
			return
		}
	})
	if err != nil {
		return fmt.Errorf("unable to set control options: %w", err)
	}

	if errSetOpts != nil {
		errSetOpts = fmt.Errorf("unable to set control options: %w", errSetOpts)
	}

	return errSetOpts
}
