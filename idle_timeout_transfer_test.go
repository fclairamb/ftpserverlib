package ftpserver

import (
	"bytes"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/require"
)

// TestIdleTimeoutDuringTransfer verifies that the idle timeout doesn't close
// the control connection when a data transfer is active.
func TestIdleTimeoutDuringTransfer(t *testing.T) {
	// Create a server with a very short idle timeout
	// The test driver adds 500ms delay for files with "delay-io" in the name
	server := NewTestServerWithTestDriver(t, &TestServerDriver{
		Debug: true,
		Settings: &Settings{
			IdleTimeout: 1, // 1 second idle timeout
		},
	})

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	client, err := goftp.DialConfig(conf, server.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(client.Close()) }()

	// Create test data - size chosen so that with 500ms delays per read,
	// the transfer will take longer than the 1 second idle timeout
	data := make([]byte, 1024*1024) // 1MB
	for i := range data {
		data[i] = byte(i % 256)
	}

	// Upload the file with "delay-io" in the name to trigger slow I/O
	// This will cause each Read() operation to take 500ms
	err = client.Store("delay-io-test.bin", bytes.NewReader(data))
	require.NoError(t, err, "Failed to upload file")

	// Download the file - this will trigger multiple 500ms delays
	// Total time will exceed the 1 second idle timeout
	// The server should extend the deadline during the active transfer
	buf := &bytes.Buffer{}
	start := time.Now()
	err = client.Retrieve("delay-io-test.bin", buf)
	elapsed := time.Since(start)

	require.NoError(t, err, "Transfer should succeed despite idle timeout")
	require.Equal(t, len(data), buf.Len(), "Downloaded data should match uploaded data")
	require.Equal(t, data, buf.Bytes(), "Downloaded content should match uploaded content")

	// Verify the transfer took longer than the idle timeout
	// This proves the deadline was extended during the transfer
	require.Greater(t, elapsed, time.Duration(server.settings.IdleTimeout)*time.Second,
		"Transfer should take longer than idle timeout to verify deadline extension worked")

	// Verify the connection is still alive after the transfer
	_, err = client.ReadDir("/")
	require.NoError(t, err, "Connection should still be alive after long transfer")
}
