package merge

import (
	"encoding/json"
	"os"
)

func CountJSON(path, severityFilter string, strict bool) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	items := parseJSONItems(data)
	count := 0
	for _, item := range items {
		if severityFilter != "" {
			sev := jsonSeverity(item)
			if !SeverityMatches(sev, severityFilter, strict) {
				continue
			}
		}
		count++
	}
	return count
}

func MergeJSON(files []Input, output, severityFilter string, strict bool) (int, error) {
	merged := []map[string]any{}
	for _, in := range files {
		if in.Path == "" {
			continue
		}
		data, err := os.ReadFile(in.Path)
		if err != nil {
			continue
		}
		for _, item := range parseJSONItems(data) {
			if severityFilter != "" {
				sev := jsonSeverity(item)
				if !SeverityMatches(sev, severityFilter, strict) {
					continue
				}
			}
			if in.Project != "" {
				item["project"] = in.Project
			}
			merged = append(merged, item)
		}
	}

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(output, out, 0644); err != nil {
		return 0, err
	}
	return len(merged), nil
}

func parseJSONItems(data []byte) []map[string]any {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		var out []map[string]any
		for _, elem := range v {
			if m, ok := elem.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		// CodeQL cannot emit plain JSON, so --output-format json is produced
		// from the SARIF that analyze actually wrote.
		if runs, ok := v["runs"].([]any); ok {
			return flattenSARIFRuns(runs)
		}
		if results, ok := v["results"].([]any); ok {
			var out []map[string]any
			for _, elem := range results {
				if m, ok := elem.(map[string]any); ok {
					out = append(out, m)
				}
			}
			return out
		}
	}
	return nil
}

// flattenSARIFRuns turns SARIF runs into one record per result. The original
// SARIF fields are preserved; severity, ruleId, messageText, path, and
// startLine are added so records are usable without walking the SARIF tree.
func flattenSARIFRuns(runs []any) []map[string]any {
	var out []map[string]any
	for _, r := range runs {
		run, ok := r.(map[string]any)
		if !ok {
			continue
		}
		ruleSeverity := buildRuleSeverityMap(run)
		results, _ := run["results"].([]any)
		for _, res := range results {
			m, ok := res.(map[string]any)
			if !ok {
				continue
			}
			item := shallowCopy(m)
			ruleID := sarifRuleID(m)
			if ruleID != "" {
				item["ruleId"] = ruleID
				if jsonSeverity(item) == "" {
					if sev := ruleSeverity[ruleID]; sev != "" {
						item["severity"] = sev
					}
				}
			}
			if text := sarifMessageText(m); text != "" {
				item["messageText"] = text
			}
			if path, line := sarifLocation(m); path != "" {
				item["path"] = path
				if line > 0 {
					item["startLine"] = line
				}
			}
			out = append(out, item)
		}
	}
	return out
}

func jsonSeverity(item map[string]any) string {
	if s, ok := item["severity"].(string); ok && s != "" {
		return s
	}
	if s, ok := item["level"].(string); ok && s != "" {
		return s
	}
	if props, ok := item["properties"].(map[string]any); ok {
		if s, ok := props["severity"].(string); ok {
			return s
		}
	}
	return ""
}
