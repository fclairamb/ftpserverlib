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
	slog "github.com/siddontang/go/log"
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
	//	logger := logrus.New()
	//	logger.Formatter = &logrus.TextFormatter{
	//		TimestampFormat: "2006-01-02 15:04:05",
	//	}
	//	//logger.SetLevel(logrus.DebugLevel)
	//	log.SetFlags(log.Lshortfile)
	//	// Use logrus for standard log output
	//	log.SetOutput(logger.Writer())
	h, err := slog.NewRotatingFileHandler("ftpserver.log", 1024*1024*30, 2)
	if err != nil {
		log.Println(err)
	}
	ftpServer.Logger = slog.NewDefault(h)
	ftpServer.Logger.SetLevel(slog.LevelTrace)
	if err := ftpServer.ListenAndServe(); err != nil {
		log.Println("Problem listening", err)
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
