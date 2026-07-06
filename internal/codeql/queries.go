package codeql

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gbiagomba/hydraql/v2/internal/report"
)

var importLangRe = regexp.MustCompile(`(?im)^\s*import\s+(java|javascript|python|cpp|swift|ruby|kotlin|typescript)\b`)

type QueryPair struct {
	Query string
	Lang  string
}

func inferQueryLanguage(queryPath string) string {
	f, err := os.Open(queryPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 8000)
	n, _ := f.Read(buf)
	m := importLangRe.FindSubmatch(buf[:n])
	if m != nil {
		return LangAlias(string(m[1]))
	}
	return ""
}

// GatherQueries discovers all .ql/.qls files under queryDirs that match langs.
func GatherQueries(queryDirs, langs []string, suiteOnly, verbose bool) []QueryPair {
	langSet := map[string]bool{}
	for _, l := range langs {
		langSet[LangAlias(l)] = true
	}

	seen := map[string]bool{}
	var collected []string

	for _, qdir := range queryDirs {
		info, err := os.Stat(qdir)
		if err != nil || !info.IsDir() {
			if verbose {
				fmt.Printf("%s⚠️  Query dir does not exist:%s %s\n", report.Yellow, report.Reset, qdir)
			}
			continue
		}

		var suites []string
		_ = filepath.WalkDir(qdir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".qls") {
				suites = append(suites, path)
			}
			return nil
		})

		if suiteOnly || len(suites) > 0 {
			if verbose && len(suites) > 0 {
				fmt.Printf("  • Preferring suites in %s (%d found)\n", qdir, len(suites))
			}
			for _, s := range suites {
				if !seen[s] {
					seen[s] = true
					collected = append(collected, s)
				}
			}
		} else {
			_ = filepath.WalkDir(qdir, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if strings.HasSuffix(path, ".ql") || strings.HasSuffix(path, ".qls") {
					if !seen[path] {
						seen[path] = true
						collected = append(collected, path)
					}
				}
				return nil
			})
		}
	}

	sort.Strings(collected)

	var pairs []QueryPair
	for _, q := range collected {
		detected := inferQueryLanguage(q)
		if detected != "" && langSet[detected] {
			pairs = append(pairs, QueryPair{Query: q, Lang: detected})
			continue
		}
		// path-hint fallback: check if any lang appears in the path segments
		parts := strings.Split(filepath.ToSlash(q), "/")
		hinted := ""
		for _, part := range parts {
			p := strings.ToLower(part)
			if langSet[p] {
				hinted = p
				break
			}
		}
		if hinted != "" {
			pairs = append(pairs, QueryPair{Query: q, Lang: hinted})
		} else if verbose {
			fmt.Printf("%s⚠️  Could not infer language for query:%s %s\n", report.Yellow, report.Reset, q)
		}
	}
	return pairs
}
