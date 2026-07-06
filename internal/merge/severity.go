package merge

import "strings"

func MapLoose(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "error":
		return "CRITICAL"
	case "warning":
		return "HIGH"
	case "note":
		return "MEDIUM"
	}
	return strings.ToUpper(sev)
}

func SeverityMatches(candidate, target string, strict bool) bool {
	if candidate == "" {
		return false
	}
	cand := strings.ToUpper(strings.TrimSpace(candidate))
	targ := strings.ToUpper(strings.TrimSpace(target))
	if strict {
		return cand == targ
	}
	mapped := MapLoose(cand)
	return mapped == targ || strings.Contains(cand, targ)
}
