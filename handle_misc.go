// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"strings"
	"time"
)

func (c *clientHandler) handleAUTH() error {
	if tlsConfig, err := c.server.driver.GetTLSConfig(); err == nil {
		c.writeMessage(StatusAuthAccepted, "AUTH command ok. Expecting TLS Negotiation.")
		c.conn = tls.Server(c.conn, tlsConfig)
		c.reader = bufio.NewReader(c.conn)
		c.writer = bufio.NewWriter(c.conn)
		c.controlTLS = true
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Cannot get a TLS config: %v", err))
	}

	return nil
}

func (c *clientHandler) handlePROT() error {
	// P for Private, C for Clear
	c.transferTLS = c.param == "P"
	c.writeMessage(StatusOK, "OK")

	return nil
}

func (c *clientHandler) handlePBSZ() error {
	c.writeMessage(StatusOK, "Whatever")
	return nil
}

func (c *clientHandler) handleSYST() error {
	c.writeMessage(StatusSystemType, "UNIX Type: L8")
	return nil
}

func (c *clientHandler) handleSTAT() error {
	if c.param == "" { // Without a file, it's the server stat
		return c.handleSTATServer()
	}

	// With a file/dir it's the file or the dir's files stat
	return c.handleSTATFile()
}

func (c *clientHandler) handleSITE() error {
	spl := strings.SplitN(c.param, " ", 2)
	if len(spl) > 1 {
		switch strings.ToUpper(spl[0]) {
		case "CHMOD":
			c.handleCHMOD(spl[1])
			return nil
		case "CHOWN":
			c.handleCHOWN(spl[1])
			return nil
		case "SYMLINK":
			c.handleSYMLINK(spl[1])
			return nil
		}
	}

	c.writeMessage(StatusSyntaxErrorNotRecognised, "Not understood SITE subcommand")

	return nil
}

func (c *clientHandler) handleSTATServer() error {
	defer c.multilineAnswer(StatusFileStatus, "Server status")()

	duration := time.Now().UTC().Sub(c.connectedAt)
	duration -= duration % time.Second
	c.writeLine(fmt.Sprintf(
		"Connected to %s from %s for %s",
		c.server.settings.ListenAddr,
		c.conn.RemoteAddr(),
		duration,
	))

	if c.user != "" {
		c.writeLine(fmt.Sprintf("Logged in as %s", c.user))
	} else {
		c.writeLine("Not logged in yet")
	}

	c.writeLine(c.server.settings.Banner)

	return nil
}

func (c *clientHandler) handleOPTS() error {
	args := strings.SplitN(c.param, " ", 2)
	if strings.EqualFold(args[0], "UTF8") {
		c.writeMessage(StatusOK, "I'm in UTF8 only anyway")
	} else {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Don't know this option")
	}

	return nil
}

func (c *clientHandler) handleNOOP() error {
	c.writeMessage(StatusOK, "OK")
	return nil
}

func (c *clientHandler) handleCLNT() error {
	c.clnt = c.param
	c.writeMessage(StatusOK, "Good to know")

	return nil
}

func (c *clientHandler) handleFEAT() error {
	c.writeLine(fmt.Sprintf("%d- These are my features", StatusSystemStatus))
	defer c.writeMessage(StatusSystemStatus, "end")

	features := []string{
		"CLNT",
		"UTF8",
		"SIZE",
		"MDTM",
		"REST STREAM",
	}

	if !c.server.settings.DisableMLSD {
		features = append(features, "MLSD")
	}

	if !c.server.settings.DisableMLST {
		features = append(features, "MLST")
	}

	if !c.server.settings.DisableMFMT {
		features = append(features, "MFMT")
	}

	// This code made me think about adding this: https://github.com/stianstr/ftpserver/commit/387f2ba
	if tlsConfig, err := c.server.driver.GetTLSConfig(); tlsConfig != nil && err == nil {
		features = append(features, "AUTH TLS")
	}

	for _, f := range features {
		c.writeLine(" " + f)
	}

	return nil
}

func (c *clientHandler) handleTYPE() error {
	switch c.param {
	case "I":
		c.writeMessage(StatusOK, "Type set to binary")
	case "A":
		c.writeMessage(StatusOK, "ASCII isn't properly supported: https://github.com/fclairamb/ftpserverlib/issues/86")
	default:
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Not understood")
	}

	return nil
}

func (c *clientHandler) handleQUIT() error {
	c.writeMessage(StatusClosingControlConn, "Goodbye")
	c.disconnect()
	c.reader = nil

	return nil
}
