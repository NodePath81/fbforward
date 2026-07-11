package policy

// SchemaVersion is the version of the external firewall policy document.
const SchemaVersion = 1

// MaxPolicyBytes bounds both policy files and ValidateFirewallPolicy content.
const MaxPolicyBytes = 1 << 20

// Document is the versioned, externally persisted firewall policy.
type Document struct {
	Version int    `yaml:"version" json:"version"`
	Default string `yaml:"default" json:"default"`
	Rules   []Rule `yaml:"rules" json:"rules"`
}

// Rule is evaluated in document order. The first matching rule wins.
type Rule struct {
	ID     string `yaml:"id" json:"id"`
	Action string `yaml:"action" json:"action"`
	Match  Match  `yaml:"match" json:"match"`
}

// Match retains the three matchers supported by the original firewall
// implementation while giving them an explicit source_ namespace in YAML.
type Match struct {
	SourceCIDR    string `yaml:"source_cidr,omitempty" json:"source_cidr,omitempty"`
	SourceASN     *int   `yaml:"source_asn,omitempty" json:"source_asn,omitempty"`
	SourceCountry string `yaml:"source_country,omitempty" json:"source_country,omitempty"`
}
