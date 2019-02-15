// ftpserver allows to create your own FTP(S) server
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/r0123r/ftpserver/drivers"
	"github.com/r0123r/ftpserver/server"
)

var (
	ftpServer *server.FtpServer
)

func main() {
	// Arguments vars
	var confFile, dataDir string

	// Parsing arguments
	flag.StringVar(&confFile, "conf", "", "Configuration file")
	flag.StringVar(&dataDir, "data", "var", "Data directory")
	flag.Parse()

	// The general idea here is that if you start it without any arg, you're probably doing a local quick&dirty run
	// possibly on a windows machine, so we're better of just using a default file name and create the file.
	if confFile == "" {
		confFile = "settings.ini"
	}

	// Loading the driver
	driver, err := drivers.NewSampleDriver(dataDir, confFile)

	if err != nil {
		log.Println("err", "Could not load the driver", "err", err)
		return
	}

	// Instantiating the server by passing our driver implementation
	ftpServer = server.NewFtpServer(driver)

	// Preparing the SIGTERM handling
	go signalHandler()

	if err := ftpServer.ListenAndServe(); err != nil {
		log.Println("err", "Problem listening", "err", err)
	}
}

func signalHandler() {
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGTERM)
	for {
		switch <-ch {
		case syscall.SIGTERM:
			ftpServer.Stop()
			break
		}
	}
}
