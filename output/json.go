package output

import (
	"encoding/json"
	"fmt"

	"golang.org/x/exp/slog"
)

type JSONWriter struct{}

func (s *JSONWriter) Write(items chan ItemMap) {
	logger := slog.With(slog.String("writer", STDOUT_WRITER_TYPE))

	items2 := ItemMaps{}
	for item := range items {
		items2 = append(items2, item)
	}

	content, err := json.MarshalIndent(items2, "", "  ")
	if err != nil {
		logger.Error(fmt.Sprintf("error while writing items: %v", err))
		return
	}
	fmt.Print(string(content))
}
