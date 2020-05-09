// Package log provides a way to use the go-kit/log package without having to deal
// with their very-opiniated/crazy choice of returning an error all the time: https://github.com/go-kit/kit/issues/164
package log

// Logger interface
type Logger interface {
	// log(keyvals ...interface{})
	Debug(event string, keyvals ...interface{})
	Info(event string, keyvals ...interface{})
	Warn(event string, keyvals ...interface{})
	Error(event string, keyvals ...interface{})
	With(keyvals ...interface{}) Logger
}
