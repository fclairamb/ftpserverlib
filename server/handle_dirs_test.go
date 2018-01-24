package server

import (
	"strings"
	"testing"

	"gopkg.in/dutchcoders/goftp.v1"
)

// TestDirAccess relies on LIST of files listing
func TestDirListing(t *testing.T) {
	s := NewTestServerWithDriver(&ServerDriver{Debug: true, Settings: &Settings{DisableMLSD: true}})
	defer s.Stop()

	var connErr error
	var ftp *goftp.FTP

	if ftp, connErr = goftp.Connect(s.Addr()); connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}
	defer ftp.Quit()

	if _, err := ftp.List("/"); err == nil {
		t.Fatal("We could list files before login")
	}

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	if err := ftp.Mkd("/known"); err != nil {
		t.Fatal("Couldn't create dir:", err)
	}

	if lines, err := ftp.List("/"); err != nil {
		t.Fatal("Couldn't list files:", err)
	} else {
		found := false
		for _, line := range lines {
			line = line[0 : len(line)-2]
			if len(line) < 47 {
				break
			}
			fileName := line[47:]
			t.Logf("Line: \"%s\", File: \"%s\"", line, fileName)
			if fileName == "known" {
				found = true
			}
		}
		if !found {
			t.Fatal("Couldn't find the dir")
		}
	}
}

// TestDirAccess relies on LIST of files listing
func TestDirHandling(t *testing.T) {
	s := NewTestServer(true)
	defer s.Stop()

	var connErr error
	var ftp *goftp.FTP

	if ftp, connErr = goftp.Connect(s.Addr()); connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}
	defer ftp.Quit()

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	if path, err := ftp.Pwd(); err != nil {
		t.Fatal("Couldn't test PWD", err)
	} else if path != "/" {
		t.Fatal("Bad path:", path)
	}

	if err := ftp.Cwd("/unknown"); err == nil {
		t.Fatal("We should have had an error")
	}

	if err := ftp.Mkd("/known"); err != nil {
		t.Fatal("Couldn't create dir:", err)
	}

	if err := ftp.Cwd("/known"); err != nil {
		t.Fatal("Couldn't access the known dir:", err)
	}

	if err := ftp.Rmd("/known"); err != nil {
		t.Fatal("Couldn't ftpDelete the known dir:", err)
	}

	if err := ftp.Rmd("/known"); err == nil {
		t.Fatal("We shouldn't have been able to ftpDelete known again")
	}
}

// TestDirListingWithSpace uses the MLSD for files listing
func TestDirListingWithSpace(t *testing.T) {
	s := NewTestServer(true)
	defer s.Stop()

	ftp, connErr := goftp.Connect(s.Addr())
	if connErr != nil {
		t.Fatal("Couldn't connect", connErr)
	}
	defer ftp.Quit()

	if err := ftp.Login("test", "test"); err != nil {
		t.Fatal("Failed to login:", err)
	}

	if err := ftp.Mkd("/ with spaces "); err != nil {
		t.Fatal("Couldn't create dir:", err)
	}

	lines, err := ftp.List("/")
	if err != nil {
		t.Fatal("Couldn't list files:", err)
	}

	found := false
	for _, line := range lines {
		line = line[0 : len(line)-2]
		if len(line) < 47 {
			break
		}
		if strings.SplitN(line, "; ", 2)[1] == " with spaces " {
			found = true
			break
		}
	}

	if !found {
		t.Fatal("Couldn't find the dir")
	}

	if err := ftp.Cwd("/ with spaces "); err != nil {
		t.Fatal("Couldn't access the known dir:", err)
	}
}
