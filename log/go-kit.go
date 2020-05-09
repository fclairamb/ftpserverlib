package log

import (
	"fmt"
	gklog "github.com/go-kit/kit/log"
	gklevel "github.com/go-kit/kit/log/level"
)

func (logger *gKLogger) checkError(err error) {
	if err != nil {
		fmt.Println("Logging faced this error: ", err)
	}
}

func (logger *gKLogger) log(gklogger gklog.Logger, event string, keyvals ...interface{}) {
	newKV := make([]interface{}, len(keyvals)+2)
	newKV = append(newKV, "event")
	newKV = append(newKV, event)
	newKV = append(newKV, keyvals...)
	logger.checkError(gklogger.Log(newKV))
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
func (logger *gKLogger) With(keyvals ...interface{}) Logger {
	return NewGKLogger(gklog.With(logger.logger, keyvals...))
}

// NewGKLogger creates a logger based on go-kit logs
func NewGKLogger(logger gklog.Logger) Logger {
	return &gKLogger{
		logger: logger,
	}
}

// NewNopGKLogger instantiates go-kit logger
func NewNopGKLogger() Logger {
	return NewGKLogger(gklog.NewNopLogger())
}

type gKLogger struct {
	logger gklog.Logger
}

var (
	// GKDefaultCaller adds a "caller" property
	GKDefaultCaller = gklog.Caller(4)
	// GKDefaultTimestampUTC adds a "ts" property
	GKDefaultTimestampUTC = gklog.DefaultTimestampUTC
)
