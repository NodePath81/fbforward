package flowcontext

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/flow"
	"github.com/NodePath81/fbforward/internal/util"
)

var (
	ErrUnauthorized  = errors.New("flow context authentication failed")
	ErrForbidden     = errors.New("backend is not authorized for this flow")
	ErrInvalidTag    = errors.New("invalid flow tag")
	ErrInvalidParams = errors.New("invalid flow context params")
	ErrRateLimited   = errors.New("flow context rate limit exceeded")
	ErrTagStore      = errors.New("flow tag store unavailable")
)

var tagPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type BackendIdentity struct {
	BackendKey string `json:"backend_key"`
	Route      string `json:"route"`
	Upstream   string `json:"upstream"`
	Actor      string `json:"actor,omitempty"`
}

type ResolveFlowRequest struct {
	Protocol   string          `json:"protocol"`
	BackendKey string          `json:"backend_key"`
	LocalAddr  string          `json:"local_addr"`
	RemoteAddr string          `json:"remote_addr"`
	WaitMS     int             `json:"wait_ms,omitempty"`
	Identity   BackendIdentity `json:"identity"`
}

type TagRequest struct {
	FlowID     string          `json:"flow_id"`
	Identity   BackendIdentity `json:"identity"`
	Namespace  string          `json:"namespace"`
	Key        string          `json:"key"`
	Value      string          `json:"value,omitempty"`
	TTLSeconds int64           `json:"ttl_seconds,omitempty"`
}

type ListFlowTagsRequest struct {
	FlowID   string          `json:"flow_id"`
	Identity BackendIdentity `json:"identity"`
}

type TagPolicy struct {
	AllowedNamespaces []string
	MaxTTL            time.Duration
	MaxKeyLength      int
	MaxValueLength    int
}

func (p TagPolicy) normalized() TagPolicy {
	if p.MaxTTL <= 0 {
		p.MaxTTL = 24 * time.Hour
	}
	if p.MaxKeyLength <= 0 {
		p.MaxKeyLength = 64
	}
	if p.MaxValueLength <= 0 {
		p.MaxValueLength = 256
	}
	allowed := make(map[string]struct{}, len(p.AllowedNamespaces))
	for _, namespace := range p.AllowedNamespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace != "" {
			allowed[namespace] = struct{}{}
		}
	}
	p.AllowedNamespaces = p.AllowedNamespaces[:0]
	for namespace := range allowed {
		p.AllowedNamespaces = append(p.AllowedNamespaces, namespace)
	}
	return p
}

type tagProjection struct {
	mu      sync.RWMutex
	flows   map[string]map[string]audit.FlowTag
	clients map[string]map[string]audit.ClientTag
}

func newTagProjection() *tagProjection {
	return &tagProjection{flows: make(map[string]map[string]audit.FlowTag), clients: make(map[string]map[string]audit.ClientTag)}
}

func (p *tagProjection) setFlow(tag audit.FlowTag, prefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := p.flows[tag.FlowID]
	if items == nil {
		items = make(map[string]audit.FlowTag)
		p.flows[tag.FlowID] = items
	}
	for key := range items {
		if strings.HasPrefix(key, prefix) {
			delete(items, key)
		}
	}
	items[tag.Tag] = tag
}

func (p *tagProjection) unsetFlow(flowID, prefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key := range p.flows[flowID] {
		if strings.HasPrefix(key, prefix) {
			delete(p.flows[flowID], key)
		}
	}
}

func (p *tagProjection) replaceFlows(flowID string, tags []audit.FlowTag) {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := make(map[string]audit.FlowTag, len(tags))
	for _, tag := range tags {
		items[tag.Tag] = tag
	}
	p.flows[flowID] = items
}

func (p *tagProjection) listFlows(flowID string) []audit.FlowTag {
	now := time.Now().UTC()
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]audit.FlowTag, 0, len(p.flows[flowID]))
	for _, tag := range p.flows[flowID] {
		if tag.ExpiresAt != nil && !now.Before(*tag.ExpiresAt) {
			continue
		}
		result = append(result, tag)
	}
	return result
}

func (p *tagProjection) setClient(tag audit.ClientTag, prefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := p.clients[tag.ClientIP]
	if items == nil {
		items = make(map[string]audit.ClientTag)
		p.clients[tag.ClientIP] = items
	}
	for key := range items {
		if strings.HasPrefix(key, prefix) {
			delete(items, key)
		}
	}
	items[tag.Tag] = tag
}

func (p *tagProjection) unsetClient(clientIP, prefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key := range p.clients[clientIP] {
		if strings.HasPrefix(key, prefix) {
			delete(p.clients[clientIP], key)
		}
	}
}

func (p *tagProjection) replaceClients(clientIP string, tags []audit.ClientTag) {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := make(map[string]audit.ClientTag, len(tags))
	for _, tag := range tags {
		items[tag.Tag] = tag
	}
	p.clients[clientIP] = items
}

func (s *UnixService) ResolveFlow(ctx context.Context, request ResolveFlowRequest) (Context, error) {
	if s == nil || s.registry == nil {
		return Context{}, ErrTagStore
	}
	tupleRequest := ResolveRequest{Protocol: request.Protocol, BackendKey: request.BackendKey, LocalAddr: request.LocalAddr, RemoteAddr: request.RemoteAddr, WaitMS: request.WaitMS}
	tuple, wait, err := ParseTuple(tupleRequest)
	if err != nil {
		return Context{}, err
	}
	if wait > s.options.ResolveTimeout {
		return Context{}, ErrInvalidTuple
	}
	result, ok := s.registry.Resolve(ctx, tuple, wait)
	if !ok {
		if s.registry.IsClosed() {
			return Context{}, ErrClosed
		}
		return Context{}, ErrFlowNotFound
	}
	if err := authorizeBackend(result, request.Identity, tuple.BackendKey); err != nil {
		return Context{}, err
	}
	return result, nil
}

func (s *UnixService) SetFlowTag(ctx context.Context, request TagRequest) (audit.FlowTag, error) {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, request.Identity)
	if err != nil {
		return audit.FlowTag{}, err
	}
	namespace, key, value, expiresAt, prefix, err := s.validateTag(request, true)
	if err != nil {
		return audit.FlowTag{}, err
	}
	if err := s.ensureFlow(flowContext); err != nil {
		return audit.FlowTag{}, err
	}
	now := time.Now().UTC()
	tag := audit.FlowTag{FlowID: flowContext.FlowID.String(), Tag: formatTag(namespace, key, value), Source: "flow-context", ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now}
	event := s.tagEvent(flowContext, tag.Tag, "set", request.Identity, expiresAt, namespace, key, value)
	if s.store == nil {
		return audit.FlowTag{}, ErrTagStore
	}
	if err := s.store.ApplyFlowTag(event, &tag, prefix); err != nil {
		return audit.FlowTag{}, err
	}
	s.projection.setFlow(tag, prefix)
	s.auditTag("set_flow_tag", flowContext, request.Identity, tag.Tag)
	return tag, nil
}

func (s *UnixService) UnsetFlowTag(ctx context.Context, request TagRequest) error {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, request.Identity)
	if err != nil {
		return err
	}
	namespace, key, _, _, prefix, err := s.validateTag(request, false)
	if err != nil {
		return err
	}
	if s.store == nil {
		return ErrTagStore
	}
	tag := formatTag(namespace, key, "")
	event := s.tagEvent(flowContext, tag, "unset", request.Identity, nil, namespace, key, "")
	if err := s.store.RemoveFlowTag(event, prefix); err != nil {
		return err
	}
	s.projection.unsetFlow(flowContext.FlowID.String(), prefix)
	s.auditTag("unset_flow_tag", flowContext, request.Identity, prefix)
	return nil
}

func (s *UnixService) SetClientTag(ctx context.Context, request TagRequest) (audit.ClientTag, error) {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, request.Identity)
	if err != nil {
		return audit.ClientTag{}, err
	}
	namespace, key, value, expiresAt, prefix, err := s.validateTag(request, true)
	if err != nil {
		return audit.ClientTag{}, err
	}
	if err := s.ensureFlow(flowContext); err != nil {
		return audit.ClientTag{}, err
	}
	now := time.Now().UTC()
	tag := audit.ClientTag{ClientIP: clientIP(flowContext), Tag: formatTag(namespace, key, value), Source: "flow-context", ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now}
	event := s.tagEvent(flowContext, tag.Tag, "set_client", request.Identity, expiresAt, namespace, key, value)
	if s.store == nil {
		return audit.ClientTag{}, ErrTagStore
	}
	if err := s.store.ApplyClientTag(event, &tag, prefix); err != nil {
		return audit.ClientTag{}, err
	}
	s.projection.setClient(tag, prefix)
	s.auditTag("set_client_tag", flowContext, request.Identity, tag.Tag)
	return tag, nil
}

func (s *UnixService) UnsetClientTag(ctx context.Context, request TagRequest) error {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, request.Identity)
	if err != nil {
		return err
	}
	namespace, key, _, _, prefix, err := s.validateTag(request, false)
	if err != nil {
		return err
	}
	if s.store == nil {
		return ErrTagStore
	}
	tag := formatTag(namespace, key, "")
	event := s.tagEvent(flowContext, tag, "unset_client", request.Identity, nil, namespace, key, "")
	if err := s.store.RemoveClientTag(event, clientIP(flowContext), prefix); err != nil {
		return err
	}
	s.projection.unsetClient(clientIP(flowContext), prefix)
	s.auditTag("unset_client_tag", flowContext, request.Identity, prefix)
	return nil
}

func (s *UnixService) ListFlowTags(ctx context.Context, request ListFlowTagsRequest) ([]audit.FlowTag, error) {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, request.Identity)
	if err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, ErrTagStore
	}
	tags, err := s.store.QueryFlowTags(flowContext.FlowID.String())
	if err != nil {
		return nil, err
	}
	s.projection.replaceFlows(flowContext.FlowID.String(), tags)
	return s.projection.listFlows(flowContext.FlowID.String()), nil
}

func (s *UnixService) authorizedFlow(ctx context.Context, flowID string, identity BackendIdentity) (Context, error) {
	if strings.TrimSpace(flowID) == "" {
		return Context{}, ErrFlowNotFound
	}
	result, ok := s.registry.Lookup(flow.ID(strings.TrimSpace(flowID)))
	if !ok {
		return Context{}, ErrFlowNotFound
	}
	if err := authorizeBackend(result, identity, result.BackendKey); err != nil {
		return Context{}, err
	}
	return result, nil
}

func authorizeBackend(value Context, identity BackendIdentity, expectedBackendKey string) error {
	if strings.TrimSpace(identity.BackendKey) == "" || strings.TrimSpace(identity.Upstream) == "" {
		return ErrForbidden
	}
	if identity.BackendKey != expectedBackendKey || identity.Upstream != value.Upstream || identity.Route != value.Route {
		return ErrForbidden
	}
	return nil
}

func (s *UnixService) validateTag(request TagRequest, setting bool) (string, string, string, *time.Time, string, error) {
	namespace := strings.TrimSpace(request.Namespace)
	key := strings.TrimSpace(request.Key)
	value := strings.TrimSpace(request.Value)
	if !tagPartPattern.MatchString(namespace) || !tagPartPattern.MatchString(key) || len(namespace) == 0 || len(namespace) > 32 || len(key) == 0 {
		return "", "", "", nil, "", ErrInvalidTag
	}
	allowed := false
	for _, item := range s.options.AllowedNamespaces {
		if item == namespace {
			allowed = true
			break
		}
	}
	if !allowed || len(key) > s.options.MaxKeyLength {
		return "", "", "", nil, "", ErrInvalidTag
	}
	if !setting {
		return namespace, key, "", nil, namespace + ":" + key + "=", nil
	}
	if value == "" || len(value) > s.options.MaxValueLength || strings.ContainsAny(value, "\r\n") {
		return "", "", "", nil, "", ErrInvalidTag
	}
	if request.TTLSeconds < 0 || request.TTLSeconds > int64(s.options.MaxTTL/time.Second) {
		return "", "", "", nil, "", ErrInvalidTag
	}
	var expiresAt *time.Time
	if request.TTLSeconds > 0 {
		expires := time.Now().UTC().Add(time.Duration(request.TTLSeconds) * time.Second)
		expiresAt = &expires
	}
	return namespace, key, value, expiresAt, namespace + ":" + key + "=", nil
}

func formatTag(namespace, key, value string) string {
	return namespace + ":" + key + "=" + value
}

func clientIP(value Context) string {
	address, err := netip.ParseAddrPort(value.ClientAddr)
	if err == nil {
		return address.Addr().String()
	}
	return strings.TrimSpace(value.ClientAddr)
}

func (s *UnixService) ensureFlow(value Context) error {
	if s.store == nil {
		return ErrTagStore
	}
	exists, err := s.store.HasFlow(value.FlowID.String())
	if err != nil || exists {
		return err
	}
	address, err := netip.ParseAddrPort(value.ClientAddr)
	if err != nil {
		return err
	}
	now := value.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ended := value.EndedAt
	if ended.IsZero() {
		ended = now
	}
	return s.store.EnsureFlow(audit.FlowRecord{FlowID: value.FlowID.String(), Protocol: value.Protocol, ClientIP: address.Addr().String(), ClientPort: int(address.Port()), Listener: value.Listener, Route: value.Route, Upstream: value.Upstream, StartedAt: now, EndedAt: ended, LastActivity: value.LastActivity, CloseReason: "context_pending"})
}

func (s *UnixService) tagEvent(value Context, tag, operation string, identity BackendIdentity, expiresAt *time.Time, namespace, key, tagValue string) audit.FlowTagEvent {
	metadata, _ := json.Marshal(map[string]string{"namespace": namespace, "key": key, "value": tagValue})
	actor := strings.TrimSpace(identity.Actor)
	if actor == "" {
		actor = identity.BackendKey
	}
	return audit.FlowTagEvent{FlowID: value.FlowID.String(), Tag: tag, Operation: operation, Source: "flow-context", Actor: actor, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC(), Metadata: string(metadata)}
}

func (s *UnixService) auditTag(event string, value Context, identity BackendIdentity, tag string) {
	if s.logger != nil {
		util.Event(s.logger, slog.LevelInfo, event, "flow.id", value.FlowID, "backend.key", identity.BackendKey, "tag", tag)
	}
}
