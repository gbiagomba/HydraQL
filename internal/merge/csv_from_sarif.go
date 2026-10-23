package merge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CodeQL has no JSON or plain-CSV writer that HydraQL can reuse when only the
// SARIF pass has been run, so this file reconstructs the CSV that
// `codeql database analyze --format=csv` would have produced from the SARIF it
// did write. Every rule below was derived by diffing the two formats over a
// real corpus; see the CHANGELOG entry for v2.5.0.

// sarifLinkRe matches SARIF's embedded link syntax, [text](index), where index
// refers to a result's relatedLocations entry by its id.
var sarifLinkRe = regexp.MustCompile(`\[([^\]]*)\]\((\d+)\)`)

// relatedLocation is one resolved relatedLocations entry.
type relatedLocation struct {
	uri         string
	startLine   int
	startColumn int
	endLine     int
	endColumn   int
}

// sarifRegion reads a physicalLocation's uri and range, applying the defaults
// SARIF leaves implicit: startColumn is 1, endLine equals startLine, and
// endColumn is exclusive where CodeQL's CSV is inclusive.
func sarifRegion(phys map[string]any) (uri string, startLine, startColumn, endLine, endColumn int) {
	artifact, _ := phys["artifactLocation"].(map[string]any)
	uri = strVal(artifact["uri"])

	region, _ := phys["region"].(map[string]any)
	startLine = intVal(region["startLine"], 0)
	startColumn = intVal(region["startColumn"], 1)
	endLine = intVal(region["endLine"], startLine)
	if ec, ok := region["endColumn"]; ok {
		endColumn = intVal(ec, startColumn) - 1
	} else {
		endColumn = startColumn
	}
	return uri, startLine, startColumn, endLine, endColumn
}

func intVal(v any, fallback int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return fallback
}

// collectRelatedLocations indexes a result's relatedLocations by their id.
func collectRelatedLocations(res map[string]any) map[int]relatedLocation {
	raw, _ := res["relatedLocations"].([]any)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[int]relatedLocation, len(raw))
	for _, entry := range raw {
		loc, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		phys, _ := loc["physicalLocation"].(map[string]any)
		if phys == nil {
			continue
		}
		uri, sl, sc, el, ec := sarifRegion(phys)
		out[intVal(loc["id"], -1)] = relatedLocation{uri, sl, sc, el, ec}
	}
	return out
}

// csvMessage rewrites SARIF's [text](id) links into the
// [["text"|"relative:///path:startLine:startColumn:endLine:endColumn"]] form
// that CodeQL's CSV writer emits. A link whose id does not resolve is left
// alone rather than guessed at.
func csvMessage(res map[string]any) string {
	text := sarifMessageText(res)
	related := collectRelatedLocations(res)
	if len(related) == 0 || !strings.Contains(text, "](") {
		return text
	}
	return sarifLinkRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := sarifLinkRe.FindStringSubmatch(match)
		id, err := strconv.Atoi(groups[2])
		if err != nil {
			return match
		}
		loc, ok := related[id]
		if !ok {
			return match
		}
		return fmt.Sprintf(`[["%s"|"relative:///%s:%d:%d:%d:%d"]]`,
			groups[1], loc.uri, loc.startLine, loc.startColumn, loc.endLine, loc.endColumn)
	})
}

// CSVRowsFromSARIF converts one SARIF document into the rows CodeQL's CSV
// writer would have produced, in the same column order as fixedHeader.
func CSVRowsFromSARIF(root map[string]any) [][]string {
	runs, _ := root["runs"].([]any)
	var rows [][]string

	for _, r := range runs {
		run, ok := r.(map[string]any)
		if !ok {
			continue
		}
		rules := indexRules(run)
		results, _ := run["results"].([]any)
		for _, entry := range results {
			res, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			rule := lookupRule(rules, res)
			props, _ := rule["properties"].(map[string]any)

			locs, _ := res["locations"].([]any)
			if len(locs) == 0 {
				continue
			}
			loc, _ := locs[0].(map[string]any)
			phys, _ := loc["physicalLocation"].(map[string]any)
			if phys == nil {
				continue
			}
			uri, startLine, startColumn, endLine, endColumn := sarifRegion(phys)
			if !strings.HasPrefix(uri, "/") {
				uri = "/" + uri // CodeQL's CSV paths are root-anchored
			}

			rows = append(rows, []string{
				// The SARIF-canonical description fields are used in
				// preference to the properties bag: CodeQL normalizes
				// whitespace in these, while properties keeps the raw
				// multi-line @name / @description text.
				firstNonEmpty(nestedText(rule, "shortDescription"), strVal(props["name"])),
				firstNonEmpty(nestedText(rule, "fullDescription"), strVal(props["description"])),
				strVal(props["problem.severity"]),
				csvMessage(res),
				uri,
				strconv.Itoa(startLine),
				strconv.Itoa(startColumn),
				strconv.Itoa(endLine),
				strconv.Itoa(endColumn),
			})
		}
	}
	return rows
}

// indexRules maps a run's rules by both id and position, because a result may
// reference either.
func indexRules(run map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	tool, _ := run["tool"].(map[string]any)
	driver, _ := tool["driver"].(map[string]any)
	rules, _ := driver["rules"].([]any)
	for i, entry := range rules {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if id := strVal(rule["id"]); id != "" {
			out[id] = rule
		}
		out["#"+strconv.Itoa(i)] = rule
	}
	return out
}

func lookupRule(rules map[string]map[string]any, res map[string]any) map[string]any {
	if id := sarifRuleID(res); id != "" {
		if rule, ok := rules[id]; ok {
			return rule
		}
	}
	if idx, ok := res["ruleIndex"]; ok {
		if rule, ok := rules["#"+strconv.Itoa(intVal(idx, -1))]; ok {
			return rule
		}
	}
	return map[string]any{}
}

func nestedText(m map[string]any, key string) string {
	nested, _ := m[key].(map[string]any)
	if nested == nil {
		return ""
	}
	return strVal(nested["text"])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
