package ftpserver // nolint

import (
	"crypto/tls"
	"fmt"
)

// Handle the "USER" command
func (c *clientHandler) handleUSER(param string) error {
	if c.server.settings.TLSRequired == MandatoryEncryption && !c.HasTLSForControl() {
		c.writeMessage(StatusServiceNotAvailable, "TLS is required")

		return nil
	}

	if c.HasTLSForControl() {
		if verifier, ok := c.server.driver.(MainDriverExtensionTLSVerifier); ok {
			if tlsConn, ok := c.conn.(*tls.Conn); ok {
				driver, err := verifier.VerifyConnection(c, param, tlsConn)

				if err != nil {
					c.writeMessage(StatusServiceNotAvailable, fmt.Sprintf("TLS verification failed: %v", err))
					c.disconnect()

					return nil
				}

				if driver != nil {
					c.user = param
					c.driver = driver
					c.writeMessage(StatusUserLoggedIn, "TLS certificate ok, continue")

					return nil
				}
			}
		}
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
