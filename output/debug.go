package output

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

var WriteSeparateLogFiles = true

// var WriteSeparateLogFiles = false

type logState struct {
	prevLogger *slog.Logger
	logF       *os.File
}

func SetDefaultLogger(logPath string, level slog.Level) (*logState, error) {
	// fmt.Printf("logPath: %q\n", logPath)
	prevLogger := slog.Default()
	if err := os.MkdirAll(filepath.Dir(logPath), 0770); err != nil {
		return nil, fmt.Errorf("error creating parent directories for log output file %q: %v", logPath, err)
	}
	logF, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	// logF, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("error opening log output file %q: %v", logPath, err)
	}
	// defer logF.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(logF, &slog.HandlerOptions{Level: level})))
	return &logState{prevLogger: prevLogger, logF: logF}, nil
}

func RestoreDefaultLogger(logS *logState) {
	logS.logF.Close()
	slog.SetDefault(logS.prevLogger)
}
