package ftpserver

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"net"
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
	h.TransferClose(nil, ErrStorageExceeded)
	require.Equal(t, "552 Issue during transfer: storage limit exceeded\r\n", buf.String())
}

func TestErrorTypes(t *testing.T) {
	// a := assert.New(t)
	t.Run("DriverError", func(t *testing.T) {
		req := require.New(t)
		var err error = newDriverError("test", os.ErrPermission)
		req.Equal("driver error: test: permission denied", err.Error())
		req.ErrorIs(err, os.ErrPermission)

		var specificError DriverError
		req.ErrorAs(err, &specificError)
		req.Equal("test", specificError.str)
	})

	t.Run("NetworkError", func(t *testing.T) {
		req := require.New(t)
		var err error = newNetworkError("test", os.ErrPermission)
		req.Equal("network error: test: permission denied", err.Error())
		req.ErrorIs(err, os.ErrPermission)

		var specificError NetworkError
		req.ErrorAs(err, &specificError)
		req.Equal("test", specificError.str)
	})

	t.Run("FileAccessError", func(t *testing.T) {
		req := require.New(t)
		var err error = newFileAccessError("test", os.ErrPermission)
		req.Equal("file access error: test: permission denied", err.Error())
		req.ErrorIs(err, os.ErrPermission)
		var specificError FileAccessError
		req.ErrorAs(err, &specificError)
		req.Equal("test", specificError.str)
	})
}

func TestReplyError(t *testing.T) {
	opErr := &net.OpError{Op: "writeto", Net: "tcp", Err: errFailWrite}
	otherWrapping := fmt.Errorf("context: %w", newDriverError("calling OpenFile", ErrStorageExceeded))

	cases := []struct {
		name  string
		err   error
		reply error
	}{
		{"library wrappers", newNetworkError("a", newFileAccessError("b", newDriverError("c", ErrStorageExceeded))),
			ErrStorageExceeded},
		{"net.OpError", opErr, errFailWrite},
		{"both nested", newNetworkError("error transferring data", opErr), errFailWrite},
		{"wrapper without a cause", newDriverError("calling OpenFile", DriverError{}), DriverError{}},
		{"wrapped by something else", otherWrapping, otherWrapping},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := clientHandler{logger: slog.New(slog.DiscardHandler)}
			require.EqualError(t, h.replyError(testCase.err), testCase.reply.Error())
		})
	}
}
