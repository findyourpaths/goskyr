package observability

import (
	"context"
	"log/slog"
	"os"
)

func InitLogging(ctx context.Context) error {
	// Default to Info level
	logLevel := slog.LevelInfo

	// Enable Debug level if DEBUG env var is set
	if os.Getenv("DEBUG") == "true" {
		logLevel = slog.LevelDebug
		slog.Info("Debug logging enabled.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Configured logging")
	return nil
}
