package harness

import (
	"fmt"
)

// ApplyShaping applies egress shaping on an interface inside the hub namespace.
func ApplyShaping(nsPID int, iface string, rule ShapingRule) error {
	return applyEgressShaping(nsPID, iface, rule)
}

// ApplyBidirectionalShaping applies shaping on both egress and ingress via IFB.
func ApplyBidirectionalShaping(nsPID int, iface string, rule ShapingRule) error {
	if nsPID == 0 {
		return fmt.Errorf("hub pid not set")
	}
	if iface == "" {
		return fmt.Errorf("iface not set")
	}
	if err := applyEgressShaping(nsPID, iface, rule); err != nil {
		return err
	}

	ifb := "ifb-" + iface
	_ = runInNamespace(nsPID, "ip", "link", "add", ifb, "type", "ifb")
	if err := runInNamespace(nsPID, "ip", "link", "set", ifb, "up"); err != nil {
		return err
	}

	_ = runInNamespace(nsPID, "tc", "qdisc", "del", "dev", iface, "ingress")
	if err := runInNamespace(nsPID, "tc", "qdisc", "add", "dev", iface, "ingress"); err != nil {
		return err
	}
	if err := runInNamespace(nsPID, "tc", "filter", "add", "dev", iface, "parent", "ffff:", "protocol", "ip", "u32", "match", "u32", "0", "0", "action", "mirred", "egress", "redirect", "dev", ifb); err != nil {
		return err
	}
	if err := applyEgressShaping(nsPID, ifb, rule); err != nil {
		return err
	}
	return nil
}

func applyEgressShaping(nsPID int, iface string, rule ShapingRule) error {
	bw := rule.Bandwidth
	lat := rule.Latency
	loss := rule.Loss
	if bw == "" {
		bw = "100mbit"
	}
	if lat == "" {
		lat = "0ms"
	}
	if loss == "" {
		loss = "0%"
	}
	_ = runInNamespace(nsPID, "tc", "qdisc", "del", "dev", iface, "root")
	if err := runInNamespace(nsPID, "tc", "qdisc", "add", "dev", iface, "root", "handle", "1:", "tbf", "rate", bw, "burst", "32k", "latency", "50ms"); err != nil {
		return err
	}
	if err := runInNamespace(nsPID, "tc", "qdisc", "add", "dev", iface, "parent", "1:1", "handle", "10:", "netem", "delay", lat, "loss", loss); err != nil {
		return err
	}
	return nil
}
