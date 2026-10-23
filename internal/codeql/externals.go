package codeql

import (
	"fmt"
	"os"
	"strings"
)

// ParseExternals validates --external values of the form pred=file.csv, the
// shape `codeql database analyze --external` expects. A query declaring
// `external string selectedSourceFile()` cannot evaluate without one, so a
// typo here is worth catching before any analysis starts.
func ParseExternals(raw []string) ([]string, error) {
	var out []string
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		pred, path, found := strings.Cut(entry, "=")
		pred = strings.TrimSpace(pred)
		path = strings.TrimSpace(path)
		if !found || pred == "" || path == "" {
			return nil, fmt.Errorf("--external %q must be in the form predicate=file.csv", entry)
		}

		resolved := ExpandHome(path)
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("--external %s: cannot read %s: %w", pred, path, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("--external %s: %s is a directory, expected a CSV file", pred, path)
		}
		out = append(out, pred+"="+resolved)
	}
	return out, nil
}
