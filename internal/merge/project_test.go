package merge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const sarifOne = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"name": "CodeQL", "rules": [{"id": "js/x", "properties": {"severity": "warning"}}]}},
      "results": [{"ruleId": "js/x", "message": {"text": "hit"}}]
    }
  ]
}`

func TestMergeSARIFTagsRunsPerProject(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.sarif", sarifOne)
	b := writeFile(t, dir, "b.sarif", sarifOne)
	out := filepath.Join(dir, "merged.sarif")

	total, err := MergeSARIF([]Input{
		{Path: a, Project: "accessibility-checker"},
		{Path: b, Project: "pdf-export-core"},
	}, out, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Runs []struct {
			AutomationDetails struct {
				ID string `json:"id"`
			} `json:"automationDetails"`
			Properties struct {
				HydraQL struct {
					Project string `json:"project"`
				} `json:"hydraql"`
			} `json:"properties"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Runs) != 2 {
		t.Fatalf("got %d runs, want one per project", len(root.Runs))
	}
	ids := map[string]bool{}
	for _, r := range root.Runs {
		ids[r.AutomationDetails.ID] = true
		if r.Properties.HydraQL.Project != r.AutomationDetails.ID {
			t.Errorf("properties.hydraql.project = %q, want %q",
				r.Properties.HydraQL.Project, r.AutomationDetails.ID)
		}
	}
	if !ids["accessibility-checker"] || !ids["pdf-export-core"] {
		t.Errorf("automationDetails.id values = %v, want both project names", ids)
	}
}

func TestMergeJSONTagsRecordsPerProject(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.json", `[{"severity":"warning","message":"hit"}]`)
	b := writeFile(t, dir, "b.json", `{"results":[{"severity":"error","message":"boom"}]}`)
	out := filepath.Join(dir, "merged.json")

	total, err := MergeJSON([]Input{
		{Path: a, Project: "accessibility-checker"},
		{Path: b, Project: "pdf-export-core"},
	}, out, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range items {
		p, _ := item["project"].(string)
		seen[p] = true
	}
	if !seen["accessibility-checker"] || !seen["pdf-export-core"] {
		t.Errorf("project keys = %v, want both project names", seen)
	}
}

// sarifAnalyzeOutput is the shape `codeql database analyze --format=sarif-latest`
// writes, which is also what --output-format json is derived from.
const sarifAnalyzeOutput = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"name": "CodeQL", "rules": [
        {"id": "js/unused-local-variable", "properties": {"severity": "recommendation"}}
      ]}},
      "results": [
        {
          "ruleId": "js/unused-local-variable",
          "message": {"text": "Unused variable x."},
          "locations": [{"physicalLocation": {
            "artifactLocation": {"uri": "src/a.js"},
            "region": {"startLine": 42}
          }}]
        }
      ]
    }
  ]
}`

func TestMergeJSONFlattensSARIF(t *testing.T) {
	dir := t.TempDir()
	in := writeFile(t, dir, "a.sarif", sarifAnalyzeOutput)
	out := filepath.Join(dir, "merged.json")

	total, err := MergeJSON([]Input{{Path: in, Project: "pdf-export-core"}}, out, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	got := items[0]
	for key, want := range map[string]any{
		"project":     "pdf-export-core",
		"ruleId":      "js/unused-local-variable",
		"severity":    "recommendation",
		"messageText": "Unused variable x.",
		"path":        "src/a.js",
		"startLine":   float64(42),
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
}

func TestCountJSONOnSARIFInput(t *testing.T) {
	path := writeFile(t, t.TempDir(), "a.sarif", sarifAnalyzeOutput)
	if got := CountJSON(path, "", false); got != 1 {
		t.Errorf("CountJSON = %d, want 1", got)
	}
	// The severity is only reachable through the rule table, so this also
	// proves the rule lookup is wired up.
	if got := CountJSON(path, "recommendation", true); got != 1 {
		t.Errorf("CountJSON with severity filter = %d, want 1", got)
	}
	if got := CountJSON(path, "critical", true); got != 0 {
		t.Errorf("CountJSON with non-matching severity = %d, want 0", got)
	}
}
