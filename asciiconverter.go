package ftpserver

import (
	"bufio"
	"io"
)

type convertMode int8

const (
	convertModeToCRLF convertMode = iota
	convertModeToLF

	bufferSize = 4096
)

type asciiConverter struct {
	reader    *bufio.Reader
	mode      convertMode
	remaining []byte
}

func newASCIIConverter(r io.Reader, mode convertMode) *asciiConverter {
	reader := bufio.NewReaderSize(r, bufferSize)

	return &asciiConverter{
		reader:    reader,
		mode:      mode,
		remaining: nil,
	}
}

func (c *asciiConverter) Read(bytes []byte) (int, error) {
	var data []byte
	var readBytes int
	var err error

	if len(c.remaining) > 0 {
		data = c.remaining
		c.remaining = nil
	} else {
		data, _, err = c.reader.ReadLine()
		if err != nil {
			return readBytes, err
		}
	}

	readBytes = len(data)
	if readBytes > 0 {
		maxSize := len(bytes) - 2
		if readBytes > maxSize {
			copy(bytes, data[:maxSize])
			c.remaining = data[maxSize:]

			return maxSize, nil
		}

		copy(bytes[:readBytes], data[:readBytes])
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
		return readBytes, err
	}

	lastByte, err := c.reader.ReadByte()

	if err == nil && lastByte == '\n' {
		switch c.mode {
		case convertModeToCRLF:
			bytes[readBytes] = '\r'
			bytes[readBytes+1] = '\n'
			readBytes += 2
		case convertModeToLF:
			bytes[readBytes] = '\n'
			readBytes++
		}
	}

	return readBytes, err //nolint:wrapcheck // here wrapping errors brings nothing
}
