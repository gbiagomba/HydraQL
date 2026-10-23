package merge

import (
	"encoding/csv"
	"encoding/json"
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
	return writeCSV(collectCSVRows(files, severityFilter, strict, readNativeCSV), output)
}

// MergeCSVFromSARIF writes the same report from the SARIF pass's output
// instead of a native CSV pass, reconstructing what CodeQL's CSV writer would
// have produced. See csv_from_sarif.go for the conversion rules.
func MergeCSVFromSARIF(files []Input, output, severityFilter string, strict bool) (int, error) {
	return writeCSV(collectCSVRows(files, severityFilter, strict, readSARIFAsCSV), output)
}

type csvRowReader func(path string) [][]string

func readNativeCSV(path string) [][]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	all, err := r.ReadAll()
	if err != nil {
		return nil
	}
	return all
}

func readSARIFAsCSV(path string) [][]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	return CSVRowsFromSARIF(root)
}

func collectCSVRows(files []Input, severityFilter string, strict bool, read csvRowReader) [][]string {
	var rows [][]string
	for _, in := range files {
		if in.Path == "" {
			continue
		}
		for _, row := range read(in.Path) {
			if isHeaderRow(row) {
				continue
			}
			if severityFilter != "" && len(row) > 2 && !SeverityMatches(row[2], severityFilter, strict) {
				continue
			}
			rows = append(rows, append([]string{in.Project}, row...))
		}
	}
	return rows
}

func writeCSV(rows [][]string, output string) (int, error) {
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
