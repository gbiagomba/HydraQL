package merge

import (
	"encoding/json"
	"testing"
)

// This SARIF mirrors what `codeql database analyze --format=sarif-latest`
// writes, including the cases that made a naive conversion diverge from
// CodeQL's own CSV writer:
//   - a multi-line @description, which CSV emits with whitespace collapsed
//     (fullDescription is pre-normalized; properties.description is not)
//   - an omitted startColumn and endLine, which sit at their SARIF defaults
//   - an exclusive endColumn, where CSV's is inclusive
//   - a [text](id) message link resolved through relatedLocations
const analyzeSARIF = `{
  "version": "2.1.0",
  "runs": [{
    "tool": {"driver": {"name": "CodeQL", "rules": [
      {
        "id": "js/inconsistent-return",
        "name": "js/inconsistent-return",
        "shortDescription": {"text": "Inconsistent return statements"},
        "fullDescription": {"text": "A function should either always return a value, or never."},
        "properties": {
          "name": "Inconsistent return statements",
          "description": "A function should either always return a value,\n              or never.",
          "problem.severity": "recommendation"
        }
      },
      {
        "id": "js/unused-variable",
        "name": "js/unused-variable",
        "shortDescription": {"text": "Unused variable"},
        "fullDescription": {"text": "Unused variables may be a symptom of a bug."},
        "properties": {"problem.severity": "warning"}
      }
    ]}},
    "results": [
      {
        "ruleId": "js/inconsistent-return",
        "message": {"text": "This returns 'undefined', whereas [another return statement](1) returns a value."},
        "locations": [{"physicalLocation": {
          "artifactLocation": {"uri": "src/a.ts"},
          "region": {"startLine": 202, "startColumn": 13, "endColumn": 20}
        }}],
        "relatedLocations": [{
          "id": 1,
          "physicalLocation": {
            "artifactLocation": {"uri": "src/a.ts"},
            "region": {"startLine": 204, "startColumn": 9, "endColumn": 52}
          },
          "message": {"text": "another return statement"}
        }]
      },
      {
        "ruleId": "js/unused-variable",
        "message": {"text": "Unused variable x."},
        "locations": [{"physicalLocation": {
          "artifactLocation": {"uri": "src/b.js"},
          "region": {"startLine": 27}
        }}]
      }
    ]
  }]
}`

func rowsFrom(t *testing.T, doc string) [][]string {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatal(err)
	}
	return CSVRowsFromSARIF(root)
}

func TestCSVRowsFromSARIF(t *testing.T) {
	rows := rowsFrom(t, analyzeSARIF)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	want := [][]string{
		{
			"Inconsistent return statements",
			// The normalized text, not the raw multi-line properties value.
			"A function should either always return a value, or never.",
			"recommendation",
			`This returns 'undefined', whereas [["another return statement"|"relative:///src/a.ts:204:9:204:51"]] returns a value.`,
			"/src/a.ts", // CSV paths are root-anchored
			"202", "13",
			"202", // endLine defaults to startLine
			"19",  // exclusive 20 becomes inclusive 19
		},
		{
			"Unused variable",
			"Unused variables may be a symptom of a bug.",
			"warning",
			"Unused variable x.",
			"/src/b.js",
			"27",
			"1",  // startColumn defaults to 1
			"27", // endLine defaults to startLine
			"1",  // endColumn absent, falls back to startColumn
		},
	}
	for i, w := range want {
		if len(rows[i]) != len(fixedHeader) {
			t.Errorf("row %d has %d columns, want %d", i, len(rows[i]), len(fixedHeader))
			continue
		}
		for j := range w {
			if rows[i][j] != w[j] {
				t.Errorf("row %d col %d (%s) = %q, want %q", i, j, fixedHeader[j], rows[i][j], w[j])
			}
		}
	}
}

// A link whose id has no matching relatedLocation is left as-is rather than
// rewritten into a location that was never reported.
func TestCSVMessageLeavesUnresolvedLinksAlone(t *testing.T) {
	doc := `{"runs":[{"tool":{"driver":{"rules":[{"id":"r","shortDescription":{"text":"R"},
      "properties":{"problem.severity":"error"}}]}},
      "results":[{"ruleId":"r","message":{"text":"see [elsewhere](7) for detail"},
      "locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.js"},
      "region":{"startLine":1}}}],
      "relatedLocations":[{"id":1,"physicalLocation":{"artifactLocation":{"uri":"a.js"},
      "region":{"startLine":2}}}]}]}]}`
	rows := rowsFrom(t, doc)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if got := rows[0][3]; got != "see [elsewhere](7) for detail" {
		t.Errorf("message = %q, want the unresolved link untouched", got)
	}
}

func TestMergeCSVFromSARIFTagsProjects(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.sarif", analyzeSARIF)
	b := writeFile(t, dir, "b.sarif", analyzeSARIF)
	out := dir + "/merged.csv"

	total, err := MergeCSVFromSARIF([]Input{
		{Path: a, Project: "proj-a"},
		{Path: b, Project: "proj-b"},
	}, out, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}

	rows := readCSV(t, out)
	if rows[0][0] != "Project" || len(rows) != 5 {
		t.Fatalf("unexpected output shape: %d lines, header %v", len(rows), rows[0])
	}
	seen := map[string]int{}
	for _, row := range rows[1:] {
		seen[row[0]]++
	}
	if seen["proj-a"] != 2 || seen["proj-b"] != 2 {
		t.Errorf("project tally = %v, want 2 each", seen)
	}
}

func TestMergeCSVFromSARIFSeverityFilter(t *testing.T) {
	dir := t.TempDir()
	in := writeFile(t, dir, "a.sarif", analyzeSARIF)
	out := dir + "/merged.csv"

	total, err := MergeCSVFromSARIF([]Input{{Path: in, Project: "p"}}, out, "warning", true)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want only the warning row", total)
	}
}
