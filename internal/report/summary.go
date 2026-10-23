package report

import (
	"fmt"
	"sort"
	"strings"
)

// Summary is the end-of-run tally. PerProject is keyed by project name and is
// empty for the flat single-project layout.
type Summary struct {
	QueriesRun int
	Findings   int
	TimedOut   int
	Databases  int
	PerQuery   []int
	PerProject map[string]int
}

func PrintSummary(s Summary, fancy bool) {
	withResults := 0
	for _, c := range s.PerQuery {
		if c > 0 {
			withResults++
		}
	}
	withoutResults := len(s.PerQuery) - withResults

	fmt.Println("\n🔎 Summary:\n───────────────────────────────")
	fmt.Printf("Databases scanned:       %d\n", s.Databases)
	fmt.Printf("Total queries run:       %d\n", s.QueriesRun)
	fmt.Printf("Total findings:          %d\n", s.Findings)
	if s.TimedOut > 0 {
		hint := " (use --no-timeout or increase --query-timeout)"
		if fancy {
			fmt.Printf("%sTimed out queries:       %d%s%s\n", Yellow, s.TimedOut, hint, Reset)
		} else {
			fmt.Printf("Timed out queries:       %d%s\n", s.TimedOut, hint)
		}
	}

	if len(s.PerProject) > 0 {
		names := make([]string, 0, len(s.PerProject))
		for name := range s.PerProject {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Println("\nFindings by project:")
		for _, name := range names {
			fmt.Printf("  %-28s %d\n", name, s.PerProject[name])
		}
	}

	ASCIIChart(withResults, withoutResults, fancy)
}

func ASCIIChart(withResults, withoutResults int, fancy bool) {
	if fancy {
		fmt.Printf("\n%s📊 Result Summary Chart%s\n", Blue, Reset)
	} else {
		fmt.Println("\nResult Summary Chart")
	}
	fmt.Printf("%-30s Bar\n", "Category")
	fmt.Printf("%-30s %s\n", "Queries with results", strings.Repeat("#", withResults))
	fmt.Printf("%-30s %s\n", "Queries without results", strings.Repeat("#", withoutResults))
}
