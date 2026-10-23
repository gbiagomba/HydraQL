package codeql

import (
	"strings"
	"testing"
)

func joined(s []string) string { return strings.Join(s, ",") }

func TestNewOutputPlan(t *testing.T) {
	cases := []struct {
		spec      string
		formats   string
		passes    string
		primary   string
		passCount int
	}{
		{"csv", "csv", "csv", "csv", 1},
		{"sarif", "sarif", "sarif-latest", "sarif", 1},
		// JSON is reshaped from the SARIF pass, so it costs nothing extra.
		{"json", "json", "sarif-latest", "json", 1},
		{"json,sarif", "json,sarif", "sarif-latest", "json", 1},
		{"sarif,json", "sarif,json", "sarif-latest", "sarif", 1},
		// CSV cannot be derived from SARIF, so asking for both costs two passes.
		{"csv,sarif", "csv,sarif", "csv,sarif-latest", "csv", 2},
		{"all", "csv,json,sarif", "csv,sarif-latest", "csv", 2},
		{"ALL", "csv,json,sarif", "csv,sarif-latest", "csv", 2},
		// Duplicates and stray whitespace collapse.
		{" csv , csv ,json", "csv,json", "csv,sarif-latest", "csv", 2},
		{"csv,all", "csv,json,sarif", "csv,sarif-latest", "csv", 2},
	}
	for _, tc := range cases {
		plan, err := NewOutputPlan(tc.spec)
		if err != nil {
			t.Errorf("NewOutputPlan(%q) returned %v", tc.spec, err)
			continue
		}
		if got := joined(plan.Formats()); got != tc.formats {
			t.Errorf("NewOutputPlan(%q).Formats() = %q, want %q", tc.spec, got, tc.formats)
		}
		if got := joined(plan.CodeQLFormats()); got != tc.passes {
			t.Errorf("NewOutputPlan(%q).CodeQLFormats() = %q, want %q", tc.spec, got, tc.passes)
		}
		if got := plan.Primary(); got != tc.primary {
			t.Errorf("NewOutputPlan(%q).Primary() = %q, want %q", tc.spec, got, tc.primary)
		}
		if got := len(plan.CodeQLFormats()); got != tc.passCount {
			t.Errorf("NewOutputPlan(%q) needs %d passes, want %d", tc.spec, got, tc.passCount)
		}
	}
}

func TestNewOutputPlanRejectsBadInput(t *testing.T) {
	for _, spec := range []string{"", "   ", ",", "xml", "csv,xml", "sarif-latest"} {
		if _, err := NewOutputPlan(spec); err == nil {
			t.Errorf("NewOutputPlan(%q) = nil error, want a rejection", spec)
		}
	}
}

func TestOutputPlanDescribe(t *testing.T) {
	one, _ := NewOutputPlan("json,sarif")
	if got := one.Describe(); got != "json, sarif (1 CodeQL analyze pass)" {
		t.Errorf("Describe() = %q", got)
	}
	two, _ := NewOutputPlan("all")
	if got := two.Describe(); got != "csv, json, sarif (2 CodeQL analyze passes)" {
		t.Errorf("Describe() = %q", got)
	}
}

func TestTempExt(t *testing.T) {
	if got := TempExt("csv"); got != "csv" {
		t.Errorf("TempExt(csv) = %q", got)
	}
	// Every non-CSV pass writes SARIF, whichever SARIF flavour was asked for.
	if got := TempExt("sarif-latest"); got != "sarif" {
		t.Errorf("TempExt(sarif-latest) = %q", got)
	}
}
