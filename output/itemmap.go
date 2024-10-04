package output

import (
	"encoding/json"
	"fmt"
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
