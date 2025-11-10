package logger

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

type Level string

const (
	INFO  Level = "INFO"
	WARN  Level = "WARN"
	ERROR Level = "ERROR"
	DEBUG Level = "DEBUG"
)

type Logger struct {
	*log.Logger
}

type LogEntry struct {
	Timestamp string      `json:"timestamp"`
	Level     Level       `json:"level"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
}

var defaultLogger *Logger

func init() {
	defaultLogger = NewLogger()
}

func NewLogger() *Logger {
	return &Logger{
		Logger: log.New(os.Stdout, "", 0),
	}
}

func (l *Logger) log(level Level, msg string, data interface{}) {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Message:   msg,
		Data:      data,
	}

	if jsonBytes, err := json.Marshal(entry); err == nil {
		l.Println(string(jsonBytes))
	}
}

func (l *Logger) Info(msg string, data ...interface{}) {
	var logData interface{}
	if len(data) > 0 {
		logData = data[0]
	}
	l.log(INFO, msg, logData)
}

func (l *Logger) Warn(msg string, data ...interface{}) {
	var logData interface{}
	if len(data) > 0 {
		logData = data[0]
	}
	l.log(WARN, msg, logData)
}

func (l *Logger) Error(msg string, data ...interface{}) {
	var logData interface{}
	if len(data) > 0 {
		logData = data[0]
	}
	l.log(ERROR, msg, logData)
}

func (l *Logger) Debug(msg string, data ...interface{}) {
	if os.Getenv("DEBUG") == "true" {
		var logData interface{}
		if len(data) > 0 {
			logData = data[0]
		}
		l.log(DEBUG, msg, logData)
	}
}

// Global logger functions
func Info(msg string, data ...interface{}) {
	defaultLogger.Info(msg, data...)
}

func Warn(msg string, data ...interface{}) {
	defaultLogger.Warn(msg, data...)
}

func Error(msg string, data ...interface{}) {
	defaultLogger.Error(msg, data...)
}

func Debug(msg string, data ...interface{}) {
	defaultLogger.Debug(msg, data...)
}
