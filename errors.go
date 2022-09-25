package ftpserver

import (
	"errors"
	"fmt"
)

var (
	// ErrStorageExceeded defines the error mapped to the FTP 552 reply code.
	// As for RFC 959 this error is checked for STOR, APPE
	ErrStorageExceeded = errors.New("storage limit exceeded")
	// ErrFileNameNotAllowed defines the error mapped to the FTP 553 reply code.
	// As for RFC 959 this error is checked for STOR, APPE, RNTO
	ErrFileNameNotAllowed = errors.New("filename not allowed")
)

func getErrorCode(err error, defaultCode int) int {
	switch {
	case errors.Is(err, ErrStorageExceeded):
		return StatusActionAborted
	case errors.Is(err, ErrFileNameNotAllowed):
		return StatusActionNotTakenNoFile
	default:
		return defaultCode
	}
}

// DriverError is a wrapper is for any error that occur while contacting the drivers
type DriverError struct {
	str string
	err error
}

func NewDriverError(str string, err error) DriverError {
	return DriverError{str: str, err: err}
}

func (e DriverError) Error() string {
	return fmt.Sprintf("driver error: %s: %v", e.str, e.err)
}

func (e DriverError) Unwrap() error {
	return e.err
}

type NetworkError struct {
	str string
	err error
}

func NewNetworkError(str string, err error) NetworkError {
	return NetworkError{str: str, err: err}
}

func (e NetworkError) Error() string {
	return fmt.Sprintf("network error: %s: %v", e.str, e.err)
}

func (e NetworkError) Unwrap() error {
	return e.err
}

type FileAccessError struct {
	str string
	err error
}

func NewFileAccessError(str string, err error) FileAccessError {
	return FileAccessError{str: str, err: err}
}

func (e FileAccessError) Error() string {
	return fmt.Sprintf("file access error: %s: %v", e.str, e.err)
}

func (e FileAccessError) Unwrap() error {
	return e.err
}
