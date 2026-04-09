package firewall

import (
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/NodePath81/fbforward/internal/config"
	"github.com/NodePath81/fbforward/internal/geoip"
	"github.com/NodePath81/fbforward/internal/metrics"
	"github.com/NodePath81/fbforward/internal/util"
)

type Engine struct {
	rules        []compiledRule
	defaultAllow bool
	lookup       geoip.LookupProvider
	metrics      *metrics.Metrics
	logger       util.Logger
}

type Decision struct {
	Allowed   bool
	RuleType  string
	RuleValue string
}

type compiledRule struct {
	action  bool
	kind    string
	value   string
	cidr    *net.IPNet
	asn     int
	country string
}

func NewEngine(cfg config.FirewallConfig, lookup geoip.LookupProvider, metrics *metrics.Metrics, logger util.Logger) (*Engine, error) {
	engine := &Engine{
		rules:        make([]compiledRule, 0, len(cfg.Rules)),
		defaultAllow: cfg.Default != "deny",
		lookup:       lookup,
		metrics:      metrics,
		logger:       util.ComponentLogger(logger, util.CompFirewall),
	}
	for _, ruleCfg := range cfg.Rules {
		rule := compiledRule{
			action: ruleCfg.Action == "allow",
		}
		switch {
		case ruleCfg.CIDR != "":
			_, network, err := net.ParseCIDR(ruleCfg.CIDR)
			if err != nil {
				return nil, fmt.Errorf("invalid firewall cidr %q: %w", ruleCfg.CIDR, err)
			}
			rule.kind = "cidr"
			rule.value = ruleCfg.CIDR
			rule.cidr = network
		case ruleCfg.ASN != 0:
			rule.kind = "asn"
			rule.value = fmt.Sprintf("%d", ruleCfg.ASN)
			rule.asn = ruleCfg.ASN
		case ruleCfg.Country != "":
			rule.kind = "country"
			rule.value = strings.ToUpper(ruleCfg.Country)
			rule.country = strings.ToUpper(ruleCfg.Country)
		default:
			return nil, fmt.Errorf("invalid firewall rule %+v", ruleCfg)
		}
		engine.rules = append(engine.rules, rule)
	}

	engine.logAvailabilityWarnings()
	return engine, nil
}

func (e *Engine) Decide(ip net.IP) Decision {
	if e == nil || ip == nil {
		return Decision{Allowed: true}
	}
	var lookup geoip.LookupResult
	lookedUp := false
	for _, rule := range e.rules {
		match := false
		switch rule.kind {
		case "cidr":
			match = rule.cidr.Contains(ip)
		case "asn":
			if !lookedUp {
				lookup = e.lookupResult(ip)
				lookedUp = true
			}
			if !lookup.ASNDBAvailable {
				continue
			}
			match = lookup.ASN == rule.asn
		case "country":
			if !lookedUp {
				lookup = e.lookupResult(ip)
				lookedUp = true
			}
			if !lookup.CountryAvailable {
				continue
			}
			match = strings.EqualFold(lookup.Country, rule.country)
		}
		if !match {
			continue
		}
		if !rule.action {
			if e.metrics != nil {
				e.metrics.IncFirewallDenied(rule.kind, rule.value)
			}
			util.Event(e.logger, slog.LevelInfo, "firewall.denied",
				"client.ip", ip.String(),
				"firewall.rule_type", rule.kind,
				"firewall.rule_value", rule.value,
			)
		}
		return Decision{
			Allowed:   rule.action,
			RuleType:  rule.kind,
			RuleValue: rule.value,
		}
	}
	return Decision{Allowed: e.defaultAllow}
}

func (e *Engine) Check(ip net.IP) bool {
	return e.Decide(ip).Allowed
}

func (e *Engine) lookupResult(ip net.IP) geoip.LookupResult {
	if e.lookup == nil {
		return geoip.LookupResult{}
	}
	return e.lookup.Lookup(ip)
}

func (e *Engine) logAvailabilityWarnings() {
	if e == nil {
		return
	}
	availability := geoip.Availability{}
	if e.lookup != nil {
		availability = e.lookup.Availability()
	}
	for _, rule := range e.rules {
		switch rule.kind {
		case "asn":
			if !availability.ASNDBAvailable {
				util.Event(e.logger, slog.LevelWarn, "firewall.geo_rule_unavailable",
					"firewall.rule_type", rule.kind,
					"firewall.rule_value", rule.value,
				)
			}
		case "country":
			if !availability.CountryAvailable {
				util.Event(e.logger, slog.LevelWarn, "firewall.geo_rule_unavailable",
					"firewall.rule_type", rule.kind,
					"firewall.rule_value", rule.value,
				)
			}
		}
	}
}
