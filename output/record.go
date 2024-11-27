package output

import (
	"encoding/json"
	"fmt"
)

type Record map[string]interface{}
type Records []Record

func (recs Records) String() string {
	content, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		fmt.Printf("error while writing records: %v", err)
		return ""
	}
	return string(content)
}

func (recs Records) TotalFields() int {
	numFields := 0
	for _, rec := range recs {
		for _, v := range rec {
			if v != nil && v != "" {
				numFields++
			}
		}
	}
	return numFields
}

func ReadRecords(str string) (Records, error) {
	rs := Records{}
	if err := json.Unmarshal([]byte(str), &rs); err != nil {
		return nil, fmt.Errorf("error while reading records: %v", err)
	}
	return rs, nil
}
