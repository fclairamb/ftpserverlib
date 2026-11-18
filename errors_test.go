package ftpserver

import (
	"bufio"
	"bytes"
	"errors"
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

func TestTransferCloseDriverWriteError(t *testing.T) {
	buf := bytes.Buffer{}
	h := clientHandler{writer: bufio.NewWriter(&buf), logger: slog.New(slog.DiscardHandler)}
	h.TransferClose(nil, newNetworkError("error transferring data", ErrStorageExceeded))
	require.Equal(t, "552 Issue during transfer: storage limit exceeded\r\n", buf.String())
}

var errCloseConn = errors.New("couldn't close the connection")

type closeErrorConn struct {
	net.Conn
}

func (closeErrorConn) Close() error {
	return errCloseConn
}

func TestTransferCloseConnectionCloseError(t *testing.T) {
	buf := bytes.Buffer{}
	h := clientHandler{
		writer:   bufio.NewWriter(&buf),
		logger:   slog.New(slog.DiscardHandler),
		transfer: &activeTransferHandler{conn: closeErrorConn{}},
	}
	h.TransferClose(nil, nil)
	require.Equal(t, "550 Issue during transfer close: "+errCloseConn.Error()+"\r\n", buf.String())
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
	driverWrapped := fmt.Errorf("driver context: %w", newDriverError("calling OpenFile", ErrStorageExceeded))

	cases := []struct {
		name  string
		err   error
		reply string
		log   string
	}{
		{"driver error", newDriverError("calling GetHandle", ErrStorageExceeded), "storage limit exceeded",
			"driver error: calling GetHandle: storage limit exceeded"},
		{"network error", newNetworkError("error transferring data", os.ErrPermission), "permission denied",
			"network error: error transferring data: permission denied"},
		{"file access error", newFileAccessError("couldn't stat", os.ErrNotExist), "file does not exist",
			"file access error: couldn't stat: file does not exist"},
		{"nested library errors", newNetworkError("outer", newDriverError("inner", os.ErrClosed)),
			"file already closed", "network error: outer: driver error: inner: file already closed"},
		{"not a library error", os.ErrPermission, "permission denied", ""},
		{"library error wrapped by something else", driverWrapped, driverWrapped.Error(), ""},
		{"library error without a cause", newDriverError("calling OpenFile", DriverError{}),
			"driver error: : <nil>", "driver error: calling OpenFile: driver error: : <nil>"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			logs := &bytes.Buffer{}
			h := clientHandler{logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}

			reply := h.replyError(testCase.err)
			require.EqualError(t, reply, testCase.reply)

			if testCase.log == "" {
				require.Empty(t, logs.String())
			} else {
				require.Contains(t, logs.String(), testCase.log)
			}
		})
	}
}
