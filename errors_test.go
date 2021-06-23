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
