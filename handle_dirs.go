package ftpserver

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/afero"
)

// thrown if listing with a filePath isn't supported (MLSD, NLST)
var errFileList = errors.New("listing a file isn't allowed")

// the order matter, put parameters with more characters first
var supportedlistArgs = []string{"-al", "-la", "-a", "-l"}

func (c *clientHandler) absPath(p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}

	return path.Join(c.Path(), p)
}

// getRelativePath returns the specified path as relative to the
// current working directory. The specified path must be cleaned
func (c *clientHandler) getRelativePath(p string) string {
	var sb strings.Builder
	base := c.Path()

	for {
		if base == p {
			return sb.String()
		}

		if !strings.HasSuffix(base, "/") {
			base += "/"
		}

		if strings.HasPrefix(p, base) {
			sb.WriteString(strings.TrimPrefix(p, base))

			return sb.String()
		}

		if base == "/" || base == "./" {
			return p
		}

		sb.WriteString("../")

		base = path.Dir(path.Clean(base))
	}
}

func (c *clientHandler) handleCWD(param string) error {
	p := c.absPath(param)

	if stat, err := c.driver.Stat(p); err == nil {
		if stat.IsDir() {
			c.SetPath(p)
			c.writeMessage(StatusFileOK, fmt.Sprintf("CD worked on %s", p))
		} else {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Can't change directory to %s: Not a Directory", p))
		}
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("CD issue: %v", err))
	}

	return nil
}

func (c *clientHandler) handleMKD(param string) error {
	p := c.absPath(param)
	if err := c.driver.Mkdir(p, 0755); err == nil {
		// handleMKD confirms to "quote-doubling"
		// https://tools.ietf.org/html/rfc959 , page 63
		c.writeMessage(StatusPathCreated, fmt.Sprintf(`Created dir "%s"`, quoteDoubling(p)))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf(`Could not create "%s" : %v`, quoteDoubling(p), err))
	}

	return nil
}

func (c *clientHandler) handleMKDIR(params string) {
	if params == "" {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Missing path")

		return
	}

	p := c.absPath(params)

	if err := c.driver.MkdirAll(p, 0755); err == nil {
		c.writeMessage(StatusFileOK, fmt.Sprintf("Created dir %s", p))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't create dir %s: %v", p, err))
	}
}

func (c *clientHandler) handleRMD(param string) error {
	var err error

	p := c.absPath(param)

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

func (c *clientHandler) handleRMDIR(params string) {
	if params == "" {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "Missing path")

		return
	}

	p := c.absPath(params)

	if err := c.driver.RemoveAll(p); err == nil {
		c.writeMessage(StatusFileOK, fmt.Sprintf("Removed dir %s", p))
	} else {
		c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Couldn't remove dir %s: %v", p, err))
	}
}

func (c *clientHandler) handleCDUP(_ string) error {
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

func (c *clientHandler) handlePWD(_ string) error {
	c.writeMessage(StatusPathCreated, fmt.Sprintf(`"%s" is the current directory`, quoteDoubling(c.Path())))

	return nil
}

func (c *clientHandler) checkLISTArgs(args string) string {
	result := args
	param := strings.ToLower(args)

	for _, arg := range supportedlistArgs {
		if strings.HasPrefix(param, arg) {
			// a check for a non-existent directory error is more appropriate here
			// but we cannot assume that the driver implementation will return an
			// os.IsNotExist error.
			if _, err := c.driver.Stat(args); err != nil {
				params := strings.SplitN(args, " ", 2)
				if len(params) == 1 {
					result = ""
				} else {
					result = params[1]
				}
			}
		}
	}

	return result
}

func (c *clientHandler) handleLIST(param string) error {
	info := fmt.Sprintf("LIST %v", param)

	if files, _, err := c.getFileList(param, true); err == nil || errors.Is(err, io.EOF) {
		if tr, errTr := c.TransferOpen(info); errTr == nil {
			err = c.dirTransferLIST(tr, files)
			c.TransferClose(err)

			return nil
		}
	} else {
		if !c.isCommandAborted() {
			c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not list: %v", err))
		}
	}

	return nil
}

func (c *clientHandler) handleNLST(param string) error {
	info := fmt.Sprintf("NLST %v", param)

	if files, parentDir, err := c.getFileList(param, true); err == nil || errors.Is(err, io.EOF) {
		if tr, errTrOpen := c.TransferOpen(info); errTrOpen == nil {
			err = c.dirTransferNLST(tr, files, parentDir)
			c.TransferClose(err)

			return nil
		}
	} else {
		if !c.isCommandAborted() {
			c.writeMessage(StatusFileActionNotTaken, fmt.Sprintf("Could not list: %v", err))
		}
	}

	return nil
}

func (c *clientHandler) dirTransferNLST(w io.Writer, files []os.FileInfo, parentDir string) error {
	if len(files) == 0 {
		_, err := w.Write([]byte(""))

		if err != nil {
			err = newNetworkError("couldn't send NLST data", err)
		}

		return err
	}

	for _, file := range files {
		// Based on RFC 959 NLST is intended to return information that can be used
		// by a program to further process the files automatically.
		// So we return paths relative to the current working directory
		if _, err := fmt.Fprintf(w, "%s\r\n", path.Join(c.getRelativePath(parentDir), file.Name())); err != nil {
			return newNetworkError("couldn't send NLST data", err)
		}
	}

	return nil
}

func (c *clientHandler) handleMLSD(param string) error {
	if c.server.settings.DisableMLSD && !c.isCommandAborted() {
		c.writeMessage(StatusSyntaxErrorNotRecognised, "MLSD has been disabled")

		return nil
	}

	info := fmt.Sprintf("MLSD %v", param)

	if files, _, err := c.getFileList(param, false); err == nil || errors.Is(err, io.EOF) {
		if tr, errTr := c.TransferOpen(info); errTr == nil {
			err = c.dirTransferMLSD(tr, files)
			c.TransferClose(err)

			return nil
		}
	} else {
		if !c.isCommandAborted() {
			c.writeMessage(StatusActionNotTaken, fmt.Sprintf("Could not list: %v", err))
		}
	}

	return nil
}

const (
	dateFormatStatTime      = "Jan _2 15:04"          // LIST date formatting with hour and minute
	dateFormatStatYear      = "Jan _2  2006"          // LIST date formatting with year
	dateFormatStatOldSwitch = time.Hour * 24 * 30 * 6 // 6 months ago
	dateFormatMLSD          = "20060102150405"        // MLSD date formatting
	fakeUser                = "ftp"
	fakeGroup               = "ftp"
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
		"%s 1 %s %s %12d %s %s",
		file.Mode(),
		fakeUser,
		fakeGroup,
		file.Size(),
		file.ModTime().Format(dateFormat),
		file.Name(),
	)
}

// fclairamb (2018-02-13): #64: Removed extra empty line
func (c *clientHandler) dirTransferLIST(w io.Writer, files []os.FileInfo) error {
	if len(files) == 0 {
		_, err := w.Write([]byte(""))

		if err != nil {
			err = newNetworkError("error writing LIST entry", err)
		}

		return err
	}

	for _, file := range files {
		if _, err := fmt.Fprintf(w, "%s\r\n", c.fileStat(file)); err != nil {
			return fmt.Errorf("error writing LIST entry: %w", err)
		}
	}

	return nil
}

// fclairamb (2018-02-13): #64: Removed extra empty line
func (c *clientHandler) dirTransferMLSD(w io.Writer, files []os.FileInfo) error {
	if len(files) == 0 {
		_, err := w.Write([]byte(""))

		if err != nil {
			err = newNetworkError("error writing MLSD entry", err)
		}

		return err
	}

	for _, file := range files {
		if err := c.writeMLSxEntry(w, file); err != nil {
			return err
		}
	}

	return nil
}
func (c *clientHandler) writeMLSxEntry(w io.Writer, file os.FileInfo) error {
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
		file.ModTime().UTC().Format(dateFormatMLSD),
		file.Name(),
	)

	if err != nil {
		err = fmt.Errorf("error writing MLSD entry: %w", err)
	}

	return err
}

func (c *clientHandler) getFileList(param string, filePathAllowed bool) ([]os.FileInfo, string, error) {
	if !c.server.settings.DisableLISTArgs {
		param = c.checkLISTArgs(param)
	}
	// directory or filePath
	listPath := c.absPath(param)
	c.SetListPath(listPath)

	// return list of single file if directoryPath points to file and filePathAllowed
	info, err := c.driver.Stat(listPath)
	if err != nil {
		return nil, "", newFileAccessError("couldn't stat", err)
	}

	if !info.IsDir() {
		if filePathAllowed {
			return []os.FileInfo{info}, path.Dir(c.getListPath()), nil
		}

		return nil, "", errFileList
	}

	var files []fs.FileInfo

	if fileList, ok := c.driver.(ClientDriverExtensionFileList); ok {
		files, err = fileList.ReadDir(listPath)

		return files, c.getListPath(), err
	}

	directory, errOpenFile := c.driver.Open(listPath)
	if errOpenFile != nil {
		return nil, "", newFileAccessError("couldn't open directory", errOpenFile)
	}

	defer c.closeDirectory(listPath, directory)

	files, err = directory.Readdir(-1)

	return files, c.getListPath(), err
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
