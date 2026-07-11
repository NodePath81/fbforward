package policy

import (
	"fmt"
	"net"
	"strings"
	"unicode"
)

// ValidationError identifies a policy document that is syntactically decoded
// but violates the policy schema or semantic constraints.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func Validate(doc *Document) error {
	if doc == nil {
		return &ValidationError{Message: "policy document is nil"}
	}
	if doc.Version != SchemaVersion {
		return &ValidationError{Message: fmt.Sprintf("policy.version must be %d", SchemaVersion)}
	}
	doc.Default = strings.ToLower(strings.TrimSpace(doc.Default))
	if doc.Default != "allow" && doc.Default != "deny" {
		return &ValidationError{Message: "policy.default must be allow or deny"}
	}
	seen := make(map[string]struct{}, len(doc.Rules))
	for i := range doc.Rules {
		rule := &doc.Rules[i]
		rule.ID = strings.TrimSpace(rule.ID)
		if rule.ID == "" {
			return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].id must not be empty", i)}
		}
		if len(rule.ID) > 128 {
			return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].id must be at most 128 characters", i)}
		}
		for _, r := range rule.ID {
			if unicode.IsControl(r) {
				return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].id contains a control character", i)}
			}
		}
		if _, ok := seen[rule.ID]; ok {
			return &ValidationError{Message: fmt.Sprintf("duplicate policy rule id: %s", rule.ID)}
		}
		seen[rule.ID] = struct{}{}

		rule.Action = strings.ToLower(strings.TrimSpace(rule.Action))
		if rule.Action != "allow" && rule.Action != "deny" {
			return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].action must be allow or deny", i)}
		}

		rule.Match.SourceCIDR = strings.TrimSpace(rule.Match.SourceCIDR)
		rule.Match.SourceCountry = strings.ToUpper(strings.TrimSpace(rule.Match.SourceCountry))
		matchers := 0
		if rule.Match.SourceCIDR != "" {
			_, network, err := net.ParseCIDR(rule.Match.SourceCIDR)
			if err != nil {
				return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].match.source_cidr is invalid: %v", i, err)}
			}
			rule.Match.SourceCIDR = network.String()
			matchers++
		}
		if rule.Match.SourceASN != nil {
			if *rule.Match.SourceASN <= 0 {
				return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].match.source_asn must be > 0", i)}
			}
			matchers++
		}
		if rule.Match.SourceCountry != "" {
			if len(rule.Match.SourceCountry) != 2 || rule.Match.SourceCountry[0] < 'A' || rule.Match.SourceCountry[0] > 'Z' || rule.Match.SourceCountry[1] < 'A' || rule.Match.SourceCountry[1] > 'Z' {
				return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].match.source_country must be a two-letter country code", i)}
			}
			matchers++
		}
		if matchers != 1 {
			return &ValidationError{Message: fmt.Sprintf("policy.rules[%d].match must specify exactly one matcher", i)}
		}
	}
	return nil
}
