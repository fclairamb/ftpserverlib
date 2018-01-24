package server

import "fmt"

// Handle the "USER" command
func (c *clientHandler) handleUSER() error {
	c.user = c.param
	return c.writeMessage(StatusUserOK, "OK")
}

// Handle the "PASS" command
func (c *clientHandler) handlePASS() error {
	if c.server.driver == nil {
		err := c.writeMessage(StatusNotLoggedIn, "I can't deal with you (nil driver)")
		c.Close()
		return err
	}

	var err error
	if c.driver, err = c.server.driver.AuthUser(c, c.user, c.param); err != nil {
		err = c.writeMessage(StatusNotLoggedIn, fmt.Sprintf("Authentication problem: %v", err))
		c.Close()
		return err
	}

	return c.writeMessage(StatusUserLoggedIn, "Password ok, continue")
}
