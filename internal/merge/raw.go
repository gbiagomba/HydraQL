package merge

import (
	"io/fs"
	"os"
	"path/filepath"
)

// CollectRaw gathers a pass's per-query outputs into their own directory.
//
// Some CodeQL writers produce output that cannot be merged: the graph formats
// (dot, dgml, graphtext) describe one query's graph, with no schema for
// combining several. Those outputs are kept exactly as CodeQL wrote them.
//
// An entry may be a file or a directory, because CodeQL's graph writers treat
// --output as a directory and fill it with one file per graph, namespaced by
// rule id. A directory is copied in whole, under its own name, so the rule
// namespacing survives and separate queries cannot collide.
func CollectRaw(inputs []Input, dir string) (int, error) {
	var present []Input
	for _, in := range inputs {
		if in.Path == "" {
			continue
		}
		if _, err := os.Stat(in.Path); err != nil {
			continue // the query failed and wrote nothing
		}
		present = append(present, in)
	}
	if len(present) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	collected := 0
	for _, in := range present {
		info, err := os.Stat(in.Path)
		if err != nil {
			return collected, err
		}
		if !info.IsDir() {
			if err := copyFile(in.Path, filepath.Join(dir, filepath.Base(in.Path))); err != nil {
				return collected, err
			}
			collected++
			continue
		}

		n, err := copyTree(in.Path, filepath.Join(dir, filepath.Base(in.Path)))
		collected += n
		if err != nil {
			return collected, err
		}
	}
	return collected, nil
}

// copyTree copies src into dest, preserving the relative layout, and returns
// the number of files written.
func copyTree(src, dest string) (int, error) {
	files := 0
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		files++
		return nil
	})
	return files, err
}

func copyFile(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}
