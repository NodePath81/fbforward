package policy

import (
	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/firewall"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/util"
)

func Compile(doc Document, lookup geoip.LookupProvider, metricSet *metrics.Metrics, logger util.Logger) (*Engine, error) {
	if err := Validate(&doc); err != nil {
		return nil, err
	}
	cfg := config.FirewallConfig{
		Enabled: true,
		Default: doc.Default,
		Rules:   make([]config.FirewallRule, 0, len(doc.Rules)),
	}
	for _, item := range doc.Rules {
		rule := config.FirewallRule{Action: item.Action}
		switch {
		case item.Match.SourceCIDR != "":
			rule.CIDR = item.Match.SourceCIDR
		case item.Match.SourceASN != nil:
			rule.ASN = *item.Match.SourceASN
		case item.Match.SourceCountry != "":
			rule.Country = item.Match.SourceCountry
		}
		cfg.Rules = append(cfg.Rules, rule)
	}
	evaluator, err := firewall.NewEngine(cfg, lookup, metricSet, logger)
	if err != nil {
		return nil, err
	}
	return &Engine{evaluator: evaluator}, nil
}

func LegacyDocument(cfg config.FirewallConfig) Document {
	doc := Document{Version: SchemaVersion, Default: cfg.Default, Rules: make([]Rule, 0, len(cfg.Rules))}
	if doc.Default == "" {
		doc.Default = "allow"
	}
	for i, item := range cfg.Rules {
		rule := Rule{ID: "legacy-rule-" + itoa(i+1), Action: item.Action}
		switch {
		case item.CIDR != "":
			rule.Match.SourceCIDR = item.CIDR
		case item.ASN != 0:
			asn := item.ASN
			rule.Match.SourceASN = &asn
		case item.Country != "":
			rule.Match.SourceCountry = item.Country
		}
		doc.Rules = append(doc.Rules, rule)
	}
	return doc
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
