package ftpserver // nolint

import "errors"

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
