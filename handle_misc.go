package ftpserver

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var errUnknowHash = errors.New("unknown hash algorithm")

func (c *clientHandler) handleAUTH(_ string) error {
	if tlsConfig, err := c.server.driver.GetTLSConfig(); err == nil {
		c.writeMessage(StatusAuthAccepted, "AUTH command ok. Expecting TLS Negotiation.")
		c.conn = tls.Server(c.conn, tlsConfig)
		c.reader = bufio.NewReaderSize(c.conn, maxCommandSize)
		c.writer = bufio.NewWriter(c.conn)
		c.setTLSForControl(true)
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Cannot get a TLS config: %v", err))
	}

	return nil
}

func (c *clientHandler) handlePROT(param string) error {
	// P for Private, C for Clear
	c.setTLSForTransfer(param == "P")
	c.writeMessage(StatusOK, "OK")

	return nil
}

func (c *clientHandler) handlePBSZ(_ string) error {
	c.writeMessage(StatusOK, "Whatever")

	return nil
}

func (c *clientHandler) handleSYST(_ string) error {
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
		c.handleSiteCHMOD(params)
	case "CHOWN":
		c.handleSiteCHOWN(params)
	case "SYMLINK":
		c.handleSYMLINK(params)
	case "MKDIR":
		c.handleSiteMKDIR(params)
	case "RMDIR":
		c.handleSiteRMDIR(params)
	default:
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Unknown SITE subcommand: "+cmd)
	}

	return nil
}

func (c *clientHandler) handleSiteCHMOD(params string) {
	spl := strings.SplitN(params, " ", 2)
	if len(spl) != 2 {
		c.writeMessage(StatusSyntaxErrorParameters, "bad command")
		return
	}

	modeNb, err := strconv.ParseUint(spl[0], 8, 32)
	if err != nil {
		c.writeMessage(StatusActionNotTaken, err.Error())
		return
	}

	mode := os.FileMode(modeNb)
	path := c.absPath(spl[1])

	// Try extension first
	if siteExt, ok := c.driver.(ClientDriverExtensionSiteCommand); ok {
		err = siteExt.SiteChmod(path, mode)
	} else {
		// Fallback to default implementation
		err = c.driver.Chmod(path, mode)
	}

	if err != nil {
		c.writeMessage(StatusActionNotTaken, err.Error())
		return
	}

	c.writeMessage(StatusOK, "SITE CHMOD command successful")
}

func (c *clientHandler) handleSiteCHOWN(params string) {
	spl := strings.SplitN(params, " ", 3)
	if len(spl) != 2 {
		c.writeMessage(StatusSyntaxErrorParameters, "bad command")
		return
	}

	var userID, groupID int
	usergroup := strings.Split(spl[0], ":")
	userName := usergroup[0]

	if id, err := strconv.ParseInt(userName, 10, 32); err == nil {
		userID = int(id)
	} else {
		userID = 0
	}

	if len(usergroup) > 1 {
		groupName := usergroup[1]
		if id, err := strconv.ParseInt(groupName, 10, 32); err == nil {
			groupID = int(id)
		} else {
			groupID = 0
		}
	} else {
		groupID = 0
	}

	path := c.absPath(spl[1])

	var err error
	// Try extension first
	if siteExt, ok := c.driver.(ClientDriverExtensionSiteCommand); ok {
		err = siteExt.SiteChown(path, userID, groupID)
	} else {
		// Fallback to default implementation (same as original)
		err = c.driver.Chown(path, userID, groupID)
	}

	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't chown: %v", err))
		return
	}

	c.writeMessage(StatusOK, "Done !")
}

func (c *clientHandler) handleSiteMKDIR(params string) {
	if params == "" {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Missing path")
		return
	}

	path := c.absPath(params)

	// Try extension first
	if siteExt, ok := c.driver.(ClientDriverExtensionSiteCommand); ok {
		err := siteExt.SiteMkdir(path)
		if err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't create dir %s: %v", path, err))
			return
		}
	} else {
		// Fallback to default implementation
		if err := c.driver.MkdirAll(path, 0o755); err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't create dir %s: %v", path, err))
			return
		}
	}

	c.writeMessage(StatusFileOK, "Created dir "+path)
}

func (c *clientHandler) handleSiteRMDIR(params string) {
	if params == "" {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Missing path")
		return
	}

	path := c.absPath(params)

	// Try extension first
	if siteExt, ok := c.driver.(ClientDriverExtensionSiteCommand); ok {
		err := siteExt.SiteRmdir(path)
		if err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't remove dir %s: %v", path, err))
			return
		}
	} else {
		// Fallback to default implementation
		if err := c.driver.RemoveAll(path); err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't remove dir %s: %v", path, err))
			return
		}
	}

	c.writeMessage(StatusFileOK, "Removed dir "+path)
}

func (c *clientHandler) handleSTATServer() error {
	// we need to hold the transfer lock here:
	// server STAT is a special action command so we need to ensure
	// to write the whole STAT response before sending a transfer
	// open/close message
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	if c.server.settings.DisableSTAT {
		c.writeMessage(StatusCommandNotImplemented, "STAT is disabled")

		return nil
	}

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
		c.writeLine("Logged in as " + c.user)
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

func (c *clientHandler) handleOptsUtf8() error {
	c.writeMessage(StatusOK, "I'm in UTF8 only anyway")

	return nil
}

func (c *clientHandler) handleOptsHash(args []string) error {
	hashMapping := getHashMapping()

	if len(args) > 0 {
		// try to change the current hash algorithm to the requested one
		if value, ok := hashMapping[args[0]]; ok {
			c.selectedHashAlgo = value
			c.writeMessage(StatusOK, args[0])
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

func (c *clientHandler) handleOPTS(param string) error {
	args := strings.SplitN(param, " ", 2)

	switch strings.ToUpper(args[0]) {
	case "UTF8":
		return c.handleOptsUtf8()
	case "HASH":
		if c.server.settings.EnableHASH {
			return c.handleOptsHash(args[1:])
		}
	}

	c.writeMessage(StatusSyntaxErrorNotRecognised, "Don't know this option")

	return nil
}

func (c *clientHandler) handleNOOP(_ string) error {
	c.writeMessage(StatusOK, "OK")

	return nil
}

func (c *clientHandler) handleCLNT(param string) error {
	c.setClientVersion(param)
	c.writeMessage(StatusOK, "Good to know")

	return nil
}

func (c *clientHandler) handleFEAT(_ string) error {
	c.writeLine(fmt.Sprintf("%d- These are my features", StatusSystemStatus))
	defer c.writeMessage(StatusSystemStatus, "end")

	features := []string{
		"CLNT",
		"UTF8",
		"SIZE",
		"MDTM",
		"REST STREAM",
		"EPRT",
		"EPSV",
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
		features = append(features, "AUTH TLS", "PBSZ", "PROT")
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
	param = strings.ReplaceAll(strings.ToUpper(param), " ", "")
	switch param {
	case "I", "L8":
		c.currentTransferType = TransferTypeBinary
		c.writeMessage(StatusOK, "Type set to binary")
	case "A", "AN", "L7":
		c.currentTransferType = TransferTypeASCII
		c.writeMessage(StatusOK, "Type set to ASCII")
	default:
		c.writeMessage(StatusNotImplementedParam, "Unsupported transfer type")
	}

	return nil
}

func (c *clientHandler) handleMODE(param string) error {
	if param == "S" {
		c.writeMessage(StatusOK, "Using stream mode")
	} else {
		c.writeMessage(StatusNotImplementedParam, "Unsupported mode")
	}

	return nil
}

func (c *clientHandler) handleQUIT(_ string) error {
	c.transferWg.Wait()

	var msg string

	if quitter, ok := c.server.driver.(MainDriverExtensionQuitMessage); ok {
		msg = quitter.QuitMessage()
	} else {
		msg = "Goodbye"
	}

	c.writeMessage(StatusClosingControlConn, msg)
	c.disconnect()
	c.reader = nil

	return nil
}

func (c *clientHandler) handleABOR(param string) error {
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	if c.transfer != nil {
		isOpened := c.isTransferOpen

		c.isTransferAborted = true

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
			c.writeMessage(StatusActionNotTaken, path+": is not a directory")

			return nil
		}

		available, err := avbl.GetAvailableSpace(path)
		if err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't get space for path %s: %v", path, err))

			return nil
		}

		c.writeMessage(StatusFileStatus, strconv.FormatInt(available, 10))
	} else {
		c.writeMessage(StatusNotImplemented, "This extension hasn't been implemented !")
	}

	return nil
}

func (c *clientHandler) handleNotImplemented(_ string) error {
	c.writeMessage(StatusCommandNotImplemented, "This command hasn't been implemented !")

	return nil
}
