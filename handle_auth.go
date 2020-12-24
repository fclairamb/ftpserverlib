// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import "fmt"

// Handle the "USER" command
func (c *clientHandler) handleUSER(param string) error {
	if c.server.settings.TLSRequired == MandatoryEncryption && !c.HasTLSForControl() {
		c.writeMessage(StatusServiceNotAvailable, "TLS is required")

		return nil
	}

	c.user = param
	c.writeMessage(StatusUserOK, "OK")

	return nil
}

// Handle the "PASS" command
func (c *clientHandler) handlePASS(param string) error {
	var err error
	c.driver, err = c.server.driver.AuthUser(c, c.user, param)

	switch {
	case err == nil:
		c.writeMessage(StatusUserLoggedIn, "Password ok, continue")
	case err != nil:
		c.writeMessage(StatusNotLoggedIn, fmt.Sprintf("Authentication problem: %v", err))
		c.disconnect()
	default:
		c.writeMessage(StatusNotLoggedIn, "I can't deal with you (nil driver)")
		c.disconnect()
	}

	return nil
}
