//go:build linux || freebsd || darwin || aix || dragonfly || netbsd || openbsd
// +build linux freebsd darwin aix dragonfly netbsd openbsd

package ftpserver

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// Control defines the function to use as dialer Control to reuse the same port/address
func Control(network, address string, c syscall.RawConn) error {
	var errSetOpts error

	err := c.Control(func(fd uintptr) {
		errSetOpts = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if errSetOpts != nil {
			return
		}

		errSetOpts = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		if errSetOpts != nil {
			return
		}
	})
	if err != nil {
		return err
	}

	return errSetOpts
}
