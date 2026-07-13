package upstream

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/NodePath81/fbforward/internal/config"
)

// OverrideState describes whether an operator override is currently being
// used or whether an adaptive route has temporarily fallen back.
type OverrideState string

const (
	OverrideNone     OverrideState = "none"
	OverrideActive   OverrideState = "active"
	OverrideFallback OverrideState = "fallback"
)

type RouteStatus struct {
	Name            string        `json:"route"`
	Strategy        string        `json:"strategy"`
	Upstreams       []string      `json:"upstreams"`
	DefaultUpstream string        `json:"default_upstream,omitempty"`
	Effective       string        `json:"effective_upstream,omitempty"`
	Override        string        `json:"override_upstream,omitempty"`
	OverrideState   OverrideState `json:"override_state"`
}

type routeDefinition struct {
	name            string
	strategy        string
	upstreams       []string
	defaultUpstream string
}

// RouteSelector owns the route-local operator override state. It deliberately
// sits above UpstreamManager so health and DNS remain shared while selection
// policy remains scoped to a route.
type RouteSelector struct {
	manager   *UpstreamManager
	mu        sync.RWMutex
	routes    map[string]routeDefinition
	overrides map[string]string
}

func NewRouteSelector(manager *UpstreamManager, routes []config.RouteConfig) *RouteSelector {
	selector := &RouteSelector{manager: manager, routes: make(map[string]routeDefinition, len(routes)), overrides: make(map[string]string)}
	for _, route := range routes {
		upstreams := append([]string(nil), route.Upstreams...)
		defaultUpstream := route.DefaultUpstream
		if route.Strategy == "static" && defaultUpstream == "" && len(upstreams) == 1 {
			defaultUpstream = upstreams[0]
		}
		selector.routes[route.Name] = routeDefinition{
			name: route.Name, strategy: route.Strategy, upstreams: upstreams, defaultUpstream: defaultUpstream,
		}
	}
	return selector
}

func (s *RouteSelector) route(name string) (routeDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	route, ok := s.routes[strings.TrimSpace(name)]
	return route, ok
}

func (s *RouteSelector) HasRoutes() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.routes) > 0
}

func (s *RouteSelector) SetOverride(routeName, tag string) error {
	route, ok := s.route(routeName)
	if !ok {
		return fmt.Errorf("route %q not found", routeName)
	}
	tag = strings.TrimSpace(tag)
	if tag == "" || !containsTag(route.upstreams, tag) {
		return fmt.Errorf("upstream %q is not configured for route %q", tag, route.name)
	}
	if s.manager == nil || s.manager.Get(tag) == nil {
		return fmt.Errorf("upstream %q not found", tag)
	}
	s.mu.Lock()
	s.overrides[route.name] = tag
	s.mu.Unlock()
	return nil
}

func (s *RouteSelector) ClearOverride(routeName string) error {
	name := strings.TrimSpace(routeName)
	if _, ok := s.route(name); !ok {
		return fmt.Errorf("route %q not found", routeName)
	}
	s.mu.Lock()
	delete(s.overrides, name)
	s.mu.Unlock()
	return nil
}

func (s *RouteSelector) override(route string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.overrides[route]
}

func (s *RouteSelector) Pick(routeName string) (*Upstream, RouteStatus, error) {
	route, ok := s.route(routeName)
	if !ok {
		return nil, RouteStatus{}, fmt.Errorf("route %q not found", routeName)
	}
	override := s.override(route.name)
	status := RouteStatus{
		Name: route.name, Strategy: route.strategy, Upstreams: append([]string(nil), route.upstreams...),
		DefaultUpstream: route.defaultUpstream, Override: override, OverrideState: OverrideNone,
	}
	if route.strategy == "static" {
		tag := route.defaultUpstream
		if override != "" {
			tag = override
			status.OverrideState = OverrideActive
		}
		selected, err := s.manager.SelectStatic(tag)
		if err != nil {
			return nil, status, err
		}
		status.Effective = tag
		return selected, status, nil
	}
	if override != "" {
		if selected, err := s.manager.SelectOverride(override, true); err == nil {
			status.Effective = override
			status.OverrideState = OverrideActive
			return selected, status, nil
		}
		status.OverrideState = OverrideFallback
	}
	selected, err := s.manager.SelectAdaptiveFrom(route.upstreams)
	if err != nil {
		return nil, status, err
	}
	status.Effective = selected.Tag
	return selected, status, nil
}

func (s *RouteSelector) Status() []RouteStatus {
	s.mu.RLock()
	names := make([]string, 0, len(s.routes))
	for name := range s.routes {
		names = append(names, name)
	}
	s.mu.RUnlock()
	sort.Strings(names)
	result := make([]RouteStatus, 0, len(names))
	for _, name := range names {
		_, status, err := s.Pick(name)
		if err != nil {
			// Status remains useful when a configured upstream is unavailable.
			route, _ := s.route(name)
			status = RouteStatus{Name: route.name, Strategy: route.strategy, Upstreams: append([]string(nil), route.upstreams...), DefaultUpstream: route.defaultUpstream, Override: s.override(name), OverrideState: OverrideNone}
			if status.Override != "" && route.strategy == "adaptive" {
				status.OverrideState = OverrideFallback
			}
		}
		result = append(result, status)
	}
	return result
}

func containsTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if tag == wanted {
			return true
		}
	}
	return false
}
