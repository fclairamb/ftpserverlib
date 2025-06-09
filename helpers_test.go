package ftpserver

import (
	"testing"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

// newClientWithRawConn creates a test server and returns a connected client and
// raw connection. The resources are closed automatically when the test ends.
func newClientWithRawConn(t *testing.T) goftp.RawConn {
	t.Helper()

	server := NewTestServer(t, false)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	t.Cleanup(func() { panicOnError(client.Close()) })

	raw, err := client.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	t.Cleanup(func() { require.NoError(t, raw.Close()) })

	return raw
}

func sendAndCheck(t *testing.T, raw goftp.RawConn, cmd string, expected int) {
	t.Helper()

	code, _, err := raw.SendCommand(cmd)
	require.NoError(t, err)
	require.Equal(t, expected, code)
}
