package harness

import (
	"fmt"
	"os/exec"
)

// ApplyShaping applies a symmetric shaping rule to an interface (executed in ns0).
func ApplyShaping(ns0PID int, iface string, rule ShapingRule) error {
	if ns0PID == 0 {
		return fmt.Errorf("ns0 pid not set")
	}
	cmds := [][]string{
		{"nsenter", "-t", fmt.Sprint(ns0PID), "-n", "tc", "qdisc", "del", "dev", iface, "root"},
		{"nsenter", "-t", fmt.Sprint(ns0PID), "-n", "tc", "qdisc", "add", "dev", iface, "root", "handle", "1:", "tbf", "rate", rule.Bandwidth, "burst", "32k", "latency", "50ms"},
		{"nsenter", "-t", fmt.Sprint(ns0PID), "-n", "tc", "qdisc", "add", "dev", iface, "parent", "1:1", "handle", "10:", "netem", "delay", rule.Latency, "loss", rule.Loss},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if err := cmd.Run(); err != nil {
			// ignore delete errors
			if args[4] == "del" {
				continue
			}
			return fmt.Errorf("tc command %v failed: %w", args, err)
		}
	}
	return nil
}
