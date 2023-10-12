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
	c.driver, msg, err = c.server.driver.AuthUser(c, c.user, param)

	switch {
	case err == nil:
		if msg == "" {
			msg = "Ok"
		}
		c.writeMessage(StatusUserLoggedIn, msg)
	case err != nil:
		if msg == "" {
			msg = fmt.Sprintf("Authentication error: %v", err)
		}
		c.writeMessage(StatusNotLoggedIn, msg)
		c.disconnect()
	default:
		c.writeMessage(StatusNotLoggedIn, "Unexpected exception (driver is nil)")
		c.disconnect()
	}

	return nil
}
