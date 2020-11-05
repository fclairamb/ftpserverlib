// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/afero"
)

// the order matter, put parameters with more characters first
var supportedlistArgs = []string{"-al", "-la", "-a", "-l"}

func (c *clientHandler) absPath(p string) string {
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}

	return path.Clean(c.Path() + "/" + p)
}

func (c *clientHandler) handleCWD() error {
	p := c.absPath(c.param)

	if _, err := c.driver.Stat(p); err == nil {
		c.SetPath(p)
		c.writeMessage(StatusFileOK, fmt.Sprintf("CD worked on %s", p))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("CD issue: %v", err))
	}

	return nil
}

func (c *clientHandler) handleMKD() error {
	p := c.absPath(c.param)
	if err := c.driver.Mkdir(p, 0755); err == nil {
		// handleMKD confirms to "qoute-doubling"
		// https://tools.ietf.org/html/rfc959 , page 63
		c.writeMessage(StatusPathCreated, fmt.Sprintf(`Created dir "%s"`, quoteDoubling(p)))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf(`Could not create "%s" : %v`, quoteDoubling(p), err))
	}

	return nil
}

func (c *clientHandler) handleRMD() error {
	var err error

	p := c.absPath(c.param)

	if rmd, ok := c.driver.(ClientDriverExtensionRemoveDir); ok {
		err = rmd.RemoveDir(p)
	} else {
		err = c.driver.Remove(p)
	}

	if err == nil {
		c.writeMessage(StatusFileOK, fmt.Sprintf("Deleted dir %s", p))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not delete dir %s: %v", p, err))
	}

	return nil
}

func (c *clientHandler) handleCDUP() error {
	parent, _ := path.Split(c.Path())
	if parent != "/" && strings.HasSuffix(parent, "/") {
		parent = parent[0 : len(parent)-1]
	}

	if _, err := c.driver.Stat(parent); err == nil {
		c.SetPath(parent)
		c.writeMessage(StatusFileOK, fmt.Sprintf("CDUP worked on %s", parent))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("CDUP issue: %v", err))
	}

	return nil
}

func (c *clientHandler) handlePWD() error {
	c.writeMessage(StatusPathCreated, "\""+c.Path()+"\" is the current directory")
	return nil
}

func (c *clientHandler) checkLISTArgs() {
	param := strings.ToLower(c.param)

	for _, arg := range supportedlistArgs {
		if strings.HasPrefix(param, arg) {
			// a check for a non-existent directory error is more appropriate here
			// but we cannot assume that the driver implementation will return an
			// os.IsNotExist error.
			if _, err := c.driver.Stat(c.param); err != nil {
				params := strings.SplitN(c.param, " ", 2)
				if len(params) == 1 {
					c.param = ""
				} else {
					c.param = params[1]
				}
			}
		}
	}
}

func (c *clientHandler) handleLIST() error {
	if !c.server.settings.DisableLISTArgs {
		c.checkLISTArgs()
	}

	if files, err := c.getFileList(); err == nil || err == io.EOF {
		if tr, errTr := c.TransferOpen(); errTr == nil {
			err = c.dirTransferLIST(tr, files)
			c.TransferClose(err)

			return err
		}
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not list: %v", err))
	}

	return nil
}

func (c *clientHandler) handleNLST() error {
	if files, err := c.getFileList(); err == nil || err == io.EOF {
		if tr, errTrOpen := c.TransferOpen(); errTrOpen == nil {
			err = c.dirTransferNLST(tr, files)
			c.TransferClose(err)

			return err
		}
	} else {
		c.writeMessage(500, fmt.Sprintf("Could not list: %v", err))
	}

	return nil
}

func (c *clientHandler) dirTransferNLST(w io.Writer, files []os.FileInfo) error {
	if len(files) == 0 {
		_, err := w.Write([]byte(""))
		return err
	}

	for _, file := range files {
		if _, err := fmt.Fprintf(w, "%s\r\n", file.Name()); err != nil {
			return err
		}
	}

	return nil
}

func (c *clientHandler) handleMLSD() error {
	if c.server.settings.DisableMLSD {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "MLSD has been disabled")
		return nil
	}

	if files, err := c.getFileList(); err == nil || err == io.EOF {
		if tr, errTr := c.TransferOpen(); errTr == nil {
			err = c.dirTransferMLSD(tr, files)
			c.TransferClose(err)

			return err
		}
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not list: %v", err))
	}

	return nil
}

const (
	dateFormatStatTime      = "Jan _2 15:04"          // LIST date formatting with hour and minute
	dateFormatStatYear      = "Jan _2  2006"          // LIST date formatting with year
	dateFormatStatOldSwitch = time.Hour * 24 * 30 * 6 // 6 months ago
	dateFormatMLSD          = "20060102150405"        // MLSD date formatting
)

func (c *clientHandler) fileStat(file os.FileInfo) string {
	modTime := file.ModTime()

	var dateFormat string

	if c.connectedAt.Sub(modTime) > dateFormatStatOldSwitch {
		dateFormat = dateFormatStatYear
	} else {
		dateFormat = dateFormatStatTime
	}

	return fmt.Sprintf(
		"%s 1 ftp ftp %12d %s %s",
		file.Mode(),
		file.Size(),
		file.ModTime().Format(dateFormat),
		file.Name(),
	)
}

// fclairamb (2018-02-13): #64: Removed extra empty line
func (c *clientHandler) dirTransferLIST(w io.Writer, files []os.FileInfo) error {
	if len(files) == 0 {
		_, err := w.Write([]byte(""))
		return err
	}

	for _, file := range files {
		if _, err := fmt.Fprintf(w, "%s\r\n", c.fileStat(file)); err != nil {
			return err
		}
	}

	return nil
}

// fclairamb (2018-02-13): #64: Removed extra empty line
func (c *clientHandler) dirTransferMLSD(w io.Writer, files []os.FileInfo) error {
	if len(files) == 0 {
		_, err := w.Write([]byte(""))
		return err
	}

	for _, file := range files {
		if err := c.writeMLSxOutput(w, file); err != nil {
			return err
		}
	}

	return nil
}
func (c *clientHandler) writeMLSxOutput(w io.Writer, file os.FileInfo) error {
	var listType string
	if file.IsDir() {
		listType = "dir"
	} else {
		listType = "file"
	}

	_, err := fmt.Fprintf(
		w,
		"Type=%s;Size=%d;Modify=%s; %s\r\n",
		listType,
		file.Size(),
		file.ModTime().Format(dateFormatMLSD),
		file.Name(),
	)

	return err
}

func (c *clientHandler) getFileList() ([]os.FileInfo, error) {
	directoryPath := c.absPath(c.param)

	if fileList, ok := c.driver.(ClientDriverExtensionFileList); ok {
		return fileList.ReadDir(directoryPath)
	}

	directory, errOpenFile := c.driver.Open(directoryPath)
	if errOpenFile != nil {
		return nil, errOpenFile
	}

	defer c.closeDirectory(directoryPath, directory)

	return directory.Readdir(-1)
}

func (c *clientHandler) closeDirectory(directoryPath string, directory afero.File) {
	if errClose := directory.Close(); errClose != nil {
		c.logger.Error("Couldn't close directory", "err", errClose, "directory", directoryPath)
	}
}

func quoteDoubling(s string) string {
	if !strings.Contains(s, "\"") {
		return s
	}

	return strings.ReplaceAll(s, "\"", `""`)
}
