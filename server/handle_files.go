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
		return c.writeMessage(550, "Could not open file: "+err.Error())
	}

	tr, err := c.TransferOpen()
	if err != nil {
		return c.writeMessage(550, err.Error())
	}
	defer c.TransferClose()
	if _, err = c.storeOrAppend(tr, file); err != nil && err != io.EOF {
		return c.writeMessage(550, err.Error())
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
		return c.writeMessage(550, err.Error())
	}
	defer c.TransferClose()
	if _, err = c.download(tr, c.absPath(c.param)); err != nil && err != io.EOF {
		return c.writeMessage(550, err.Error())
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
		return c.writeMessage(550, err.Error())
	}

	return c.writeMessage(200, "SITE CHMOD command successful")
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
		return c.writeMessage(550, fmt.Sprintf("Couldn't delete %s: %v", path, err))
	}
	return c.writeMessage(250, fmt.Sprintf("Removed file %s", path))
}

func (c *clientHandler) handleRNFR() error {
	path := c.absPath(c.param)
	if _, err := c.driver.GetFileInfo(c, path); err != nil {
		return c.writeMessage(550, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}
	c.ctxRnfr = path
	return c.writeMessage(350, "Sure, give me a target")
}

func (c *clientHandler) handleRNTO() error {
	if c.ctxRnfr == "" {
		return nil
	}

	dst := c.absPath(c.param)
	if err := c.driver.RenameFile(c, c.ctxRnfr, dst); err != nil {
		return c.writeMessage(550, fmt.Sprintf("Couldn't rename %s to %s: %s", c.ctxRnfr, dst, err.Error()))
	}
	c.ctxRnfr = ""
	return c.writeMessage(250, "Done !")
}

func (c *clientHandler) handleSIZE() error {
	path := c.absPath(c.param)
	info, err := c.driver.GetFileInfo(c, path)
	if err != nil {
		return c.writeMessage(550, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}
	return c.writeMessage(213, fmt.Sprintf("%d", info.Size()))
}

func (c *clientHandler) handleSTATFile() error {
	info, err := c.driver.GetFileInfo(c, c.absPath(c.param))
	if err != nil {
		return c.writeMessage(450, fmt.Sprintf("Could not STAT: %v", err))
	}

	var files []os.FileInfo
	if info.IsDir() {
		if files, err = c.driver.ListFiles(c); err != nil {
			return c.writeMessage(450, fmt.Sprintf("Could not STAT: %v", err))
		}
	} else {
		files = []os.FileInfo{info}
	}

	if err = c.writeLine("211-Status follows:"); err != nil {
		return err
	}
	for _, f := range files {
		if err = c.writeLine(fmt.Sprintf(" %s", c.fileStat(f))); err != nil {
			return err
		}
	}
	return c.writeLine("211 End of status")
}

func (c *clientHandler) handleMLST() error {
	if c.daddy.settings.DisableMLST {
		return c.writeMessage(500, "MLST has been disabled")
	}

	path := c.absPath(c.param)
	info, err := c.driver.GetFileInfo(c, path)
	if err != nil {
		return c.writeMessage(550, fmt.Sprintf("Could not list: %v", err))
	}
	if _, err = c.conn.Write([]byte("250- File details\r\n ")); err != nil {
		return err
	}
	if err := c.writeMLSxOutput(c.conn, info); err != nil {
		return err
	}
	return c.writeMessage(250, "End of file details")
}

func (c *clientHandler) handleALLO() error {
	// We should probably add a method in the driver
	size, err := strconv.Atoi(c.param)
	if err != nil {
		return c.writeMessage(501, fmt.Sprintf("Couldn't parse size: %v", err))
	}

	if ok, err2 := c.driver.CanAllocate(c, size); err2 != nil {
		return c.writeMessage(500, fmt.Sprintf("Driver issue: %v", err2))
	} else if !ok {
		return c.writeMessage(550, "NOT OK, we don't have the free space")
	}
	return c.writeMessage(202, "OK, we have the free space")
}

func (c *clientHandler) handleREST() error {
	size, err := strconv.ParseInt(c.param, 10, 0)
	if err != nil {
		return c.writeMessage(550, fmt.Sprintf("Couldn't parse size: %v", err))

	}
	c.ctxRest = size
	return c.writeMessage(350, "OK")
}

func (c *clientHandler) handleMDTM() error {
	path := c.absPath(c.param)
	info, err := c.driver.GetFileInfo(c, path)
	if err != nil {
		return c.writeMessage(550, fmt.Sprintf("Couldn't access %q: %s", path, err.Error()))
	}
	return c.writeMessage(250, info.ModTime().UTC().Format(dateFormatMLSD))
}
