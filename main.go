// ftpserver allows to create your own FTP(S) server
package main

import (
	"flag"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	"github.com/fclairamb/ftpserver/sample"
	"github.com/fclairamb/ftpserver/server"

	"github.com/sirupsen/logrus"
)

var (
	ftpServer *server.FtpServer
)

func main() {
	// Arguments vars
	var confFile, dataDir string
	var onlyConf bool

	// Parsing arguments
	flag.StringVar(&confFile, "conf", "", "Configuration file")
	flag.StringVar(&dataDir, "data", "", "Data directory")
	flag.BoolVar(&onlyConf, "conf-only", false, "Only create the config")
	flag.Parse()

	autoCreate := onlyConf

	// The general idea here is that if you start it without any arg, you're probably doing a local quick&dirty run
	// possibly on a windows machine, so we're better of just using a default file name and create the file.
	if confFile == "" {
		confFile = "settings.toml"
		autoCreate = true
	}

	if autoCreate {
		if _, err := os.Stat(confFile); err != nil {
			if os.IsNotExist(err) {
				logrus.WithFields(logrus.Fields{"action": "conf_file.create", "confFile": confFile}).Info("No config file, creating one")
				if err = ioutil.WriteFile(confFile, confFileContent(), 0644); err != nil {
					logrus.WithFields(logrus.Fields{"action": "conf_file.could_not_create", "confFile": confFile}).Error("Couldn't create config file ", err)
				}
			} else {
				logrus.WithFields(logrus.Fields{"action": "conf_file.stat", "confFile": confFile}).Error("Couldn't stat config file ", err)
			}
		}
	}

	// Loading the driver
	driver, err := sample.NewSampleDriver(dataDir, confFile)
	if err != nil {
		logrus.Fatalf("Could not load the driver %v", err)
	}

	// Overriding the driver default silent logger by a sub-logger (component: driver)
	driver.Entry = logrus.WithField("component", "driver")

	// Instantiating the server by passing our driver implementation
	ftpServer = server.NewFtpServer(driver)

	// Overriding the server default silent logger by a sub-logger (component: server)
	ftpServer.Entry = logrus.WithField("component", "server")

	// Blocking call, behaving similarly to the http.ListenAndServe
	if onlyConf {
		logrus.Info("Only creating conf")
		return
	}

	// Preparing the SIGTERM handling
	done := make(chan struct{})
	go signalHandler(done)

	if err := ftpServer.ListenAndServe(); err != nil {
		if !ftpServer.Stopped() {
			logrus.Fatalf("Problem listening %v", err)
			close(done)
		}
	}
}

func signalHandler(done chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	defer signal.Stop(ch)
	for {
		select {
		case sig := <-ch:
			if sig == syscall.SIGTERM {
				ftpServer.Stop()
				return
			}
		case <-done:
			return
		}
	}
}

func confFileContent() []byte {
	str := `# ftpserver configuration file
#
# These are all the config parameters with their default values. If not present,

# Max number of control connections to accept
# max_connections = 0
max_connections = 10

[server]
# Address to listen on
# listen_host = "0.0.0.0"

# Port to listen on
# listen_port = 2121

# Public host to expose in the passive connection
# public_host = ""

# Idle timeout time
# idle_timeout = 900

# Data port range from 10000 to 15000
# [dataPortRange]
# start = 2122
# end = 2200

[server.dataPortRange]
start = 2122
end = 2200

[[users]]
user="fclairamb"
pass="floflo"
dir="shared"

[[users]]
user="test"
pass="test"
dir="shared"

[[users]]
user="mcardon"
pass="marmar"
dir="marie"
`
	return []byte(str)
}
