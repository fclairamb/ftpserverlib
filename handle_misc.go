// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errUnknowHash = errors.New("unknown hash algorithm")

func (c *clientHandler) handleAUTH(param string) error {
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

func (c *clientHandler) handlePROT(param string) error {
	// P for Private, C for Clear
	c.transferTLS = param == "P"
	c.writeMessage(StatusOK, "OK")

	return nil
}

func (c *clientHandler) handlePBSZ(param string) error {
	c.writeMessage(StatusOK, "Whatever")

	return nil
}

func (c *clientHandler) handleSYST(param string) error {
	if c.server.settings.DisableSYST {
		c.writeMessage(StatusCommandNotImplemented, "SYST is disabled")

		return nil
	}

	c.writeMessage(StatusSystemType, "UNIX Type: L8")

	return nil
}

func (c *clientHandler) handleSTAT(param string) error {
	if param == "" { // Without a file, it's the server stat
		return c.handleSTATServer()
	}

	// With a file/dir it's the file or the dir's files stat
	return c.handleSTATFile(param)
}

func (c *clientHandler) handleSITE(param string) error {
	if c.server.settings.DisableSite {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "SITE support is disabled")

		return nil
	}

	spl := strings.SplitN(param, " ", 2)
	cmd := strings.ToUpper(spl[0])
	var params string

	if len(spl) > 1 {
		params = spl[1]
	} else {
		params = ""
	}

	switch cmd {
	case "CHMOD":
		c.handleCHMOD(params)
	case "CHOWN":
		c.handleCHOWN(params)
	case "SYMLINK":
		c.handleSYMLINK(params)
	case "MKDIR":
		c.handleMKDIR(params)
	case "RMDIR":
		c.handleRMDIR(params)
	default:
		c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf("Unknown SITE subcommand: %s", cmd))
	}

	return nil
}

func (c *clientHandler) handleSTATServer() error {
	if c.server.settings.DisableSTAT {
		c.writeMessage(StatusCommandNotImplemented, "STAT is disabled")

		return nil
	}

	// drakkan(2020-12-17): we don't handle STAT properly,
	// we should return the status for all the transfers and we should allow
	// stat while a transfer is in progress, see RFC 959
	defer c.multilineAnswer(StatusSystemStatus, "Server status")()

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

	if info := c.GetTranferInfo(); info != "" {
		c.writeLine("Transfer connection open")
		c.writeLine(info)
	}

	c.writeLine(c.server.settings.Banner)

	return nil
}

func (c *clientHandler) handleOPTS(param string) error {
	args := strings.SplitN(param, " ", 2)
	if strings.EqualFold(args[0], "UTF8") {
		c.writeMessage(StatusOK, "I'm in UTF8 only anyway")

		return nil
	}

	if strings.EqualFold(args[0], "HASH") && c.server.settings.EnableHASH {
		hashMapping := getHashMapping()

		if len(args) > 1 {
			// try to change the current hash algorithm to the requested one
			if value, ok := hashMapping[args[1]]; ok {
				c.selectedHashAlgo = value
				c.writeMessage(StatusOK, args[1])
			} else {
				c.writeMessage(StatusSyntaxErrorParameters, "Unknown algorithm, current selection not changed")
			}

			return nil
		}
		// return the current hash algorithm
		var currentHash string

		for k, v := range hashMapping {
			if v == c.selectedHashAlgo {
				currentHash = k
			}
		}

		c.writeMessage(StatusOK, currentHash)

		return nil
	}

	c.writeMessage(StatusSyntaxErrorNotRecognised, "Don't know this option")

	return nil
}

func (c *clientHandler) handleNOOP(param string) error {
	c.writeMessage(StatusOK, "OK")

	return nil
}

func (c *clientHandler) handleCLNT(param string) error {
	c.clnt = param
	c.writeMessage(StatusOK, "Good to know")

	return nil
}

func (c *clientHandler) handleFEAT(param string) error {
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

	if c.server.settings.EnableHASH {
		var hashLine strings.Builder

		nonStandardHashImpl := []string{"XCRC", "MD5", "XMD5", "XSHA", "XSHA1", "XSHA256", "XSHA512"}
		hashMapping := getHashMapping()

		for k, v := range hashMapping {
			hashLine.WriteString(k)

			if v == c.selectedHashAlgo {
				hashLine.WriteString("*")
			}

			hashLine.WriteString(";")
		}

		features = append(features, hashLine.String())
		features = append(features, nonStandardHashImpl...)
	}

	if c.server.settings.EnableCOMB {
		features = append(features, "COMB")
	}

	if _, ok := c.driver.(ClientDriverExtensionAvailableSpace); ok {
		features = append(features, "AVBL")
	}

	for _, f := range features {
		c.writeLine(" " + f)
	}

	return nil
}

func (c *clientHandler) handleTYPE(param string) error {
	switch param {
	case "I":
		c.writeMessage(StatusOK, "Type set to binary")
	case "A":
		c.writeMessage(StatusOK, "ASCII isn't properly supported: https://github.com/fclairamb/ftpserverlib/issues/86")
	default:
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Not understood")
	}

	return nil
}

func (c *clientHandler) handleQUIT(param string) error {
	c.transferWg.Wait()
	c.writeMessage(StatusClosingControlConn, "Goodbye")
	c.disconnect()
	c.reader = nil

	return nil
}

// param is the previous command, the one to abort
func (c *clientHandler) handleABOR(param string) error {
	c.Lock()
	defer c.Unlock()

	if c.transfer != nil {
		isOpened := c.isTransferOpen

		if isOpened || c.canOpenTransfer(param) {
			c.isTransferAborted = true
		}

		if err := c.closeTransfer(); err != nil {
			c.logger.Warn(
				"Problem aborting transfer for command", param,
				"err", err,
			)
		}

		if c.debug {
			c.logger.Debug(
				"Transfer aborted",
				"command", param)
		}

		if isOpened {
			c.writeMessage(StatusTransferAborted, "Connection closed; transfer aborted")
		}
	}

	c.writeMessage(StatusClosingDataConn, "ABOR successful; closing transfer connection")

	return nil
}

func (c *clientHandler) handleAVBL(param string) error {
	if avbl, ok := c.driver.(ClientDriverExtensionAvailableSpace); ok {
		path := c.absPath(param)

		info, err := c.driver.Stat(path)
		if err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %s: %v", path, err))

			return nil
		}

		if !info.IsDir() {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("%s: is not a directory", path))

			return nil
		}

		available, err := avbl.GetAvailableSpace(path)
		if err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't get space for path %s: %v", path, err))

			return nil
		}

		c.writeMessage(StatusFileStatus, fmt.Sprintf("%d", available))
	} else {
		c.writeMessage(StatusNotImplemented, "This extension hasn't been implemented !")
	}

	return nil
}
