package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func decodeConfig(raw []byte, cfg *Config, strict bool) error {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.KnownFields(true)
	}
	if err := decoder.Decode(cfg); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("configuration must contain exactly one YAML document")
		}
		return err
	}
	return nil
}

func hasModernTopology(raw []byte) bool {
	var root map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return false
	}
	_, listeners := root["listeners"]
	_, routes := root["routes"]
	return listeners || routes
}

func (c *Config) normalizeTopology() error {
	if c.topologyNormalized {
		return nil
	}
	modern := c.topologyMode == "modern" || len(c.Listeners) > 0 || len(c.Routes) > 0
	if modern {
		c.topologyMode = "modern"
		if len(c.Forwarding.Listeners) > 0 {
			return errors.New("top-level listeners cannot be combined with forwarding.listeners")
		}
		if len(c.Listeners) == 0 || len(c.Routes) == 0 {
			return errors.New("listeners and routes must both be provided")
		}
		listeners := make([]ListenerConfig, 0, len(c.Listeners))
		for i, spec := range c.Listeners {
			listener, err := normalizeListenerSpec(spec, i)
			if err != nil {
				return err
			}
			c.Listeners[i] = ListenerSpec{
				Name: listener.Name, Bind: net.JoinHostPort(listener.BindAddr, strconv.Itoa(listener.BindPort)),
				Protocol: listener.Protocol, Route: listener.Route, Shaping: listener.Shaping,
			}
			listeners = append(listeners, listener)
		}
		c.Forwarding.Listeners = listeners
	} else {
		if c.topologyMode == "" {
			c.topologyMode = "legacy"
		}
		if len(c.Forwarding.Listeners) == 0 {
			return nil
		}
		c.Listeners = make([]ListenerSpec, 0, len(c.Forwarding.Listeners))
		for _, listener := range c.Forwarding.Listeners {
			route := strings.TrimSpace(listener.Route)
			if route == "" {
				route = fmt.Sprintf("%s:%d", listener.BindAddr, listener.BindPort)
			}
			name := strings.TrimSpace(listener.Name)
			if name == "" {
				name = fmt.Sprintf("%s-%s", strings.ToLower(listener.Protocol), strings.ReplaceAll(net.JoinHostPort(listener.BindAddr, strconv.Itoa(listener.BindPort)), ":", "-"))
			}
			listener.Name = name
			listener.Route = route
			c.Forwarding.Listeners[len(c.Listeners)] = listener
			c.Listeners = append(c.Listeners, ListenerSpec{
				Name: name, Bind: net.JoinHostPort(listener.BindAddr, strconv.Itoa(listener.BindPort)),
				Protocol: listener.Protocol, Route: route, Shaping: listener.Shaping,
			})
		}
		if len(c.Routes) == 0 {
			upstreams := make([]string, 0, len(c.Upstreams))
			for _, upstream := range c.Upstreams {
				upstreams = append(upstreams, strings.TrimSpace(upstream.Tag))
			}
			seen := make(map[string]struct{})
			for _, listener := range c.Listeners {
				if _, ok := seen[listener.Route]; ok {
					continue
				}
				seen[listener.Route] = struct{}{}
				strategy := "adaptive"
				if len(upstreams) == 1 {
					strategy = "static"
				}
				c.Routes = append(c.Routes, RouteConfig{Name: listener.Route, Strategy: strategy, Upstreams: append([]string(nil), upstreams...)})
			}
		}
		if c.configLoaded {
			c.Warnings = append(c.Warnings, "forwarding.listeners is deprecated; use top-level listeners and routes")
		}
	}
	c.topologyNormalized = true
	return nil
}

func normalizeListenerSpec(spec ListenerSpec, index int) (ListenerConfig, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return ListenerConfig{}, fmt.Errorf("listeners[%d].name must not be empty", index)
	}
	bind := strings.TrimSpace(spec.Bind)
	if bind == "" {
		return ListenerConfig{}, fmt.Errorf("listeners[%d].bind must not be empty", index)
	}
	host, portText, err := net.SplitHostPort(bind)
	if err != nil {
		return ListenerConfig{}, fmt.Errorf("listeners[%d].bind: %w", index, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return ListenerConfig{}, fmt.Errorf("listeners[%d].bind port must be numeric", index)
	}
	return ListenerConfig{
		Name: name, BindAddr: host, BindPort: port,
		Protocol: strings.ToLower(strings.TrimSpace(spec.Protocol)),
		Route:    strings.TrimSpace(spec.Route), Shaping: spec.Shaping,
	}, nil
}
