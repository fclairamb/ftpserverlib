// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"bufio"
	"io"
)

type convertMode int

const (
	convertModeToCRLF convertMode = iota
	convertModeToLF
)

type asciiConverter struct {
	reader    *bufio.Reader
	mode      convertMode
	remaining []byte
}

func newASCIIConverter(r io.Reader, mode convertMode) *asciiConverter {
	reader := bufio.NewReaderSize(r, 4096)

	return &asciiConverter{
		reader:    reader,
		mode:      mode,
		remaining: nil,
	}
}

func (c *asciiConverter) Read(p []byte) (n int, err error) {
	var data []byte

	if len(c.remaining) > 0 {
		data = c.remaining
		c.remaining = nil
	} else {
		data, _, err = c.reader.ReadLine()
		if err != nil {
			return
		}
	}

	n = len(data)
	if n > 0 {
		maxSize := len(p) - 2
		if n > maxSize {
			copy(p, data[:maxSize])
			c.remaining = data[maxSize:]

			return maxSize, nil
		}

		copy(p[:n], data[:n])
	}

	// we can have a partial read if the line is too long
	// or a trailing line without a line ending, so we check
	// the last byte to decide if we need to add a line ending.
	// This will also ensure that a file without line endings
	// will remain unchanged.
	// Please note that a binary file will likely contain
	// newline chars so it will be still corrupted if the
	// client transfers it in ASCII mode
	err = c.reader.UnreadByte()
	if err != nil {
		return
	}

	lastByte, err := c.reader.ReadByte()

	if err == nil && lastByte == '\n' {
		switch c.mode {
		case convertModeToCRLF:
			p[n] = '\r'
			p[n+1] = '\n'
			n += 2
		case convertModeToLF:
			p[n] = '\n'
			n++
		}
	}

	return n, err
}
