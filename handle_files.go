// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"crypto/md5"  //nolint:gosec
	"crypto/sha1" //nolint:gosec
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
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

	if err != nil {
		return
	}

	var tr net.Conn
	tr, err = c.TransferOpen()

	if err == nil {
		err = c.doFileTransfer(tr, file, write)

		// We always close the file
		if errClose := file.Close(); errClose != nil && err == nil {
			err = errClose
		}
	}

	c.TransferClose(err)
}

func (c *clientHandler) doFileTransfer(tr net.Conn, file io.ReadWriter, write bool) error {
	var err error

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
			_, err = out.Write([]byte{})
		}
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
	spl := strings.SplitN(params, " ", 2)

	if len(spl) < 2 {
		c.writeMessage(StatusSyntaxErrorParameters, "bad command")
		return
	}

	var userID, groupID int
	{
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
	}

	path := c.absPath(spl[1])

	if err := c.driver.Chown(path, userID, groupID); err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't chown: %v", err))
	} else {
		c.writeMessage(StatusOK, "Done !")
	}
}

// https://learn.akamai.com/en-us/webhelp/netstorage/netstorage-user-guide/
// GUID-AB301948-C6FF-4957-9291-FE3F02457FD0.html
func (c *clientHandler) handleSYMLINK(params string) {
	spl := strings.SplitN(params, " ", 2)

	if len(spl) < 2 {
		c.writeMessage(StatusSyntaxErrorParameters, "bad command")
		return
	}

	oldname := c.absPath(spl[0])
	newname := c.absPath(spl[1])

	if symlinkInt, ok := c.driver.(ClientDriverExtensionSymlink); !ok {
		// It's not implemented and that's not OK, it must be explicitly refused
		c.writeMessage(StatusCommandNotImplemented, "This extension hasn't been implemented !")
	} else {
		if err := symlinkInt.Symlink(oldname, newname); err != nil {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't symlink: %v", err))
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
		if info.IsDir() {
			var files []os.FileInfo
			var errList error

			defer c.multilineAnswer(StatusDirectoryStatus, fmt.Sprintf("STAT %v", c.param))()

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
			defer c.multilineAnswer(StatusFileStatus, fmt.Sprintf("STAT %v", c.param))()

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
		return nil
	}

	mtime, err := time.Parse("20060102150405", params[0])
	if err != nil {
		c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf(
			"Couldn't parse mtime, given: %s, err: %v", params[0], err))
		return nil
	}

	path := c.absPath(params[1])

	if err := c.driver.Chtimes(path, mtime, mtime); err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf(
			"Couldn't set mtime %q for %q, err: %v", mtime.Format(time.RFC3339), path, err))
		return nil
	}

	c.writeMessage(StatusFileStatus, fmt.Sprintf("Modify=%s; %s", params[0], params[1]))

	return nil
}

func (c *clientHandler) handleHASH() error {
	return c.handleGenericHash(c.selectedHashAlgo, false)
}

func (c *clientHandler) handleCRC32() error {
	return c.handleGenericHash(HASHAlgoCRC32, true)
}

func (c *clientHandler) handleMD5() error {
	return c.handleGenericHash(HASHAlgoMD5, true)
}

func (c *clientHandler) handleSHA1() error {
	return c.handleGenericHash(HASHAlgoSHA1, true)
}

func (c *clientHandler) handleSHA256() error {
	return c.handleGenericHash(HASHAlgoSHA256, true)
}

func (c *clientHandler) handleSHA512() error {
	return c.handleGenericHash(HASHAlgoSHA512, true)
}

func (c *clientHandler) handleGenericHash(algo HASHAlgo, isCustomMode bool) error {
	args := strings.SplitN(c.param, " ", 3)
	info, err := c.driver.Stat(args[0])

	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("%v: %v", c.param, err))
		return nil
	}

	if !info.Mode().IsRegular() {
		c.writeMessage(StatusActionNotTakenNoFile, fmt.Sprintf("%v is not a regular file", c.param))
		return nil
	}

	start := int64(0)
	end := info.Size()

	if isCustomMode {
		// for custom command the range can be specified in this way:
		// XSHA1 <file> <start> <end>
		if len(args) > 1 {
			start, err = strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("invalid start offset %v: %v", args[1], err))
				return nil
			}
		}

		if len(args) > 2 {
			end, err = strconv.ParseInt(args[2], 10, 64)
			if err != nil {
				c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("invalid end offset %v2: %v", args[2], err))
				return nil
			}
		}
	}
	// to support partial hash also for the HASH command we should implement RANG too,
	// but this apply also to uploads/downloads and so complicat the things, we'll add
	// this support in future improvements
	var result string
	if hasher, ok := c.driver.(ClientDriverExtensionHasher); ok {
		result, err = hasher.ComputeHash(c.absPath(args[0]), algo, start, end)
	} else {
		result, err = c.computeHashForFile(c.absPath(args[0]), algo, start, end)
	}

	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("%v: %v", args[0], err))
		return nil
	}

	hashMapping := getHashMapping()
	hashName := ""

	for k, v := range hashMapping {
		if v == algo {
			hashName = k
		}
	}

	firstLine := fmt.Sprintf("Computing %v digest", hashName)

	if isCustomMode {
		c.writeMessage(StatusFileOK, fmt.Sprintf("%v\r\n%v", firstLine, result))
		return nil
	}

	response := fmt.Sprintf("%v\r\n%v %v-%v %v %v", firstLine, hashName, start, end, result, args[0])
	c.writeMessage(StatusFileStatus, response)

	return nil
}

func (c *clientHandler) computeHashForFile(filePath string, algo HASHAlgo, start, end int64) (string, error) {
	var h hash.Hash
	var file FileTransfer
	var err error

	switch algo {
	case HASHAlgoCRC32:
		h = crc32.NewIEEE()
	case HASHAlgoMD5:
		h = md5.New() //nolint:gosec
	case HASHAlgoSHA1:
		h = sha1.New() //nolint:gosec
	case HASHAlgoSHA256:
		h = sha256.New()
	case HASHAlgoSHA512:
		h = sha512.New()
	default:
		return "", errUnknowHash
	}

	if fileTransfer, ok := c.driver.(ClientDriverExtentionFileTransfer); ok {
		file, err = fileTransfer.GetHandle(filePath, os.O_RDONLY, start)
	} else {
		file, err = c.driver.OpenFile(filePath, os.O_RDONLY, os.ModePerm)
	}

	if err != nil {
		return "", err
	}

	if start > 0 {
		_, err = file.Seek(start, io.SeekStart)
		if err != nil {
			return "", err
		}
	}

	_, err = io.CopyN(h, file, end-start)
	defer file.Close() //nolint:errcheck // we ignore close error here

	if err != nil && err != io.EOF {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
