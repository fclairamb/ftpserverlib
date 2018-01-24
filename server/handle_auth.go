package server

import "fmt"

// Handle the "USER" command
func (c *clientHandler) handleUSER() error {
	c.user = c.param
	return c.writeMessage(331, "OK")
}

// Handle the "PASS" command
func (c *clientHandler) handlePASS() error {
	if c.daddy.driver == nil {
		err := c.writeMessage(530, "I can't deal with you (nil driver)")
		c.Close()
		return err
	}

	var err error
	if c.driver, err = c.daddy.driver.AuthUser(c, c.user, c.param); err != nil {
		err = c.writeMessage(530, fmt.Sprintf("Authentication problem: %v", err))
		c.Close()
		return err
	}

	return c.writeMessage(230, "Password ok, continue")
}
