package merge

import (
	"encoding/csv"
	"os"
)

var fixedHeader = []string{
	"Name", "Description", "Severity", "Message",
	"Path", "Start line", "Start column", "End line", "End column",
}

func CountCSV(path, severityFilter string, strict bool) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		return 0
	}

	count := 0
	for i, row := range rows {
		if i == 0 {
			continue // skip header
		}
		if severityFilter != "" && len(row) > 2 && !SeverityMatches(row[2], severityFilter, strict) {
			continue
		}
		count++
	}
	return count
}

func MergeCSV(files []string, output, severityFilter string, strict bool) (int, error) {
	var rows [][]string
	for _, path := range files {
		if path == "" {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		r := csv.NewReader(f)
		all, err := r.ReadAll()
		f.Close()
		if err != nil {
			continue
		}
		for i, row := range all {
			if i == 0 {
				continue
			}
			if severityFilter != "" && len(row) > 2 && !SeverityMatches(row[2], severityFilter, strict) {
				continue
			}
			rows = append(rows, row)
		}
	}

	out, err := os.Create(output)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	w := csv.NewWriter(out)
	if err := w.Write(fixedHeader); err != nil {
		return 0, err
	}
	if err := w.WriteAll(rows); err != nil {
		return 0, err
	}
	w.Flush()
	return len(rows), w.Error()
}
