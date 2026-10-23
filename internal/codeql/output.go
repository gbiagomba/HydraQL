package codeql

import (
	"fmt"
	"strings"
)

// OutputFormats are the report formats HydraQL can write.
var OutputFormats = []string{"csv", "json", "sarif"}

// ValidOutputFormat reports whether HydraQL knows how to produce the format.
func ValidOutputFormat(outputFormat string) bool {
	for _, f := range OutputFormats {
		if outputFormat == f {
			return true
		}
	}
	return false
}

// CodeQLFormat maps a HydraQL output format to the value CodeQL's
// `database analyze --format` actually accepts. CodeQL has no "sarif" or
// "json" format: SARIF is requested as sarif-latest, and JSON is derived from
// SARIF by the merge package.
func CodeQLFormat(outputFormat string) string {
	if outputFormat == "csv" {
		return "csv"
	}
	return "sarif-latest"
}

// TempExt is the file extension for a CodeQL analyze pass's per-query output.
func TempExt(codeqlFormat string) string {
	if codeqlFormat == "csv" {
		return "csv"
	}
	return "sarif"
}

// OutputPlan resolves a --output-format specification into the reports to
// write and the CodeQL analyze passes needed to produce them.
type OutputPlan struct {
	formats       []string
	codeqlFormats []string
}

// NewOutputPlan parses a comma-separated list of formats, where "all" expands
// to every supported format.
func NewOutputPlan(spec string) (OutputPlan, error) {
	var formats []string
	seen := map[string]bool{}
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			formats = append(formats, f)
		}
	}

	for _, tok := range strings.Split(spec, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		switch {
		case tok == "":
			continue
		case tok == "all":
			for _, f := range OutputFormats {
				add(f)
			}
		case ValidOutputFormat(tok):
			add(tok)
		default:
			return OutputPlan{}, fmt.Errorf("unknown output format %q; expected %s, or all",
				tok, strings.Join(OutputFormats, ", "))
		}
	}
	if len(formats) == 0 {
		return OutputPlan{}, fmt.Errorf("no output format requested")
	}

	var codeqlFormats []string
	cfSeen := map[string]bool{}
	for _, f := range formats {
		c := CodeQLFormat(f)
		if !cfSeen[c] {
			cfSeen[c] = true
			codeqlFormats = append(codeqlFormats, c)
		}
	}
	return OutputPlan{formats: formats, codeqlFormats: codeqlFormats}, nil
}

// Formats lists the reports to write, in the order requested.
func (p OutputPlan) Formats() []string { return p.formats }

// CodeQLFormats lists the distinct analyze passes required. Asking for csv and
// sarif together costs two passes, because `codeql database analyze --format`
// takes a single value; json rides along on the sarif pass for free.
func (p OutputPlan) CodeQLFormats() []string { return p.codeqlFormats }

// Primary is the format whose results drive the run summary. Every format
// reports the same findings, so this only decides which pass is counted.
func (p OutputPlan) Primary() string { return p.formats[0] }

// Describe summarizes the plan for the run header.
func (p OutputPlan) Describe() string {
	passes := "pass"
	if len(p.codeqlFormats) > 1 {
		passes = "passes"
	}
	return fmt.Sprintf("%s (%d CodeQL analyze %s)",
		strings.Join(p.formats, ", "), len(p.codeqlFormats), passes)
}
