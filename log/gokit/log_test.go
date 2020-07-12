package gokit

import (
	"testing"

	"github.com/fclairamb/ftpserverlib/log"
)

func getLogger() log.Logger {
	return NewGKLoggerStdout().With(
		"ts", GKDefaultTimestampUTC,
		"caller", GKDefaultCaller,
	)
}

func TestLogSimple(t *testing.T) {
	logger := getLogger()
	logger.Info("Hello !")
}
