// Package log provides a simple interface to handle logging
package log

// Logger interface
type Logger interface {
	// log(keyvals ...interface{})
	Debug(event string, keyvals ...interface{})
	Info(event string, keyvals ...interface{})
	Warn(event string, keyvals ...interface{})
	Error(event string, err error, keyvals ...interface{})
	With(keyvals ...interface{}) Logger
}
