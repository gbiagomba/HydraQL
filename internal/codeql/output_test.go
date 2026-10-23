package codeql

import (
	"strings"
	"testing"
)

func planOf(t *testing.T, spec string) OutputPlan {
	t.Helper()
	p, err := NewOutputPlan(spec)
	if err != nil {
		t.Fatalf("NewOutputPlan(%q) returned %v", spec, err)
	}
	return p
}

func names(p OutputPlan) string {
	var out []string
	for _, t := range p.Targets() {
		out = append(out, t.Name)
	}
	return strings.Join(out, ",")
}

func modes(p OutputPlan) string {
	var out []string
	for _, t := range p.Targets() {
		switch t.Mode {
		case OutputMerged:
			out = append(out, "merged")
		case OutputDerived:
			out = append(out, "derived")
		default:
			out = append(out, "raw")
		}
	}
	return strings.Join(out, ",")
}

// "all" takes the one-pass route: CodeQL runs SARIF once, and csv and json are
// reshaped from it rather than costing their own analysis.
func TestOutputPlanAllIsOneSARIFPass(t *testing.T) {
	p := planOf(t, "all")
	if got := names(p); got != "sarif,json,csv" {
		t.Errorf("targets = %q, want sarif,json,csv", got)
	}
	if got := modes(p); got != "merged,derived,derived" {
		t.Errorf("modes = %q, want merged,derived,derived", got)
	}
	if got := strings.Join(p.CodeQLFormats(), ","); got != "sarif-latest" {
		t.Errorf("passes = %q, want a single sarif-latest pass", got)
	}
}

// Naming csv explicitly is a passthrough to CodeQL's own CSV writer, so it
// runs its own pass rather than being reconstructed from SARIF.
func TestOutputPlanExplicitCSVIsNative(t *testing.T) {
	p := planOf(t, "csv")
	if got := modes(p); got != "merged" {
		t.Errorf("modes = %q, want merged (native pass)", got)
	}
	if got := strings.Join(p.CodeQLFormats(), ","); got != "csv" {
		t.Errorf("passes = %q, want csv", got)
	}

	both := planOf(t, "csv,sarif")
	if got := strings.Join(both.CodeQLFormats(), ","); got != "csv,sarif-latest" {
		t.Errorf("csv,sarif passes = %q, want two passes", got)
	}
}

func TestOutputPlanSARIFSpellingsArePassthroughButMerge(t *testing.T) {
	for _, spec := range []string{"sarif-latest", "sarifv2.1.0"} {
		p := planOf(t, spec)
		got := p.Targets()[0]
		if got.Name != "sarif" {
			t.Errorf("%s: Name = %q, want sarif", spec, got.Name)
		}
		if got.CodeQLFormat != spec {
			t.Errorf("%s: CodeQLFormat = %q, want the requested spelling", spec, got.CodeQLFormat)
		}
		if !got.Merged() {
			t.Errorf("%s: should still merge, it is SARIF 2.1.0", spec)
		}
	}
	// Two spellings of the same report collapse to one target.
	if got := names(planOf(t, "sarif,sarif-latest,sarifv2.1.0")); got != "sarif" {
		t.Errorf("targets = %q, want a single sarif", got)
	}
}

func TestOutputPlanGraphFormatsAreRaw(t *testing.T) {
	for _, spec := range []string{"dot", "dgml", "graphtext"} {
		p := planOf(t, spec)
		got := p.Targets()[0]
		if got.Mode != OutputRaw {
			t.Errorf("%s: Mode = %v, want OutputRaw", spec, got.Mode)
		}
		if got.CodeQLFormat != spec {
			t.Errorf("%s: CodeQLFormat = %q, want passthrough", spec, got.CodeQLFormat)
		}
		if !p.NeedsGraphQueries() || p.NeedsAlertQueries() {
			t.Errorf("%s: should need graph queries only", spec)
		}
		if _, ok := p.Primary(); ok {
			t.Errorf("%s: raw output has no findings, so it must not be primary", spec)
		}
	}
	// Mixed: the sarif target is primary and both query kinds are needed.
	p := planOf(t, "sarif,dot")
	if !p.NeedsGraphQueries() || !p.NeedsAlertQueries() {
		t.Error("sarif,dot should need both query kinds")
	}
	primary, ok := p.Primary()
	if !ok || primary.Name != "sarif" {
		t.Errorf("Primary() = %+v, want sarif", primary)
	}
}

// CodeQL's --help advertises these, but the CLI rejects them at run time.
func TestOutputPlanRejectsRemovedFormats(t *testing.T) {
	for spec, want := range map[string]string{
		"sarifv1": "2.8.2",
		"sarifv2": "2.8.2",
		"lgtm":    "2.23.5",
	} {
		_, err := NewOutputPlan(spec)
		if err == nil {
			t.Errorf("NewOutputPlan(%q) = nil error, want a rejection", spec)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("NewOutputPlan(%q) error %q should name CLI version %s", spec, err, want)
		}
	}
}

func TestOutputPlanRejectsUnknownAndEmpty(t *testing.T) {
	for _, spec := range []string{"", "   ", ",", "xml", "csv,xml", "sarifv3"} {
		if _, err := NewOutputPlan(spec); err == nil {
			t.Errorf("NewOutputPlan(%q) = nil error, want a rejection", spec)
		}
	}
}

func TestOutputPlanDescribe(t *testing.T) {
	if got := planOf(t, "all").Describe(); got != "sarif, json (from sarif), csv (from sarif); 1 CodeQL analyze pass (sarif-latest)" {
		t.Errorf("Describe() = %q", got)
	}
	if got := planOf(t, "csv,sarif").Describe(); got != "csv, sarif; 2 CodeQL analyze passes (csv, sarif-latest)" {
		t.Errorf("Describe() = %q", got)
	}
	if got := planOf(t, "dot").Describe(); got != "dot (raw, per query); 1 CodeQL analyze pass (dot)" {
		t.Errorf("Describe() = %q", got)
	}
}

// CodeQL's graph writers treat --output as a directory and fill it with one
// file per graph, so those passes have no single filename to name.
func TestTempExtPerPass(t *testing.T) {
	for in, want := range map[string]string{
		"csv": "csv", "sarif-latest": "sarif", "sarifv2.1.0": "sarif",
		"dot": "", "dgml": "", "graphtext": "",
	} {
		if got := TempExt(in); got != want {
			t.Errorf("TempExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWritesDirectory(t *testing.T) {
	for _, f := range []string{"dot", "dgml", "graphtext"} {
		if !WritesDirectory(f) {
			t.Errorf("WritesDirectory(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"csv", "sarif-latest", "sarifv2.1.0"} {
		if WritesDirectory(f) {
			t.Errorf("WritesDirectory(%q) = true, want false", f)
		}
	}
}

func TestIsGraphFormat(t *testing.T) {
	for _, f := range []string{"dot", "dgml", "graphtext"} {
		if !IsGraphFormat(f) {
			t.Errorf("IsGraphFormat(%q) = false", f)
		}
	}
	for _, f := range []string{"csv", "sarif-latest", "sarifv2.1.0"} {
		if IsGraphFormat(f) {
			t.Errorf("IsGraphFormat(%q) = true", f)
		}
	}
}
