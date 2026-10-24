package codeql

import (
	"os"
	"path/filepath"
	"strings"
)

// langExtensions maps a file extension to the CodeQL language that extracts it.
// Keys are lowercase and include the dot.
var langExtensions = map[string]string{
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript",
	".cjs": "javascript", ".ts": "javascript", ".tsx": "javascript",
	".mts": "javascript", ".cts": "javascript", ".vue": "javascript",

	".py": "python", ".pyw": "python", ".pyi": "python",

	".java": "java", ".kt": "java", ".kts": "java",

	".c": "cpp", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp",
	".h": "cpp", ".hpp": "cpp", ".hh": "cpp", ".hxx": "cpp",

	".cs": "csharp",
	".go": "go",
	".rb": "ruby", ".erb": "ruby", ".rake": "ruby",
	".swift": "swift",
	".rs":    "rust",
}

// scanEntryCap bounds how many directory entries a source scan will examine.
// Proving a language absent needs a full walk, which on a very large tree is
// worth bounding; extraction costs far more, but an unbounded walk on a
// pathological tree should not stall the run.
const scanEntryCap = 2_000_000

// NestedLink is a symlinked directory found inside a source root. CodeQL does
// not follow these, so the code behind one is invisible unless it is analyzed
// as its own source root.
type NestedLink struct {
	Rel    string // path relative to the source root
	Target string // resolved absolute path
}

// SourceScan is what a single walk of a source root found.
type SourceScan struct {
	Languages map[string]int // aliased language -> files seen
	Nested    []NestedLink
	Truncated bool // the entry cap was reached, so absence is not proven
}

// Has reports whether the scan saw any source for a language.
func (s SourceScan) Has(lang string) bool { return s.Languages[LangAlias(lang)] > 0 }

// ScanSourceRoot walks a source root once, recording which languages have
// source present and which symlinked directories are nested inside.
//
// Symlinks are not followed, both because CodeQL does not follow them either
// and because following them risks cycles.
func ScanSourceRoot(root string, exclude []string) SourceScan {
	scan := SourceScan{Languages: map[string]int{}}
	entries := 0

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entries++; entries > scanEntryCap {
			scan.Truncated = true
			return filepath.SkipAll
		}
		if path == root {
			return nil
		}

		name := d.Name()
		if strings.HasPrefix(name, ".") || excluded(name, exclude) || nonLangDirs[strings.ToLower(name)] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// A previously generated database is not source.
		if d.IsDir() && (IsCodeQLDatabase(path) || strings.HasPrefix(strings.ToLower(name), "cqldb")) {
			return filepath.SkipDir
		}

		if d.Type()&os.ModeSymlink != 0 {
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				if target, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
					rel, _ := filepath.Rel(root, path)
					scan.Nested = append(scan.Nested, NestedLink{Rel: rel, Target: target})
				}
			}
			return nil
		}

		if !d.IsDir() {
			if lang, ok := langExtensions[strings.ToLower(filepath.Ext(name))]; ok {
				scan.Languages[lang]++
			}
		}
		return nil
	})
	return scan
}

// PresentLangs splits requested languages by whether the scan found source for
// them. A truncated scan keeps every requested language, since absence was
// never established.
func (s SourceScan) PresentLangs(requested []string) (present, absent []string) {
	for _, lang := range requested {
		if s.Truncated || s.Has(lang) {
			present = append(present, lang)
		} else {
			absent = append(absent, lang)
		}
	}
	return present, absent
}
