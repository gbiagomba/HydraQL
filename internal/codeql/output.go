package codeql

import (
	"fmt"
	"sort"
	"strings"
)

// OutputMode says how a requested report is produced.
type OutputMode int

const (
	// OutputMerged is a merged, project-tagged report built from its own
	// `codeql database analyze` pass.
	OutputMerged OutputMode = iota
	// OutputDerived is a merged, project-tagged report reshaped from the SARIF
	// pass, costing no extra analysis.
	OutputDerived
	// OutputRaw is CodeQL's own output kept per query, for formats that cannot
	// be meaningfully merged.
	OutputRaw
)

// OutputTarget is one report HydraQL will write.
type OutputTarget struct {
	Name         string // report name, also the file extension
	CodeQLFormat string // the analyze pass that feeds it
	Mode         OutputMode
}

// Merged reports whether the target produces a single combined report.
func (t OutputTarget) Merged() bool { return t.Mode != OutputRaw }

const sarifPass = "sarif-latest"

// hydraFormats are the names HydraQL produces itself, by merging or reshaping.
// "sarif" and "json" both ride the SARIF pass; "csv" runs its own.
var hydraFormats = map[string]OutputTarget{
	"csv":   {Name: "csv", CodeQLFormat: "csv", Mode: OutputMerged},
	"json":  {Name: "json", CodeQLFormat: sarifPass, Mode: OutputDerived},
	"sarif": {Name: "sarif", CodeQLFormat: sarifPass, Mode: OutputMerged},
}

// sarifSpellings are CodeQL's own names for the SARIF version HydraQL merges.
// They are accepted as passthrough aliases of "sarif"; the requested spelling
// is forwarded to CodeQL, which records it in semmle.formatSpecifier.
var sarifSpellings = map[string]bool{
	"sarif-latest": true,
	"sarifv2.1.0":  true,
}

// graphFormats are CodeQL's graph writers. They describe `@kind graph` queries
// rather than alerts, so there is nothing to merge: whatever CodeQL writes is
// kept as-is. Each writes a directory of files rather than a single file.
var graphFormats = map[string]bool{
	"graphtext": true,
	"dot":       true,
	"dgml":      true,
}

// removedFormats are values CodeQL's --help still advertises but the CLI
// rejects at run time. Rejecting them up front costs a second instead of
// failing on every query.
var removedFormats = map[string]string{
	"sarifv1": "SARIF v1.0.0 support was removed in CodeQL CLI 2.8.2",
	"sarifv2": "SARIF v2.0.0 support was removed in CodeQL CLI 2.8.2",
	"lgtm":    "the LGTM results format was removed in CodeQL CLI 2.23.5",
}

// IsGraphFormat reports whether a CodeQL format consumes `@kind graph` queries.
func IsGraphFormat(codeqlFormat string) bool { return graphFormats[codeqlFormat] }

// CodeQLFormat maps a HydraQL output format to the value CodeQL's
// `database analyze --format` accepts. CodeQL has no "sarif" or "json"
// format: SARIF is requested as sarif-latest, and JSON is reshaped from it.
func CodeQLFormat(outputFormat string) string {
	if t, ok := hydraFormats[outputFormat]; ok {
		return t.CodeQLFormat
	}
	return outputFormat
}

// TempExt is the file extension for a pass's per-query output, or "" when the
// pass writes a directory instead of a file.
//
// CodeQL's graph writers treat --output as a directory and fill it with one
// file per graph, namespaced by rule id (for example js/print-ast.dot), so
// there is no single filename to name.
func TempExt(codeqlFormat string) string {
	switch {
	case codeqlFormat == "csv":
		return "csv"
	case IsGraphFormat(codeqlFormat):
		return ""
	default:
		return "sarif"
	}
}

// WritesDirectory reports whether a pass's --output is a directory.
func WritesDirectory(codeqlFormat string) bool { return TempExt(codeqlFormat) == "" }

// SupportedFormats lists everything --output-format accepts, for help text and
// error messages.
func SupportedFormats() []string {
	var out []string
	for name := range hydraFormats {
		out = append(out, name)
	}
	for name := range sarifSpellings {
		out = append(out, name)
	}
	for name := range graphFormats {
		out = append(out, name)
	}
	sort.Strings(out)
	return append([]string{"all"}, out...)
}

// OutputPlan resolves a --output-format specification into the reports to
// write and the CodeQL analyze passes needed to produce them.
type OutputPlan struct {
	targets       []OutputTarget
	codeqlFormats []string
}

// NewOutputPlan parses a comma-separated specification.
//
// "all" takes the one-pass route the SARIF writer makes possible: CodeQL runs
// once with --format=sarif-latest, and the CSV and JSON reports are reshaped
// from that SARIF rather than costing their own analysis.
//
// Naming a CodeQL format directly is a passthrough: that exact value reaches
// `codeql database analyze --format`. "csv" therefore yields CodeQL's own CSV
// writer rather than HydraQL's reconstruction of it.
func NewOutputPlan(spec string) (OutputPlan, error) {
	var targets []OutputTarget
	seen := map[string]bool{}

	add := func(t OutputTarget) {
		if !seen[t.Name] {
			seen[t.Name] = true
			targets = append(targets, t)
		}
	}

	for _, raw := range strings.Split(spec, ",") {
		tok := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case tok == "":
			continue

		case tok == "all":
			// One SARIF pass feeds all three reports.
			add(OutputTarget{Name: "sarif", CodeQLFormat: sarifPass, Mode: OutputMerged})
			add(OutputTarget{Name: "json", CodeQLFormat: sarifPass, Mode: OutputDerived})
			add(OutputTarget{Name: "csv", CodeQLFormat: sarifPass, Mode: OutputDerived})

		case removedFormats[tok] != "":
			return OutputPlan{}, fmt.Errorf(
				"output format %q is not usable: %s.\nSupported formats: %s",
				tok, removedFormats[tok], strings.Join(SupportedFormats(), ", "))

		case sarifSpellings[tok]:
			// A passthrough spelling of SARIF still merges, because HydraQL
			// understands the 2.1.0 schema it produces.
			add(OutputTarget{Name: "sarif", CodeQLFormat: tok, Mode: OutputMerged})

		case graphFormats[tok]:
			add(OutputTarget{Name: tok, CodeQLFormat: tok, Mode: OutputRaw})

		default:
			if t, ok := hydraFormats[tok]; ok {
				add(t)
				continue
			}
			return OutputPlan{}, fmt.Errorf(
				"unknown output format %q.\nSupported formats: %s",
				tok, strings.Join(SupportedFormats(), ", "))
		}
	}

	if len(targets) == 0 {
		return OutputPlan{}, fmt.Errorf("no output format requested")
	}

	var codeqlFormats []string
	cfSeen := map[string]bool{}
	for _, t := range targets {
		if !cfSeen[t.CodeQLFormat] {
			cfSeen[t.CodeQLFormat] = true
			codeqlFormats = append(codeqlFormats, t.CodeQLFormat)
		}
	}
	return OutputPlan{targets: targets, codeqlFormats: codeqlFormats}, nil
}

// Targets lists the reports to write, in the order requested.
func (p OutputPlan) Targets() []OutputTarget { return p.targets }

// CodeQLFormats lists the distinct analyze passes required.
func (p OutputPlan) CodeQLFormats() []string { return p.codeqlFormats }

// NeedsGraphQueries reports whether any target consumes `@kind graph` queries.
func (p OutputPlan) NeedsGraphQueries() bool {
	for _, t := range p.targets {
		if IsGraphFormat(t.CodeQLFormat) {
			return true
		}
	}
	return false
}

// NeedsAlertQueries reports whether any target consumes alert queries.
func (p OutputPlan) NeedsAlertQueries() bool {
	for _, t := range p.targets {
		if !IsGraphFormat(t.CodeQLFormat) {
			return true
		}
	}
	return false
}

// Primary is the merged target whose results drive the run summary. Raw graph
// output has no findings to count, so it is never primary.
func (p OutputPlan) Primary() (OutputTarget, bool) {
	for _, t := range p.targets {
		if t.Merged() {
			return t, true
		}
	}
	return OutputTarget{}, false
}

// Describe summarizes the plan for the run header.
func (p OutputPlan) Describe() string {
	var names []string
	for _, t := range p.targets {
		switch t.Mode {
		case OutputDerived:
			names = append(names, t.Name+" (from sarif)")
		case OutputRaw:
			names = append(names, t.Name+" (raw, per query)")
		default:
			names = append(names, t.Name)
		}
	}
	passes := "pass"
	if len(p.codeqlFormats) > 1 {
		passes = "passes"
	}
	return fmt.Sprintf("%s; %d CodeQL analyze %s (%s)",
		strings.Join(names, ", "), len(p.codeqlFormats), passes,
		strings.Join(p.codeqlFormats, ", "))
}
