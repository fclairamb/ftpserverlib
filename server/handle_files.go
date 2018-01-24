package server

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func (c *clientHandler) handleSTOR() error {
	return c.handleStoreAndAppend(false)
}

func (c *clientHandler) handleAPPE() error {
	return c.handleStoreAndAppend(true)
}

// Handles both the "STOR" and "APPE" commands
func (c *clientHandler) handleStoreAndAppend(append bool) error {
	file, err := c.openFile(c.absPath(c.param), append)
	if err != nil {
		return c.writeMessage(StatusFileActionNotTaken, "Could not open file: "+err.Error())
	}

	tr, err := c.TransferOpen()
	if err != nil {
		return c.writeMessage(StatusFileActionNotTaken, err.Error())
	}
	defer c.TransferClose()
	if _, err = c.storeOrAppend(tr, file); err != nil && err != io.EOF {
		return c.writeMessage(StatusFileActionNotTaken, err.Error())
	}

	return nil
}

func (c *clientHandler) openFile(path string, append bool) (FileStream, error) {
	flag := os.O_WRONLY
	if append {
		flag |= os.O_APPEND
	}

	return c.driver.OpenFile(c, path, flag)
}

func (c *clientHandler) handleRETR() error {
	tr, err := c.TransferOpen()
	if err != nil {
		return c.writeMessage(StatusFileActionNotTaken, err.Error())
	}
	defer c.TransferClose()
	if _, err = c.download(tr, c.absPath(c.param)); err != nil && err != io.EOF {
		return c.writeMessage(StatusFileActionNotTaken, err.Error())
	}
	return nil
}

func (c *clientHandler) download(w io.Writer, name string) (int64, error) {
	file, err := c.driver.OpenFile(c, name, os.O_RDONLY)
	if err != nil {
		return 0, err
	}

	if c.ctxRest != 0 {
		if _, err := file.Seek(c.ctxRest, 0); err != nil {
			return 0, err
		}
		c.ctxRest = 0
	}

	defer file.Close() // nolint: errcheck
	return io.Copy(w, file)
}

func (c *clientHandler) handleCHMOD(params string) error {
	spl := strings.SplitN(params, " ", 2)
	modeNb, err := strconv.ParseUint(spl[0], 10, 32)
	if err == nil {
		err = c.driver.ChmodFile(c, c.absPath(spl[1]), os.FileMode(modeNb))
	}

	if err != nil {
		return c.writeMessage(StatusActionNotTaken, err.Error())
	}

	return c.writeMessage(StatusOK, "SITE CHMOD command successful")
}

func (c *clientHandler) storeOrAppend(r io.Reader, file FileStream) (int64, error) {
	if c.ctxRest != 0 {
		if _, err := file.Seek(c.ctxRest, 0); err != nil {
			return 0, err
		}
		c.ctxRest = 0
	}

	defer file.Close() // nolint: errcheck
	return io.Copy(file, r)
}

func (c *clientHandler) handleDELE() error {
	path := c.absPath(c.param)
	if err := c.driver.DeleteFile(c, path); err != nil {
		return c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Couldn't delete %s: %v", path, err))
	}
	return c.writeMessage(StatusFileOK, fmt.Sprintf("Removed file %s", path))
}

func (c *clientHandler) handleRNFR() error {
	path := c.absPath(c.param)
	if _, err := c.driver.GetFileInfo(c, path); err != nil {
		return c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}
	c.ctxRnfr = path
	return c.writeMessage(StatusFileActionPending, "Sure, give me a target")
}

func (c *clientHandler) handleRNTO() error {
	if c.ctxRnfr == "" {
		return nil
	}

	dst := c.absPath(c.param)
	if err := c.driver.RenameFile(c, c.ctxRnfr, dst); err != nil {
		return c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Couldn't rename %s to %s: %s", c.ctxRnfr, dst, err.Error()))
	}
	c.ctxRnfr = ""
	return c.writeMessage(StatusFileOK, "Done !")
}

func (c *clientHandler) handleSIZE() error {
	path := c.absPath(c.param)
	info, err := c.driver.GetFileInfo(c, path)
	if err != nil {
		return c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}
	return c.writeMessage(StatusFileStatus, fmt.Sprintf("%d", info.Size()))
}

func (c *clientHandler) handleSTATFile() error {
	info, err := c.driver.GetFileInfo(c, c.absPath(c.param))
	if err != nil {
		return c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not STAT: %v", err))
	}

	var files []os.FileInfo
	status := StatusSystemStatus
	if info.IsDir() {
		status = StatusDirectoryStatus
		if files, err = c.driver.ListFiles(c); err != nil {
			return c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not STAT: %v", err))
		}
	} else {
		files = []os.FileInfo{info}
	}

	if err = c.writeLine(fmt.Sprintf("%v-Status follows:", status)); err != nil {
		return err
	}
	for _, f := range files {
		if err = c.writeLine(fmt.Sprintf(" %s", c.fileStat(f))); err != nil {
			return err
		}
	}
	return c.writeLine(fmt.Sprintf("%v End of status", status))
}

func (c *clientHandler) handleMLST() error {
	if c.server.settings.DisableMLST {
		return c.writeMessage(StatusCommandNotImplemented, "MLST has been disabled")
	}

	path := c.absPath(c.param)
	info, err := c.driver.GetFileInfo(c, path)
	if err != nil {
		return c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not list: %v", err))
	}
	if _, err = c.conn.Write([]byte(fmt.Sprintf("%v- File details\r\n ", StatusFileOK))); err != nil {
		return err
	}
	if err := c.writeMLSxOutput(c.conn, info); err != nil {
		return err
	}
	return c.writeMessage(StatusFileOK, "End of file details")
}

func (c *clientHandler) handleALLO() error {
	// TOOO(fclairamb): Add a method in the driver to better support this.
	size, err := strconv.Atoi(c.param)
	if err != nil {
		return c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("Couldn't parse size: %v", err))
	}

	if ok, err2 := c.driver.CanAllocate(c, size); err2 != nil {
		return c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf("Driver issue: %v", err2))
	} else if !ok {
		return c.writeMessage(StatusActionNotTaken, "NOT OK, we don't have the free space")
	}
	return c.writeMessage(StatusOK, "OK, we have the free space")
}

func (c *clientHandler) handleREST() error {
	size, err := strconv.ParseInt(c.param, 10, 0)
	if err != nil {
		return c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("Couldn't parse size: %v", err))

	}
	c.ctxRest = size
	return c.writeMessage(StatusFileActionPending, "OK")
}

func (c *clientHandler) handleMDTM() error {
	path := c.absPath(c.param)
	info, err := c.driver.GetFileInfo(c, path)
	if err != nil {
		return c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %q: %s", path, err.Error()))
	}
	return c.writeMessage(StatusFileOK, info.ModTime().UTC().Format(dateFormatMLSD))
}
