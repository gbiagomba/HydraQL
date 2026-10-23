package merge

import (
	"encoding/csv"
	"os"
)

var fixedHeader = []string{
	"Name", "Description", "Severity", "Message",
	"Path", "Start line", "Start column", "End line", "End column",
}

// mergedHeader prefixes the CodeQL columns with the project a finding came from.
var mergedHeader = append([]string{"Project"}, fixedHeader...)

// isHeaderRow guards against double-counting a header. `codeql database
// analyze --format=csv` emits bare data rows with no header line, which is why
// fixedHeader is hardcoded above, but a hand-edited or re-exported file may
// carry one.
func isHeaderRow(row []string) bool {
	return len(row) > 1 && row[0] == "Name" && row[1] == "Description"
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
	for _, row := range rows {
		if isHeaderRow(row) {
			continue
		}
		if severityFilter != "" && len(row) > 2 && !SeverityMatches(row[2], severityFilter, strict) {
			continue
		}
		count++
	}
	return count
}

func MergeCSV(files []Input, output, severityFilter string, strict bool) (int, error) {
	var rows [][]string
	for _, in := range files {
		if in.Path == "" {
			continue
		}
		f, err := os.Open(in.Path)
		if err != nil {
			continue
		}
		r := csv.NewReader(f)
		r.FieldsPerRecord = -1
		all, err := r.ReadAll()
		f.Close()
		if err != nil {
			continue
		}
		for _, row := range all {
			if isHeaderRow(row) {
				continue
			}
			if severityFilter != "" && len(row) > 2 && !SeverityMatches(row[2], severityFilter, strict) {
				continue
			}
			rows = append(rows, append([]string{in.Project}, row...))
		}
	}

	out, err := os.Create(output)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	w := csv.NewWriter(out)
	if err := w.Write(mergedHeader); err != nil {
		return 0, err
	}
	if err := w.WriteAll(rows); err != nil {
		return 0, err
	}
	w.Flush()
	return len(rows), w.Error()
}
