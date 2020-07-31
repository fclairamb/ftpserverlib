// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

func (c *clientHandler) handleSTOR() error {
	c.transferFile(true, false)
	return nil
}

func (c *clientHandler) handleAPPE() error {
	c.transferFile(true, true)
	return nil
}

func (c *clientHandler) handleRETR() error {
	c.transferFile(false, false)
	return nil
}

// File transfer, read or write, seek or not, is basically the same.
// To make sure we don't miss any step, we execute everything in order
func (c *clientHandler) transferFile(write bool, append bool) {
	var file FileTransfer
	var err error

	path := c.absPath(c.param)

	// We try to open the file
	{
		var fileFlag int
		var filePerm os.FileMode = 0777
		if write {
			fileFlag = os.O_WRONLY
			if append {
				fileFlag |= os.O_APPEND
			} else {
				fileFlag |= os.O_CREATE
				// if this isn't a resume we add the truncate flag
				// to be sure to overwrite an existing file
				if c.ctxRest == 0 {
					fileFlag |= os.O_TRUNC
				}
			}
		} else {
			fileFlag = os.O_RDONLY
		}
		if fileTransfer, ok := c.driver.(ClientDriverExtentionFileTransfer); ok {
			file, err = fileTransfer.GetHandle(path, fileFlag, c.ctxRest)
		} else {
			file, err = c.driver.OpenFile(path, fileFlag, filePerm)
		}

		// If this fail, can stop right here and reset the seek position
		if err != nil {
			c.writeMessage(550, "Could not access file: "+err.Error())
			c.ctxRest = 0
			return
		}
	}

	// Try to seek on it
	if c.ctxRest != 0 {
		if _, errSeek := file.Seek(c.ctxRest, 0); errSeek != nil {
			err = errSeek
		}
		// Whatever happens we should reset the seek position
		c.ctxRest = 0
	}

	// Start the transfer
	if err == nil {
		err = c.doTransfer(file, write)
	}

	// *ALWAYS* close the file but only save the error if there wasn't one before
	if errClose := file.Close(); errClose != nil && err == nil {
		err = errClose
	}

	if err != nil {
		// if the transfer could not be open we already sent an error
		if _, isOpenTransferErr := err.(*openTransferError); !isOpenTransferErr {
			c.writeMessage(StatusActionNotTaken, "Could not transfer file: "+err.Error())
			return
		}
	}
}

func (c *clientHandler) doTransfer(file FileTransfer, write bool) error {
	var tr net.Conn
	var err error

	if tr, err = c.TransferOpen(); err == nil {
		// Copy the data
		var in io.Reader
		var out io.Writer

		if write { // ... from the connection to the file
			in = tr
			out = file
		} else { // ... from the file to the connection
			in = file
			out = tr
		}
		// for reads io.EOF isn't an error, for writes it must be considered an error
		if written, errCopy := io.Copy(out, in); errCopy != nil && (errCopy != io.EOF || write) {
			err = errCopy
		} else {
			c.logger.Debug(
				"Stream copy finished",
				"writtenBytes", written,
			)
			if written == 0 {
				_, err = out.Write([]byte(""))
			}
		}

		c.TransferClose(err)
	}

	if err != nil {
		if fileTransferError, ok := file.(FileTransferError); ok {
			fileTransferError.TransferError(err)
		}
	}

	return err
}

func (c *clientHandler) handleCHMOD(params string) {
	spl := strings.SplitN(params, " ", 2)
	modeNb, err := strconv.ParseUint(spl[0], 8, 32)

	mode := os.FileMode(modeNb)
	path := c.absPath(spl[1])

	if err == nil {
		err = c.driver.Chmod(path, mode)
	}

	if err != nil {
		c.writeMessage(StatusActionNotTaken, err.Error())
		return
	}

	c.writeMessage(StatusOK, "SITE CHMOD command successful")
}

// https://www.raidenftpd.com/en/raiden-ftpd-doc/help-sitecmd.html (wildcard isn't supported)
func (c *clientHandler) handleCHOWN(params string) {
	// TODO: Implement it
	spl := strings.SplitN(params, " ", 2)

	if len(spl) < 2 {
		c.writeMessage(StatusSyntaxErrorParameters, "bad command")
		return
	}

	var user, group string
	{
		usergroup := strings.Split(spl[0], ":")
		user = usergroup[0]
		if len(usergroup) > 1 {
			group = usergroup[1]
		} else {
			group = ""
		}
	}

	path := c.absPath(spl[1])

	if chownInt, ok := c.driver.(ClientDriverExtensionChown); !ok {
		// It's not implemented and that's not OK, it must be explicitly refused
		c.writeMessage(StatusCommandNotImplemented, "This extension hasn't been implemented !")
	} else {
		if err := chownInt.Chown(path, user, group); err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't allocate: %v", err))
		} else {
			c.writeMessage(StatusOK, "Done !")
		}
	}
}

func (c *clientHandler) handleDELE() error {
	path := c.absPath(c.param)
	if err := c.driver.Remove(path); err == nil {
		c.writeMessage(StatusFileOK, fmt.Sprintf("Removed file %s", path))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't delete %s: %v", path, err))
	}

	return nil
}

func (c *clientHandler) handleRNFR() error {
	path := c.absPath(c.param)
	if _, err := c.driver.Stat(path); err == nil {
		c.writeMessage(StatusFileActionPending, "Sure, give me a target")
		c.ctxRnfr = path
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}

	return nil
}

func (c *clientHandler) handleRNTO() error {
	dst := c.absPath(c.param)

	if c.ctxRnfr != "" {
		if err := c.driver.Rename(c.ctxRnfr, dst); err == nil {
			c.writeMessage(StatusFileOK, "Done !")
			c.ctxRnfr = ""
		} else {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't rename %s to %s: %s", c.ctxRnfr, dst, err.Error()))
		}
	}

	return nil
}

func (c *clientHandler) handleSIZE() error {
	path := c.absPath(c.param)
	if info, err := c.driver.Stat(path); err == nil {
		c.writeMessage(StatusFileStatus, fmt.Sprintf("%d", info.Size()))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}

	return nil
}

func (c *clientHandler) handleSTATFile() error {
	path := c.absPath(c.param)

	if info, err := c.driver.Stat(path); err == nil {
		defer c.multilineAnswer(StatusSystemStatus, "System status")()

		// c.writeLine(fmt.Sprintf("%d-Status follows:", StatusSystemStatus))
		if info.IsDir() {
			var files []os.FileInfo
			var errList error

			directoryPath := c.absPath(c.param)

			if fileList, ok := c.driver.(ClientDriverExtensionFileList); ok {
				files, errList = fileList.ReadDir(directoryPath)
			} else {
				directory, errOpenFile := c.driver.Open(c.absPath(c.param))

				if errOpenFile != nil {
					c.writeMessage(500, fmt.Sprintf("Could not list: %v", errOpenFile))
					return nil
				}
				files, errList = directory.Readdir(-1)
				c.closeDirectory(directoryPath, directory)
			}

			if errList == nil {
				for _, f := range files {
					c.writeLine(fmt.Sprintf(" %s", c.fileStat(f)))
				}
			}
		} else {
			c.writeLine(fmt.Sprintf(" %s", c.fileStat(info)))
		}
	} else {
		c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not STAT: %v", err))
	}

	return nil
}

func (c *clientHandler) handleMLST() error {
	if c.server.settings.DisableMLST {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "MLST has been disabled")
		return nil
	}

	path := c.absPath(c.param)

	if info, err := c.driver.Stat(path); err == nil {
		defer c.multilineAnswer(StatusFileOK, "File details")()

		if errWrite := c.writeMLSxOutput(c.writer, info); errWrite != nil {
			return errWrite
		}
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not list: %v", err))
	}

	return nil
}

func (c *clientHandler) handleALLO() error {
	// We should probably add a method in the driver
	if size, err := strconv.Atoi(c.param); err == nil {
		if alloInt, ok := c.driver.(ClientDriverExtensionAllocate); !ok {
			c.writeMessage(StatusNotImplemented, "This extension hasn't been implemented !")
		} else {
			if errAllocate := alloInt.AllocateSpace(size); errAllocate != nil {
				c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't alloInt: %v", errAllocate))
			} else {
				c.writeMessage(StatusOK, "Done !")
			}
		}
	} else {
		c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("Couldn't parse size: %v", err))
	}

	return nil
}

func (c *clientHandler) handleREST() error {
	if size, err := strconv.ParseInt(c.param, 10, 0); err == nil {
		c.ctxRest = size
		c.writeMessage(StatusFileActionPending, "OK")
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't parse size: %v", err))
	}

	return nil
}

func (c *clientHandler) handleMDTM() error {
	path := c.absPath(c.param)
	if info, err := c.driver.Stat(path); err == nil {
		c.writeMessage(StatusFileStatus, info.ModTime().UTC().Format(dateFormatMLSD))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %s: %s", path, err.Error()))
	}

	return nil
}

// RFC draft: https://tools.ietf.org/html/draft-somers-ftp-mfxx-04#section-3.1
func (c *clientHandler) handleMFMT() error {
	params := strings.SplitN(c.param, " ", 2)
	if len(params) != 2 {
		c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf(
			"Couldn't set mtime, not enough params, given: %s", c.param))
	}

	mtime, err := time.Parse("20060102150405", params[0])
	if err != nil {
		c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf(
			"Couldn't parse mtime, given: %s, err: %v", params[0], err))
	}

	path := c.absPath(params[1])

	if err := c.driver.Chtimes(path, mtime, mtime); err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf(
			"Couldn't set mtime %q for %q, err: %v", mtime.Format(time.RFC3339), path, err))
	}

	c.writeMessage(StatusFileStatus, fmt.Sprintf("Modify=%s; %s", params[0], params[1]))

	return nil
}
