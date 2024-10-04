package output

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

type ItemMap map[string]interface{}
type ItemMaps []ItemMap

func (ims ItemMaps) String() string {
	content, err := json.MarshalIndent(ims, "", "  ")
	if err != nil {
		fmt.Printf("error while writing items: %v", err)
		return ""
	}
	return string(content)
}

func (ims ItemMaps) Write(fpath string) error {
	f, err := os.Create(fpath)
	if err != nil {
		return fmt.Errorf("error opening file at %q: %v", fpath, err)
	}
	defer f.Close()

	if _, err = f.WriteString(ims.String()); err != nil {
		return fmt.Errorf("error writing file at %q: %v", fpath, err)
	}

	slog.Info(fmt.Sprintf("successfully wrote itemmaps to file %q", fpath))
	return nil
}

func (ims ItemMaps) TotalFields() int {
	numFields := 0
	for _, item := range ims {
		for _, v := range item {
			if v != nil && v != "" {
				numFields++
			}
		}
	}
	return numFields
}
