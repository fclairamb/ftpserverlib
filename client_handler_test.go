package ftpserver

import (
	"sync"
	"testing"

	"gopkg.in/dutchcoders/goftp.v1"
)

func TestConcurrency(t *testing.T) {
	s := NewTestServer(t, false)

	nbClients := 100

	waitGroup := sync.WaitGroup{}
	waitGroup.Add(nbClients)

	for i := 0; i < nbClients; i++ {
		go func() {
			var err error
			var ftp *goftp.FTP

			if ftp, err = goftp.Connect(s.Addr()); err != nil {
				panic(err)
			}

			defer func() { panicOnError(ftp.Close()) }()

			if err = ftp.Login("test", "test"); err != nil {
				panic(err)
			}

			waitGroup.Done()
		}()
	}

	waitGroup.Wait()
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
