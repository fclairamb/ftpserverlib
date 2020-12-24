package ftpserver

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/secsy/goftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcurrency(t *testing.T) {
	server := NewTestServer(t, false)

	nbClients := 100

	waitGroup := sync.WaitGroup{}
	waitGroup.Add(nbClients)

	for i := 0; i < nbClients; i++ {
		go func() {
			conf := goftp.Config{
				User:     authUser,
				Password: authPass,
			}

			var err error
			var c *goftp.Client

			if c, err = goftp.DialConfig(conf, server.Addr()); err != nil {
				panic(fmt.Sprintf("Couldn't connect: %v", err))
			}

			if _, err = c.ReadDir("/"); err != nil {
				panic(fmt.Sprintf("Couldn't list dir: %v", err))
			}

			defer func() { panicOnError(c.Close()) }()

			waitGroup.Done()
		}()
	}

	waitGroup.Wait()
}

func TestLastCommand(t *testing.T) {
	cc := clientHandler{}
	assert.Empty(t, cc.GetLastCommand())
}

func TestTransferOpenError(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	// we send STOR without opening a transfer connection
	rc, response, err := raw.SendCommand("STOR file")
	require.NoError(t, err)
	require.Equal(t, StatusActionNotTaken, rc)
	require.Equal(t, "unable to open transfer: no transfer connection", response)
}

func TestTLSMethods(t *testing.T) {
	t.Run("without-tls", func(t *testing.T) {
		cc := clientHandler{
			server: NewTestServer(t, true),
		}
		require.False(t, cc.HasTLSForControl())
		require.False(t, cc.HasTLSForTransfers())
	})

	t.Run("with-implicit-tls", func(t *testing.T) {
		s := NewTestServerWithDriver(t, &TestServerDriver{
			Settings: &Settings{
				TLSRequired: ImplicitEncryption,
			},
			TLS:   true,
			Debug: true,
		})
		cc := clientHandler{
			server: s,
		}
		require.True(t, cc.HasTLSForControl())
		require.True(t, cc.HasTLSForTransfers())
	})
}

func TestCloseConnection(t *testing.T) {
	driver := &TestServerDriver{
		Debug: true,
	}
	s := NewTestServerWithDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	ftpUpload(t, c, createTemporaryFile(t, 1024*1024), "file.bin")

	require.Len(t, driver.GetClientsInfo(), 1)

	err = c.Rename("file.bin", "delay-io.bin")
	require.NoError(t, err)

	raw, err := c.OpenRawConn()
	require.NoError(t, err)

	defer func() { require.NoError(t, raw.Close()) }()

	require.Len(t, driver.GetClientsInfo(), 2)

	err = driver.DisconnectClient()
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return len(driver.GetClientsInfo()) == 1
	}, 1*time.Second, 50*time.Millisecond)

	err = driver.DisconnectClient()
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return len(driver.GetClientsInfo()) == 0
	}, 1*time.Second, 50*time.Millisecond)
}

func TestClientContextConcurrency(t *testing.T) {
	driver := &TestServerDriver{}
	s := NewTestServerWithDriver(t, driver)

	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	done := make(chan bool, 1)
	connected := make(chan bool, 1)

	go func() {
		_, err := c.Getwd()
		assert.NoError(t, err)
		connected <- true

		counter := 0

		for counter < 100 {
			_, err := c.Getwd()
			assert.NoError(t, err)
			counter++
		}

		done <- true
	}()

	<-connected

	isDone := false
	for !isDone {
		info := driver.GetClientsInfo()
		assert.Len(t, info, 1)

		select {
		case <-done:
			isDone = true
		default:
		}
	}
}

type multilineMessage struct {
	message       string
	expectedLines []string
}

func TestMultiLineMessages(t *testing.T) {
	testMultilines := []multilineMessage{
		{
			message:       "single line",
			expectedLines: []string{"single line"},
		},
		{
			message:       "",
			expectedLines: []string{""},
		},
		{
			message:       "first line\r\nsecond line\r\n",
			expectedLines: []string{"first line", "second line"},
		},
		{
			message:       "first line\nsecond line\n",
			expectedLines: []string{"first line", "second line"},
		},
		{
			message:       "first line\rsecond line",
			expectedLines: []string{"first line\rsecond line"},
		},
		{
			message: `first line

second line

`,
			expectedLines: []string{"first line", "", "second line", ""},
		},
	}

	for _, msg := range testMultilines {
		lines := getMessageLines(msg.message)
		if len(lines) != len(msg.expectedLines) {
			t.Errorf("unexpected number of lines got: %v want: %v", len(lines), len(msg.expectedLines))
		}

		for _, line := range lines {
			if !isStringInSlice(line, msg.expectedLines) {
				t.Errorf("unexpected line %#v", line)
			}
		}
	}
}

func isStringInSlice(s string, list []string) bool {
	for _, c := range list {
		if s == c {
			return true
		}
	}

	return false
}

func TestUnknownCommand(t *testing.T) {
	s := NewTestServer(t, true)
	conf := goftp.Config{
		User:     authUser,
		Password: authPass,
	}

	c, err := goftp.DialConfig(conf, s.Addr())
	require.NoError(t, err, "Couldn't connect")

	defer func() { panicOnError(c.Close()) }()

	raw, err := c.OpenRawConn()
	require.NoError(t, err, "Couldn't open raw connection")

	defer func() { require.NoError(t, raw.Close()) }()

	cmd := "UNSUPPORTED"
	rc, response, err := raw.SendCommand(cmd)
	require.NoError(t, err)
	require.Equal(t, StatusSyntaxErrorNotRecognised, rc)
	require.Equal(t, fmt.Sprintf("Unknown command %#v", cmd), response)
}
