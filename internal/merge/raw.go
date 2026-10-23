package merge

import (
	"os"
	"path/filepath"
)

// CollectRaw gathers a pass's per-query outputs into their own directory.
//
// Some CodeQL writers produce output that cannot be merged: the graph formats
// (dot, dgml, graphtext) describe one query's graph, with no schema for
// combining several. Those files are kept exactly as CodeQL wrote them.
func CollectRaw(inputs []Input, dir string) (int, error) {
	var present []Input
	for _, in := range inputs {
		if in.Path != "" {
			present = append(present, in)
		}
	}
	if len(present) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	collected := 0
	for _, in := range present {
		data, err := os.ReadFile(in.Path)
		if err != nil {
			return collected, err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(in.Path)), data, 0o644); err != nil {
			return collected, err
		}
		collected++
	}
	return collected, nil
}
