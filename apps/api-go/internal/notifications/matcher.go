package notifications

import "strings"

var severityRank = map[string]int{"LOW": 1, "MEDIUM": 2, "HIGH": 3, "CRITICAL": 4}

func SeverityRank(severity string) int {
	return severityRank[strings.ToUpper(strings.TrimSpace(severity))]
}

func SeverityMeets(actual, minimum string) bool {
	actualRank := SeverityRank(actual)
	minimumRank := SeverityRank(minimum)
	if actualRank == 0 || minimumRank == 0 {
		return false
	}
	return actualRank >= minimumRank
}

type RuleSpec struct {
	ID              string
	IsEnabled       bool
	MinimumSeverity string
	AlertTypes      []string
	CooldownSeconds int
}

func RuleMatches(rule RuleSpec, severity, alertType string) bool {
	if !rule.IsEnabled {
		return false
	}
	if !SeverityMeets(severity, rule.MinimumSeverity) {
		return false
	}
	if len(rule.AlertTypes) == 0 {
		return true
	}
	for _, candidate := range rule.AlertTypes {
		if candidate == alertType {
			return true
		}
	}
	return false
}

func MatchingRules(rules []RuleSpec, severity, alertType string) []RuleSpec {
	matched := []RuleSpec{}
	for _, rule := range rules {
		if RuleMatches(rule, severity, alertType) {
			matched = append(matched, rule)
		}
	}
	return matched
}

func ValidSeverity(value string) bool {
	return SeverityRank(value) > 0
}
