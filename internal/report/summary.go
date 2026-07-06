package report

import (
	"fmt"
	"strings"
)

func PrintSummary(ran, total, timedOut int, perQueryCounts []int, fancy bool) {
	withResults := 0
	for _, c := range perQueryCounts {
		if c > 0 {
			withResults++
		}
	}
	withoutResults := ran - withResults

	fmt.Println("\n🔎 Summary:\n───────────────────────────────")
	fmt.Printf("Total queries run:       %d\n", ran)
	fmt.Printf("Total findings:          %d\n", total)
	if timedOut > 0 {
		hint := " (use --no-timeout or increase --query-timeout)"
		if fancy {
			fmt.Printf("%sTimed out queries:       %d%s%s\n", Yellow, timedOut, hint, Reset)
		} else {
			fmt.Printf("Timed out queries:       %d%s\n", timedOut, hint)
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
