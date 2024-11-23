package output

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

type JSONWriter struct{}

func (s *JSONWriter) Write(recChan chan Record) {
	logger := slog.With(slog.String("writer", STDOUT_WRITER_TYPE))

	recs := Records{}
	for rec := range recChan {
		recs = append(recs, rec)
	}

	content, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		logger.Error(fmt.Sprintf("error while writing recordss: %v", err))
		return
	}
	fmt.Print(string(content))
}
