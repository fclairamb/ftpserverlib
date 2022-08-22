package ftpserver

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// Control defines the function to use as dialer Control to reuse the same port/address
func Control(network, address string, c syscall.RawConn) error {
	var errSetOpts error

	err := c.Control(func(fd uintptr) {
		errSetOpts = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}

	return errSetOpts
}
