package observability

import (
	"context"
	"log/slog"
	"os"
)

func InitLogging(ctx context.Context) error {
	// logLevel := slog.LevelDebug
	// if InText() {
	logLevel := slog.LevelInfo
	if os.Getenv("DEBUG") != "true" {
		logLevel = slog.LevelDebug
		slog.Info("Debug logging enabled.") // Optional: confirm it's working
	}
	// }

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Configured logging")
	return nil
}
