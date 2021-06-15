package ftpserver // nolint

// StorageExceededError should be implemented by errors that should return 552 code.
// As for RFC 959 this error can be returned for STOR, APPE
type StorageExceededError interface {
	IsExceeded() bool
}

// FileNameNotAllowedError should be implemented by errors that should return 553 code.
// As for RFC 959 this error can be returned for STOR, APPE, RNTO
type FileNameNotAllowedError interface {
	IsNotAllowed() bool
}

func getErrorCode(err error, defaultCode int) int {
	switch e := err.(type) {
	case StorageExceededError:
		if e.IsExceeded() {
			return StatusActionAborted
		}
	case FileNameNotAllowedError:
		if e.IsNotAllowed() {
			return StatusActionNotTakenNoFile
		}
	}

	return defaultCode
}
