package ftpserver

import (
	"crypto/tls"
	"fmt"
)

// Handle the "USER" command
func (c *clientHandler) handleUSER(param string) error {
	if verifier, ok := c.server.driver.(MainDriverExtensionUserVerifier); ok {
		err := verifier.PreAuthUser(c, param)

		if err != nil {
			c.writeMessage(StatusNotLoggedIn, fmt.Sprintf("User rejected: %v", err))
			c.disconnect()

			return nil
		}
	}

	if c.isTLSRequired() && !c.HasTLSForControl() {
		c.writeMessage(StatusServiceNotAvailable, "TLS is required")
		c.disconnect()

		return nil
	}

	if c.HasTLSForControl() {
		if verifier, ok := c.server.driver.(MainDriverExtensionTLSVerifier); ok {
			if tlsConn, ok := c.conn.(*tls.Conn); ok {
				driver, err := verifier.VerifyConnection(c, param, tlsConn)

				if err != nil {
					c.writeMessage(StatusNotLoggedIn, fmt.Sprintf("TLS verification failed: %v", err))
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
	var msg string
	c.driver, err = c.server.driver.AuthUser(c, c.user, param)

	dpa, ok := c.server.driver.(MainDriverExtensionPostAuthMessage)
	if ok {
		msg = dpa.PostAuthMessage(c, c.user, err)
	}

	switch {
	case err == nil && c.driver == nil:
		c.writeMessage(StatusNotLoggedIn, "Unexpected exception (driver is nil)")
		c.disconnect()
	case err != nil:
		if msg == "" {
			msg = fmt.Sprintf("Authentication error: %v", err)
		}

		c.writeMessage(StatusNotLoggedIn, msg)
		c.disconnect()
	default: // err == nil && c.driver != nil
		if msg == "" {
			msg = "Password ok, continue"
		}

		c.writeMessage(StatusUserLoggedIn, msg)
	}

	return nil
}
