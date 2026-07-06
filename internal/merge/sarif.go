package merge

import (
	"encoding/json"
	"os"
)


func CountSARIF(path, severityFilter string, strict bool) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return 0
	}
	runs, _ := root["runs"].([]any)
	count := 0
	for _, r := range runs {
		run, ok := r.(map[string]any)
		if !ok {
			continue
		}
		rulesMap := buildRuleSeverityMap(run)
		results, _ := run["results"].([]any)
		for _, res := range results {
			resMap, ok := res.(map[string]any)
			if !ok {
				continue
			}
			if severityFilter != "" && !resultMatchesSeverity(resMap, rulesMap, severityFilter, strict) {
				continue
			}
			count++
		}
	}
	return count
}

func MergeSARIF(files []string, output, severityFilter string, strict bool) (int, error) {
	merged := map[string]any{
		"$schema": "https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs":    []any{},
	}
	total := 0

	for _, path := range files {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var root map[string]any
		if err := json.Unmarshal(data, &root); err != nil {
			continue
		}
		runs, _ := root["runs"].([]any)
		for _, r := range runs {
			run, ok := r.(map[string]any)
			if !ok {
				continue
			}
			rulesMap := buildRuleSeverityMap(run)
			newRun := shallowCopy(run)
			var filtered []any
			results, _ := run["results"].([]any)
			for _, res := range results {
				resMap, ok := res.(map[string]any)
				if !ok {
					continue
				}
				if severityFilter != "" && !resultMatchesSeverity(resMap, rulesMap, severityFilter, strict) {
					continue
				}
				filtered = append(filtered, resMap)
			}
			newRun["results"] = filtered
			total += len(filtered)
			existing, _ := merged["runs"].([]any)
			merged["runs"] = append(existing, newRun)
		}
	}

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(output, out, 0644); err != nil {
		return 0, err
	}
	return total, nil
}

func buildRuleSeverityMap(run map[string]any) map[string]string {
	m := map[string]string{}
	tool, _ := run["tool"].(map[string]any)
	driver, _ := tool["driver"].(map[string]any)
	rules, _ := driver["rules"].([]any)
	for _, r := range rules {
		rule, ok := r.(map[string]any)
		if !ok {
			continue
		}
		rid, _ := rule["id"].(string)
		if rid == "" {
			rid, _ = rule["name"].(string)
		}
		if rid == "" {
			continue
		}
		sev := ""
		props, _ := rule["properties"].(map[string]any)
		if s, ok := props["severity"].(string); ok {
			sev = s
		} else if s, ok := props["problem.severity"].(string); ok {
			sev = s
		}
		if sev == "" {
			defCfg, _ := rule["defaultConfiguration"].(map[string]any)
			sev, _ = defCfg["level"].(string)
		}
		m[rid] = sev
	}
	return m
}

func resultMatchesSeverity(res map[string]any, rulesMap map[string]string, target string, strict bool) bool {
	props, _ := res["properties"].(map[string]any)
	candidates := []string{
		strVal(res["severity"]),
		strVal(res["level"]),
	}
	if props != nil {
		candidates = append(candidates, strVal(props["severity"]), strVal(props["problem.severity"]))
	}
	// look up rule severity
	ruleID := ""
	if rid, ok := res["ruleId"].(string); ok {
		ruleID = rid
	} else if ruleObj, ok := res["rule"].(map[string]any); ok {
		ruleID, _ = ruleObj["id"].(string)
	}
	if ruleID != "" {
		if sev, ok := rulesMap[ruleID]; ok {
			candidates = append(candidates, sev)
		}
	}
	for _, c := range candidates {
		if c != "" && SeverityMatches(c, target, strict) {
			return true
		}
	}
	return false
}

func shallowCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
