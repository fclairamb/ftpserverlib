package ftpserver

import (
	"crypto/tls"
	"fmt"
)

// Handle the "USER" command
func (c *clientHandler) handleUSER(user string) error {
	if verifier, ok := c.server.driver.(MainDriverExtensionUserVerifier); ok {
		err := verifier.PreAuthUser(c, user)
		if err != nil {
			c.writeMessage(StatusNotLoggedIn, fmt.Sprintf("User rejected: %v", err))
			_ = c.disconnect()

			return nil
		}
	}

	if c.isTLSRequired() && !c.HasTLSForControl() {
		c.writeMessage(StatusServiceNotAvailable, "TLS is required")
		_ = c.disconnect()

		return nil
	}

	if c.HasTLSForControl() {
		if c.handleUserTLS(user) {
			return nil
		}
	}

	c.user = user
	c.writeMessage(StatusUserOK, "OK")

	return nil
}

func (c *clientHandler) handleUserTLS(user string) bool {
	verifier, interfaceFound := c.server.driver.(MainDriverExtensionTLSVerifier)

	if !interfaceFound {
		return false
	}

	tlsConn, interfaceFound := c.conn.(*tls.Conn)

	if !interfaceFound {
		return false
	}

	driver, err := verifier.VerifyConnection(c, user, tlsConn)
	if err != nil {
		c.writeMessage(StatusNotLoggedIn, fmt.Sprintf("TLS verification failed: %v", err))
		_ = c.disconnect()

		return true
	}

	if driver != nil {
		c.user = user
		c.driver = driver
		c.writeMessage(StatusUserLoggedIn, "TLS certificate ok, continue")

		return true
	}

	return false
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
		_ = c.disconnect()
	case err != nil:
		if msg == "" {
			msg = fmt.Sprintf("Authentication error: %v", err)
		}

		c.writeMessage(StatusNotLoggedIn, msg)
		_ = c.disconnect()
	default: // err == nil && c.driver != nil
		if msg == "" {
			msg = "Password ok, continue"
		}

		c.writeMessage(StatusUserLoggedIn, msg)
	}

	return nil
}
