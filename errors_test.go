package ftpserver

import (
	"bufio"
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type quotaExceededError struct{}

func (q *quotaExceededError) IsExceeded() bool {
	return true
}

func (q *quotaExceededError) Error() string {
	return "quota exceeded"
}

type fileNotAllowedError struct{}

func (f *fileNotAllowedError) IsNotAllowed() bool {
	return true
}

func (f *fileNotAllowedError) Error() string {
	return "file name not allowed"
}

func TestCustomErrorsCode(t *testing.T) {
	code := getErrorCode(&quotaExceededError{}, StatusActionNotTaken)
	assert.Equal(t, StatusActionAborted, code)
	code = getErrorCode(&fileNotAllowedError{}, StatusActionNotTaken)
	assert.Equal(t, StatusActionNotTakenNoFile, code)
	code = getErrorCode(os.ErrPermission, StatusActionNotTaken)
	assert.Equal(t, StatusActionNotTaken, code)
	code = getErrorCode(os.ErrClosed, StatusNotLoggedIn)
	assert.Equal(t, StatusNotLoggedIn, code)
}

func TestTransferCloseStorageExceeded(t *testing.T) {
	buf := bytes.Buffer{}
	h := clientHandler{writer: bufio.NewWriter(&buf)}
	h.TransferClose(&quotaExceededError{})
	require.Equal(t, "552 Issue during transfer: quota exceeded\r\n", buf.String())
}
