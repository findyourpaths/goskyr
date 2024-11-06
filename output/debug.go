package output

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// var WriteSeparateLogFiles = true

var WriteSeparateLogFiles = false

type logState struct {
	prevLogger *slog.Logger
	logF       *os.File
}

func SetDefaultLogger(logPath string) (*logState, error) {
	prevLogger := slog.Default()
	if err := os.MkdirAll(filepath.Dir(logPath), 0770); err != nil {
		return nil, fmt.Errorf("error creating parent directories for log output file %q: %v", logPath, err)
	}
	logF, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("error opening log output file %q: %v", logPath, err)
	}
	// defer logF.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(logF, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &logState{prevLogger: prevLogger, logF: logF}, nil
}

func RestoreDefaultLogger(logS *logState) {
	logS.logF.Close()
	slog.SetDefault(logS.prevLogger)
}
