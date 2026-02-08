package output

import (
	"encoding/json"
	"fmt"
	"log/slog"
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

// MergeRecords merges records from an independent scraper into the primary records
// by matching on the given key field. Fields from secondary records are copied into
// the matching primary record. Unmatched records are logged as warnings.
func MergeRecords(primary, secondary Records, key string) Records {
	// Build lookup from key value → secondary record
	lookup := make(map[string]Record, len(secondary))
	for _, rec := range secondary {
		k, ok := rec[key]
		if !ok {
			continue
		}
		kStr := fmt.Sprintf("%v", k)
		if kStr != "" {
			lookup[kStr] = rec
		}
	}

	matched := 0
	for _, rec := range primary {
		k, ok := rec[key]
		if !ok {
			continue
		}
		kStr := fmt.Sprintf("%v", k)
		sec, found := lookup[kStr]
		if !found {
			slog.Debug("in MergeRecords(), no match for primary record", "key", key, "value", kStr)
			continue
		}
		// Copy fields from secondary into primary (skip the merge key itself)
		for field, val := range sec {
			if field == key {
				continue
			}
			rec[field] = val
		}
		matched++
	}
	slog.Info("MergeRecords()", "key", key, "primary", len(primary), "secondary", len(secondary), "matched", matched)
	return primary
}

func ReadRecords(str string) (Records, error) {
	rs := Records{}
	if err := json.Unmarshal([]byte(str), &rs); err != nil {
		return nil, fmt.Errorf("error while reading records: %v", err)
	}
	return rs, nil
}
