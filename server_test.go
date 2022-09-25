package ftpserver

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	lognoop "github.com/fclairamb/go-log/noop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// we change the timezone to be able to test that MLSD/MLST commands write UTC timestamps
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		fmt.Printf("unable to set timezone: %v\n", err)
		os.Exit(1)
	}

	time.Local = loc

	os.Exit(m.Run())
}

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

func TestCannotListen(t *testing.T) {
	a := assert.New(t)

	portBlockerListener, err := net.Listen("tcp", "127.0.0.1:0")
	a.NoError(err)

	defer portBlockerListener.Close()

	server := FtpServer{
		Logger: lognoop.NewNoOpLogger(),
		driver: &TestServerDriver{
			Settings: &Settings{
				ListenAddr: portBlockerListener.Addr().String(),
			},
		},
	}

	err = server.Listen()
	a.Error(err)
}

func TestListenerAcceptErrors(t *testing.T) {
	errNetFake := &fakeNetError{error: errListenerAccept}

	server := FtpServer{
		listener: newFakeListener(errNetFake),
		Logger:   lognoop.NewNoOpLogger(),
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

func TestServerSettings(t *testing.T) {
	server := FtpServer{
		Logger: lognoop.NewNoOpLogger(),
		driver: &TestServerDriver{
			Settings: &Settings{
				PublicHost: "127.0.0",
			},
		},
	}
	err := server.loadSettings()
	_, ok := err.(*ipValidationError)
	require.True(t, ok)

	server.driver = &TestServerDriver{
		Settings: &Settings{
			PublicHost: "::1",
		},
	}
	err = server.loadSettings()
	_, ok = err.(*ipValidationError)
	require.True(t, ok)

	server.driver = &TestServerDriver{
		Settings: &Settings{
			PublicHost: "::ffff:192.168.1.1",
		},
	}
	err = server.loadSettings()
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1", server.settings.PublicHost)
}

func TestTemporaryError(t *testing.T) {
	a := assert.New(t)

	// Test the temporaryError function
	a.False(temporaryError(nil))
	a.False(temporaryError(&fakeNetError{error: errListenerAccept}))
	a.False(temporaryError(&net.OpError{
		Err: &fakeNetError{error: errListenerAccept},
	}))

	for _, serr := range []syscall.Errno{syscall.ECONNABORTED, syscall.ECONNRESET} {
		a.True(temporaryError(&net.OpError{Err: &os.SyscallError{Err: serr}}))
	}

	a.False(temporaryError(&net.OpError{Err: &os.SyscallError{Err: syscall.EAGAIN}}))
}
