// Package log provides a simple interface to handle logging
package log

// Logger interface
type Logger interface {

	// Debug logging: Every details
	Debug(event string, keyvals ...interface{})

	// Info logging: Core events
	Info(event string, keyvals ...interface{})

	// Warning logging: Anything out of the ordinary but non-life threatening
	Warn(event string, keyvals ...interface{})

	// Error logging: Major issue
	Error(event string, keyvals ...interface{})

	// Context extending interface
	With(keyvals ...interface{}) Logger
}

// Nothing creates a no-op logger
func Nothing() Logger {
	return &noLogger{}
}

type noLogger struct{}

func (nl *noLogger) Debug(string, ...interface{}) {
}

func (nl *noLogger) Info(string, ...interface{}) {
}

func (nl *noLogger) Warn(string, ...interface{}) {
}

func (nl *noLogger) Error(string, ...interface{}) {
}

func (nl *noLogger) With(...interface{}) Logger {
	return nl
}
