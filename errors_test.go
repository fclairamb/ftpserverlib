package ftpserver

import (
	"bufio"
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomErrorsCode(t *testing.T) {
	code := getErrorCode(ErrStorageExceeded, StatusActionNotTaken)
	assert.Equal(t, StatusActionAborted, code)
	code = getErrorCode(ErrFileNameNotAllowed, StatusActionNotTaken)
	assert.Equal(t, StatusActionNotTakenNoFile, code)
	code = getErrorCode(os.ErrPermission, StatusActionNotTaken)
	assert.Equal(t, StatusActionNotTaken, code)
	code = getErrorCode(os.ErrClosed, StatusNotLoggedIn)
	assert.Equal(t, StatusNotLoggedIn, code)
}

func TestTransferCloseStorageExceeded(t *testing.T) {
	buf := bytes.Buffer{}
	h := clientHandler{writer: bufio.NewWriter(&buf)}
	h.TransferClose(ErrStorageExceeded)
	require.Equal(t, "552 Issue during transfer: storage limit exceeded\r\n", buf.String())
}

func TestErrorTypes(t *testing.T) {
	// a := assert.New(t)
	t.Run("DriverError", func(t *testing.T) {
		r := require.New(t)
		var err error = newDriverError("test", os.ErrPermission)
		r.Equal("driver error: test: permission denied", err.Error())
		r.ErrorIs(err, os.ErrPermission)

		var specificError DriverError
		r.ErrorAs(err, &specificError)
		r.Equal("test", specificError.str)
	})

	t.Run("NetworkError", func(t *testing.T) {
		r := require.New(t)
		var err error = newNetworkError("test", os.ErrPermission)
		r.Equal("network error: test: permission denied", err.Error())
		r.ErrorIs(err, os.ErrPermission)

		var specificError NetworkError
		r.ErrorAs(err, &specificError)
		r.Equal("test", specificError.str)
	})

	t.Run("FileAccessError", func(t *testing.T) {
		r := require.New(t)
		var err error = newFileAccessError("test", os.ErrPermission)
		r.Equal("file access error: test: permission denied", err.Error())
		r.ErrorIs(err, os.ErrPermission)
		var specificError FileAccessError
		r.ErrorAs(err, &specificError)
		r.Equal("test", specificError.str)
	})
}
