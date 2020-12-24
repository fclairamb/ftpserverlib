package ftpserver

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fclairamb/ftpserverlib/log"
)

var errListenerAccept = errors.New("error accepting a connection")

type fakeNetError struct {
	error
	count int
}

func (e *fakeNetError) Timeout() bool {
	return false
}

func (e *fakeNetError) Temporary() bool {
	e.count++

	return e.count < 10
}

func (e *fakeNetError) Error() string {
	return e.error.Error()
}

type fakeListener struct {
	server net.Conn
	client net.Conn
	err    error
}

func (l *fakeListener) Accept() (net.Conn, error) {
	return l.client, l.err
}

func (l *fakeListener) Close() error {
	errClient := l.client.Close()
	errServer := l.server.Close()

	if errServer != nil {
		return errServer
	}

	return errClient
}

func (l *fakeListener) Addr() net.Addr {
	return l.server.LocalAddr()
}

func newFakeListener(err error) net.Listener {
	server, client := net.Pipe()

	return &fakeListener{
		server: server,
		client: client,
		err:    err,
	}
}

func TestListernerAcceptErrors(t *testing.T) {
	errNetFake := &fakeNetError{error: errListenerAccept}

	server := FtpServer{
		listener: newFakeListener(errNetFake),
		Logger:   log.Nothing(),
	}
	err := server.Serve()
	require.EqualError(t, err, errListenerAccept.Error())
}

func TestPortCommandFormatOK(t *testing.T) {
	net, err := parsePORTAddr("127,0,0,1,239,163")
	require.NoError(t, err, "Problem parsing")
	require.Equal(t, "127.0.0.1", net.IP.String(), "Problem parsing IP")
	require.Equal(t, 239<<8+163, net.Port, "Problem parsing port")
}

func TestPortCommandFormatInvalid(t *testing.T) {
	badFormats := []string{
		"127,0,0,1,239,",
		"127,0,0,1,1,1,1",
	}
	for _, f := range badFormats {
		_, err := parsePORTAddr(f)
		require.Error(t, err, "This should have failed")
	}
}

func TestQuoteDoubling(t *testing.T) {
	type args struct {
		s string
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		// TODO: Add test cases.
		{"1", args{" white space"}, " white space"},
		{"1", args{` one" quote`}, ` one"" quote`},
		{"1", args{` two"" quote`}, ` two"""" quote`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, quoteDoubling(tt.args.s))
		})
	}
}
