package ftpserver

import (
	"encoding/csv"
	"strings"
)

func advSplitN(s string, sep rune, n int) ([]string, error) {
	r := csv.NewReader(strings.NewReader(s))
	r.Comma = sep
	record, err := r.Read()
	if err != nil {
		return nil, err
	}
	if len(record) > n {
		return record[:n], nil
	}
	return record, nil
}
