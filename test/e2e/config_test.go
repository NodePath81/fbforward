//go:build e2e

package e2e

import "fmt"

// staticConfig renders the small common topology used by loopback forwarding
// scenarios. Feature-specific blocks remain in the test that exercises them.
type staticConfigOptions struct {
	hostname         string
	protocol         string
	listenerName     string
	listenerPort     int
	controlPort      int
	upstreamHost     string
	auditPath        string
	tcpIdle          string
	udpIdle          string
	flowContext      bool
	forwardingLimits bool
	metrics          bool
	policyPath       string
}

func staticConfig(options staticConfigOptions) string {
	metrics := ""
	if options.metrics {
		metrics = "  metrics:\n    enabled: true\n"
	}
	config := fmt.Sprintf(`hostname: %s

listeners:
  - name: %s
    bind: 127.0.0.1:%d
    protocol: %s
    route: local

routes:
  - name: local
    strategy: static
    upstreams: [local]

upstreams:
  - tag: local
    destination:
      host: %s

control:
  bind_addr: 127.0.0.1
  bind_port: %d
  auth_token: e2e-control-token
%s`, options.hostname, options.listenerName, options.listenerPort, options.protocol, options.upstreamHost, options.controlPort, metrics)

	if options.forwardingLimits || options.tcpIdle != "" || options.udpIdle != "" {
		config += "\nforwarding:\n"
		if options.forwardingLimits {
			config += "  limits:\n    max_tcp_connections: 10\n    max_udp_mappings: 10\n"
		}
		if options.tcpIdle != "" || options.udpIdle != "" {
			config += "  idle_timeout:\n"
			if options.tcpIdle == "" {
				options.tcpIdle = "5s"
			}
			if options.udpIdle == "" {
				options.udpIdle = "5s"
			}
			config += fmt.Sprintf("    tcp: %s\n    udp: %s\n", options.tcpIdle, options.udpIdle)
		}
	}
	if options.auditPath != "" {
		config += fmt.Sprintf(`
ip_log:
  enabled: true
  db_path: %s
  batch_size: 1
  flush_interval: 10ms
`, options.auditPath)
	}
	if options.flowContext {
		config += `
flow_context:
  enabled: true
  max_ttl: 24h
  identities:
    - id: caddy
      token: e2e-backend-token
      routes: [local]
      upstreams: [local]
      namespaces: [app]
`
	}
	if options.policyPath != "" {
		return config + fmt.Sprintf(`
firewall:
  enabled: true
  policy_file: %s
  fail_on_initial_load: true
`, options.policyPath)
	}
	return config + "\nfirewall:\n  enabled: false\n"
}
