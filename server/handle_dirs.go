package server

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

func (c *clientHandler) absPath(p string) string {
	p2 := c.Path()

	if p == "." {
		return p2
	}

	if strings.HasPrefix(p, "/") {
		p2 = p
	} else {
		if p2 != "/" {
			p2 += "/"
		}
		p2 += p
	}

	if p2 != "/" && strings.HasSuffix(p2, "/") {
		p2 = p2[0 : len(p2)-1]
	}

	return p2
}

func (c *clientHandler) handleCWD() error {
	if c.param == ".." {
		return c.handleCDUP()
	}

	p := c.absPath(c.param)
	if err := c.driver.ChangeDirectory(c, p); err != nil {
		return c.writeMessage(550, fmt.Sprintf("CD issue: %v", err))
	}
	c.SetPath(p)
	return c.writeMessage(250, fmt.Sprintf("CD worked on %s", p))
}

func (c *clientHandler) handleMKD() error {
	p := c.absPath(c.param)
	if err := c.driver.MakeDirectory(c, p); err != nil {
		return c.writeMessage(550, fmt.Sprintf("Could not create %s : %v", p, err))
	}

	return c.writeMessage(257, fmt.Sprintf("Created dir %s", p))
}

func (c *clientHandler) handleRMD() error {
	p := c.absPath(c.param)
	if err := c.driver.DeleteFile(c, p); err != nil {
		return c.writeMessage(550, fmt.Sprintf("Could not delete dir %s: %v", p, err))
	}

	return c.writeMessage(250, fmt.Sprintf("Deleted dir %s", p))
}

func (c *clientHandler) handleCDUP() error {
	parent, _ := path.Split(c.Path())
	if parent != "/" && strings.HasSuffix(parent, "/") {
		parent = parent[0 : len(parent)-1]
	}
	if err := c.driver.ChangeDirectory(c, parent); err != nil {
		return c.writeMessage(550, fmt.Sprintf("CDUP issue: %v", err))
	}
	c.SetPath(parent)
	return c.writeMessage(250, fmt.Sprintf("CDUP worked on %s", parent))
}

func (c *clientHandler) handlePWD() error {
	return c.writeMessage(257, fmt.Sprintf("%q is the current directory", c.Path()))
}

func (c *clientHandler) handleLIST() error {
	files, err := c.driver.ListFiles(c)
	if err != nil {
		return c.writeMessage(500, fmt.Sprintf("Could not list: %v", err))
	}

	tr, err2 := c.TransferOpen()
	if err2 != nil {
		return err2
	}
	defer c.TransferClose()

	return c.dirTransferLIST(tr, files)
}

func (c *clientHandler) handleMLSD() error {
	if c.daddy.settings.DisableMLSD {
		return c.writeMessage(500, "MLSD has been disabled")
	}
	files, err := c.driver.ListFiles(c)
	if err != nil {
		return c.writeMessage(500, fmt.Sprintf("Could not list: %v", err))
	}

	tr, err2 := c.TransferOpen()
	if err2 != nil {
		return err2
	}
	defer c.TransferClose()

	return c.dirTransferMLSD(tr, files)
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

func (c *clientHandler) dirTransferLIST(w io.Writer, files []os.FileInfo) error {
	for _, file := range files {
		if _, err := fmt.Fprintf(w, "%s\r\n", c.fileStat(file)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\r\n")
	return err
}

func (c *clientHandler) dirTransferMLSD(w io.Writer, files []os.FileInfo) error {
	for _, file := range files {
		if err := c.writeMLSxOutput(w, file); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\r\n")
	return err
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
