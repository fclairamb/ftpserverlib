// Package gokit provides go-kit Logger implementation
package gokit

import (
	"fmt"
	"os"

	gklog "github.com/go-kit/kit/log"
	gklevel "github.com/go-kit/kit/log/level"

	"github.com/fclairamb/ftpserverlib/log"
)

func (logger *gKLogger) checkError(err error) {
	if err != nil {
		fmt.Println("Logging faced this error: ", err)
	}
}

func (logger *gKLogger) log(gklogger gklog.Logger, event string, keyvals ...interface{}) {
	keyvals = append(keyvals, "event", event)
	logger.checkError(gklogger.Log(keyvals...))
}

// Debug logs key-values at debug level
func (logger *gKLogger) Debug(event string, keyvals ...interface{}) {
	logger.log(gklevel.Debug(logger.logger), event, keyvals...)
}

// Info logs key-values at info level
func (logger *gKLogger) Info(event string, keyvals ...interface{}) {
	logger.log(gklevel.Info(logger.logger), event, keyvals...)
}

// Warn logs key-values at warn level
func (logger *gKLogger) Warn(event string, keyvals ...interface{}) {
	logger.log(gklevel.Warn(logger.logger), event, keyvals...)
}

// Error logs key-values at error level
func (logger *gKLogger) Error(event string, keyvals ...interface{}) {
	logger.log(gklevel.Error(logger.logger), event, keyvals...)
}

// With adds key-values
func (logger *gKLogger) With(keyvals ...interface{}) log.Logger {
	return NewGKLogger(gklog.With(logger.logger, keyvals...))
}

// NewGKLogger creates a logger based on go-kit logs
func NewGKLogger(logger gklog.Logger) log.Logger {
	return &gKLogger{
		logger: logger,
	}
}

// NewGKLoggerStdout creates a logger based on go-kit logs but with some default parameters
func NewGKLoggerStdout() log.Logger {
	return NewGKLogger(gklog.NewLogfmtLogger(gklog.NewSyncWriter(os.Stdout)))
}

type gKLogger struct {
	logger gklog.Logger
}

var (
	// GKDefaultCaller adds a "caller" property
	GKDefaultCaller = gklog.Caller(5)
	// GKDefaultTimestampUTC adds a "ts" property
	GKDefaultTimestampUTC = gklog.DefaultTimestampUTC
)
