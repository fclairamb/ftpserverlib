// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"crypto/md5"  //nolint:gosec
	"crypto/sha1" //nolint:gosec
	"crypto/sha256"
	"crypto/sha512"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (c *clientHandler) handleSTOR(param string) error {
	info := fmt.Sprintf("STOR %v", param)
	c.transferFile(true, false, param, info)

	return nil
}

func (c *clientHandler) handleAPPE(param string) error {
	info := fmt.Sprintf("APPE %v", param)
	c.transferFile(true, true, param, info)

	return nil
}

func (c *clientHandler) handleRETR(param string) error {
	info := fmt.Sprintf("RETR %v", param)
	c.transferFile(false, false, param, info)

	return nil
}

// File transfer, read or write, seek or not, is basically the same.
// To make sure we don't miss any step, we execute everything in order
func (c *clientHandler) transferFile(write bool, append bool, param, info string) {
	var file FileTransfer
	var err error
	var fileFlag int

	path := c.absPath(param)

	// We try to open the file
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

	file, err = c.getFileHandle(path, fileFlag, c.ctxRest)

	// If this fail, can stop right here and reset the seek position
	if err != nil {
		if !c.isCommandAborted() {
			c.writeMessage(StatusActionNotTaken, "Could not access file: "+err.Error())
		}

		c.ctxRest = 0

		return
	}

	// Try to seek on it
	if c.ctxRest != 0 {
		_, err = file.Seek(c.ctxRest, 0)
		// Whatever happens we should reset the seek position
		c.ctxRest = 0

		if err != nil {
			// if we are unable to seek we can stop right here and close the file
			if !c.isCommandAborted() {
				c.writeMessage(StatusActionNotTaken, "Could not seek file: "+err.Error())
			}
			// we can ignore the close error here
			c.closeUnchecked(file)

			return
		}
	}

	tr, err := c.TransferOpen(info)
	if err != nil {
		// an error is already returned to the FTP client
		// we can stop right here and close the file ignoring close error if any
		c.closeUnchecked(file)

		return
	}

	err = c.doFileTransfer(tr, file, write)
	// we ignore close error for reads
	if errClose := file.Close(); errClose != nil && err == nil && write {
		err = errClose
	}

	// closing the transfer we also send the response message to the FTP client
	c.TransferClose(err)
}

func (c *clientHandler) doFileTransfer(tr net.Conn, file io.ReadWriter, write bool) error {
	var err error
	var in io.Reader
	var out io.Writer

	conversionMode := convertModeToCRLF

	// Copy the data
	if write { // ... from the connection to the file
		in = tr
		out = file

		if runtime.GOOS != "windows" {
			conversionMode = convertModeToLF
		}
	} else { // ... from the file to the connection
		in = file
		out = tr
	}

	if c.currentTransferType == TransferTypeASCII {
		in = newASCIIConverter(in, conversionMode)
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

func (c *clientHandler) handleCOMB(param string) error {
	if !c.server.settings.EnableCOMB {
		// if disabled the client should not arrive here as COMB support is not declared in the FEAT response
		c.writeMessage(StatusCommandNotImplemented, "COMB support is disabled")

		return nil
	}

	relativePaths, err := unquoteSpaceSeparatedParams(param)
	if err != nil || len(relativePaths) < 2 {
		c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("invalid COMB parameters: %v", param))

		return nil
	}

	targetPath := c.absPath(relativePaths[0])

	sourcePaths := make([]string, 0, len(relativePaths)-1)
	for _, src := range relativePaths[1:] {
		sourcePaths = append(sourcePaths, c.absPath(src))
	}
	// if targetPath exists we have append to it
	// partial files will be deleted if COMB succeeded
	_, err = c.driver.Stat(targetPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not access file %#v: %v", targetPath, err))

		return nil
	}

	fileFlag := os.O_WRONLY
	if errors.Is(err, os.ErrNotExist) {
		fileFlag |= os.O_CREATE
	} else {
		fileFlag |= os.O_APPEND
	}

	c.combineFiles(targetPath, fileFlag, sourcePaths)

	return nil
}

func (c *clientHandler) combineFiles(targetPath string, fileFlag int, sourcePaths []string) {
	file, err := c.getFileHandle(targetPath, fileFlag, 0)
	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not access file %#v: %v", targetPath, err))

		return
	}

	for _, partial := range sourcePaths {
		var src FileTransfer

		src, err = c.getFileHandle(partial, os.O_RDONLY, 0)
		if err != nil {
			c.closeUnchecked(file)
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not access file %#v: %v", partial, err))

			return
		}

		_, err = io.Copy(file, src)
		if err != nil {
			c.closeUnchecked(src)
			c.closeUnchecked(file)
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could combine file %#v: %v", partial, err))

			return
		}

		c.closeUnchecked(src)

		err = c.driver.Remove(partial)
		if err != nil {
			c.closeUnchecked(file)
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could delete file %#v after combine: %v", partial, err))

			return
		}
	}

	err = file.Close()
	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could close combined file %#v: %v", targetPath, err))

		return
	}

	c.writeMessage(StatusFileOK, "COMB succeeded!")
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
	spl := strings.SplitN(params, " ", 3)

	if len(spl) != 2 {
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
	spl := strings.SplitN(params, " ", 3)

	if len(spl) != 2 {
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

func (c *clientHandler) handleDELE(param string) error {
	path := c.absPath(param)
	if err := c.driver.Remove(path); err == nil {
		c.writeMessage(StatusFileOK, fmt.Sprintf("Removed file %s", path))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't delete %s: %v", path, err))
	}

	return nil
}

func (c *clientHandler) handleRNFR(param string) error {
	path := c.absPath(param)
	if _, err := c.driver.Stat(path); err == nil {
		c.writeMessage(StatusFileActionPending, "Sure, give me a target")
		c.ctxRnfr = path
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}

	return nil
}

func (c *clientHandler) handleRNTO(param string) error {
	dst := c.absPath(param)

	if c.ctxRnfr != "" {
		if err := c.driver.Rename(c.ctxRnfr, dst); err == nil {
			c.writeMessage(StatusFileOK, "Done !")
			c.ctxRnfr = ""
		} else {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't rename %s to %s: %s", c.ctxRnfr, dst, err.Error()))
		}
	} else {
		c.writeMessage(StatusBadCommandSequence, "RNFR is expected before RNTO")
	}

	return nil
}

// properly handling the SIZE command when TYPE ASCII is used would
// require to scan the entire file to perform the ASCII translation
// logic. Considering that calculating such result could be very
// resource-intensive and also dangerous (DoS) we reject SIZE when
// the current TYPE is ASCII.
// However, clients in general should not be resuming downloads
// in ASCII mode. Resuming downloads in binary mode is the
// recommended way as specified in RFC-3659
func (c *clientHandler) handleSIZE(param string) error {
	if c.currentTransferType == TransferTypeASCII {
		c.writeMessage(StatusActionNotTaken, "SIZE not allowed in ASCII mode")

		return nil
	}

	path := c.absPath(param)
	if info, err := c.driver.Stat(path); err == nil {
		c.writeMessage(StatusFileStatus, fmt.Sprintf("%d", info.Size()))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %s: %v", path, err))
	}

	return nil
}

func (c *clientHandler) handleSTATFile(param string) error {
	path := c.absPath(param)

	if info, err := c.driver.Stat(path); err == nil {
		if info.IsDir() {
			var files []os.FileInfo
			var errList error

			defer c.multilineAnswer(StatusDirectoryStatus, fmt.Sprintf("STAT %v", param))()

			directoryPath := c.absPath(param)

			if fileList, ok := c.driver.(ClientDriverExtensionFileList); ok {
				files, errList = fileList.ReadDir(directoryPath)
			} else {
				directory, errOpenFile := c.driver.Open(c.absPath(param))

				if errOpenFile != nil {
					c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not list: %v", errOpenFile))

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
			defer c.multilineAnswer(StatusFileStatus, fmt.Sprintf("STAT %v", param))()

			c.writeLine(fmt.Sprintf(" %s", c.fileStat(info)))
		}
	} else {
		c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not STAT: %v", err))
	}

	return nil
}

func (c *clientHandler) handleMLST(param string) error {
	if c.server.settings.DisableMLST {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "MLST has been disabled")

		return nil
	}

	path := c.absPath(param)

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

func (c *clientHandler) handleALLO(param string) error {
	// We should probably add a method in the driver
	if size, err := strconv.Atoi(param); err == nil {
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

func (c *clientHandler) handleREST(param string) error {
	if size, err := strconv.ParseInt(param, 10, 0); err == nil {
		if c.currentTransferType == TransferTypeASCII {
			c.writeMessage(StatusSyntaxErrorParameters, "Resuming transfers not allowed in ASCII mode")

			return nil
		}

		c.ctxRest = size
		c.writeMessage(StatusFileActionPending, "OK")
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't parse size: %v", err))
	}

	return nil
}

func (c *clientHandler) handleMDTM(param string) error {
	path := c.absPath(param)
	if info, err := c.driver.Stat(path); err == nil {
		c.writeMessage(StatusFileStatus, info.ModTime().UTC().Format(dateFormatMLSD))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't access %s: %s", path, err.Error()))
	}

	return nil
}

// RFC draft: https://tools.ietf.org/html/draft-somers-ftp-mfxx-04#section-3.1
func (c *clientHandler) handleMFMT(param string) error {
	params := strings.SplitN(param, " ", 2)
	if len(params) != 2 {
		c.writeMessage(StatusSyntaxErrorNotRecognised, fmt.Sprintf(
			"Couldn't set mtime, not enough params, given: %s", param))

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

func (c *clientHandler) handleHASH(param string) error {
	return c.handleGenericHash(param, c.selectedHashAlgo, false)
}

func (c *clientHandler) handleCRC32(param string) error {
	return c.handleGenericHash(param, HASHAlgoCRC32, true)
}

func (c *clientHandler) handleMD5(param string) error {
	return c.handleGenericHash(param, HASHAlgoMD5, true)
}

func (c *clientHandler) handleSHA1(param string) error {
	return c.handleGenericHash(param, HASHAlgoSHA1, true)
}

func (c *clientHandler) handleSHA256(param string) error {
	return c.handleGenericHash(param, HASHAlgoSHA256, true)
}

func (c *clientHandler) handleSHA512(param string) error {
	return c.handleGenericHash(param, HASHAlgoSHA512, true)
}

func (c *clientHandler) handleGenericHash(param string, algo HASHAlgo, isCustomMode bool) error {
	if !c.server.settings.EnableHASH {
		// if disabled the client should not arrive here as HASH support is not declared in the FEAT response
		c.writeMessage(StatusCommandNotImplemented, "File hash support is disabled")

		return nil
	}

	args := strings.SplitN(param, " ", 3)
	info, err := c.driver.Stat(args[0])

	if err != nil {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("%v: %v", param, err))

		return nil
	}

	if !info.Mode().IsRegular() {
		c.writeMessage(StatusActionNotTakenNoFile, fmt.Sprintf("%v is not a regular file", param))

		return nil
	}

	start := int64(0)
	end := info.Size()

	// to support partial hash also for the HASH command, we should implement RANG,
	// but it applies also to uploads/downloads and so it complicates their handling,
	// we'll add this support in future improvements
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
				c.writeMessage(StatusSyntaxErrorParameters, fmt.Sprintf("invalid end offset %v: %v", args[2], err))

				return nil
			}
		}
	}

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

	hashName := getHashName(algo)
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

	file, err = c.getFileHandle(filePath, os.O_RDONLY, start)

	if err != nil {
		return "", err
	}

	defer c.closeUnchecked(file) // we ignore close error here

	if start > 0 {
		_, err = file.Seek(start, io.SeekStart)
		if err != nil {
			return "", err
		}
	}

	_, err = io.CopyN(h, file, end-start)

	if err != nil && err != io.EOF {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *clientHandler) getFileHandle(name string, flags int, offset int64) (FileTransfer, error) {
	if fileTransfer, ok := c.driver.(ClientDriverExtentionFileTransfer); ok {
		return fileTransfer.GetHandle(name, flags, offset)
	}

	return c.driver.OpenFile(name, flags, os.ModePerm)
}

func (c *clientHandler) closeUnchecked(file io.Closer) {
	if err := file.Close(); err != nil {
		c.logger.Warn(
			"Problem closing a file",
			"err", err,
		)
	}
}

// This method split params by spaces, except when the space is inside quotes.
// It was introduced to support COMB command. Supported COMB examples:
//
// - Append a single part onto an existing (or new) file: e.g., COMB "final.log" "132.log".
// - Target and source files do not require enclosing quotes UNLESS the filename includes spaces:
//   - COMB final5.log 64.log 65.log
//   - COMB "final5.log" "64.log" "65.log"
//   - COMB final7.log "6 6.log" 67.log
func unquoteSpaceSeparatedParams(params string) ([]string, error) {
	reader := csv.NewReader(strings.NewReader(params))
	reader.Comma = ' ' // space

	return reader.Read()
}
