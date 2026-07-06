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

func MergeJSON(files []string, output, severityFilter string, strict bool) (int, error) {
	var merged []map[string]any
	for _, path := range files {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
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
