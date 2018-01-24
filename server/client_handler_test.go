package server

import (
	"testing"

	"gopkg.in/dutchcoders/goftp.v1"
)

func TestConcurrency(t *testing.T) {
	s := NewTestServer(false)
	defer s.Stop()

	nbClients := 100
	results := make(chan error, nbClients)
	for i := 0; i < nbClients; i++ {
		go func() {
			ftp, err := goftp.Connect(s.Addr())
			if err != nil {
				results <- err
				return
			}
			defer ftp.Close()

			results <- ftp.Login("test", "test")
		}()
	}

	for i := 0; i < nbClients; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}
