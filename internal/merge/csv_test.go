package merge

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

// codeql database analyze --format=csv emits bare data rows with no header,
// which is why fixedHeader is hardcoded in this package.
const headerlessCSV = `"js/unused-local-variable","Unused variable","recommendation","Unused variable x.","/src/a.js","3","5","3","6"
"js/useless-assignment","Useless assignment","warning","Useless assignment to y.","/src/b.js","9","1","9","4"
`

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestCountCSVKeepsEveryRow(t *testing.T) {
	path := writeFile(t, t.TempDir(), "out.csv", headerlessCSV)
	if got := CountCSV(path, "", false); got != 2 {
		t.Errorf("CountCSV = %d, want 2 (a headerless file must not lose its first row)", got)
	}
}

func TestCountCSVSkipsAHeaderWhenPresent(t *testing.T) {
	withHeader := "Name,Description,Severity,Message,Path,Start line,Start column,End line,End column\n" + headerlessCSV
	path := writeFile(t, t.TempDir(), "out.csv", withHeader)
	if got := CountCSV(path, "", false); got != 2 {
		t.Errorf("CountCSV = %d, want 2", got)
	}
}

func TestMergeCSVAddsProjectColumn(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.csv", headerlessCSV)
	b := writeFile(t, dir, "b.csv", headerlessCSV)
	out := filepath.Join(dir, "merged.csv")

	total, err := MergeCSV([]Input{
		{Path: a, Project: "accessibility-checker"},
		{Path: b, Project: "pdf-export-core"},
	}, out, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("merged %d rows, want 4", total)
	}

	rows := readCSV(t, out)
	if len(rows) != 5 {
		t.Fatalf("output has %d lines, want 1 header + 4 rows", len(rows))
	}
	if rows[0][0] != "Project" || len(rows[0]) != len(fixedHeader)+1 {
		t.Errorf("header = %v, want Project prefixed to the CodeQL columns", rows[0])
	}

	seen := map[string]int{}
	for _, row := range rows[1:] {
		seen[row[0]]++
	}
	if seen["accessibility-checker"] != 2 || seen["pdf-export-core"] != 2 {
		t.Errorf("project attribution = %v, want 2 rows each", seen)
	}
}

func TestMergeCSVSeverityFilter(t *testing.T) {
	dir := t.TempDir()
	in := writeFile(t, dir, "a.csv", headerlessCSV)
	out := filepath.Join(dir, "merged.csv")

	total, err := MergeCSV([]Input{{Path: in, Project: "p"}}, out, "warning", true)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("merged %d rows, want only the warning row", total)
	}
}

func TestMergeCSVEmptyInputStillWritesHeader(t *testing.T) {
	out := filepath.Join(t.TempDir(), "merged.csv")
	total, err := MergeCSV(nil, out, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	rows := readCSV(t, out)
	if len(rows) != 1 || rows[0][0] != "Project" {
		t.Errorf("want a lone header row, got %v", rows)
	}
}
