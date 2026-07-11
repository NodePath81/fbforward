package flowcontext

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
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
	ErrUnknownMethod = errors.New("unknown flow context method")
)

var tagPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Identity is resolved from the bearer token on the server. None of its
// authorization fields are accepted from an HTTP request body.
type Identity struct {
	ID         string
	Token      string
	Routes     []string
	Upstreams  []string
	Namespaces []string
}

func (i Identity) allowsContext(value Context) bool {
	return contains(i.Routes, value.Route) && contains(i.Upstreams, value.Upstream)
}

func (i Identity) allowsNamespace(namespace string) bool {
	return contains(i.Namespaces, namespace)
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type ResolveFlowRequest struct {
	Protocol   string `json:"protocol"`
	BackendKey string `json:"backend_key"`
	LocalAddr  string `json:"local_addr"`
	RemoteAddr string `json:"remote_addr"`
	WaitMS     int    `json:"wait_ms,omitempty"`
}

type TagRequest struct {
	FlowID     string `json:"flow_id"`
	Namespace  string `json:"namespace"`
	Key        string `json:"key"`
	Value      string `json:"value,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

type ListFlowTagsRequest struct {
	FlowID string `json:"flow_id"`
}

func (s *Service) ResolveFlow(ctx context.Context, request ResolveFlowRequest, identity Identity) (Context, error) {
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
	if err := authorizeBackend(result, identity); err != nil {
		return Context{}, err
	}
	return result, nil
}

func (s *Service) SetFlowTag(ctx context.Context, request TagRequest, identity Identity) (audit.FlowTag, error) {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, identity)
	if err != nil {
		return audit.FlowTag{}, err
	}
	namespace, key, value, expiresAt, prefix, err := s.validateTag(request, identity, true)
	if err != nil {
		return audit.FlowTag{}, err
	}
	now := time.Now().UTC()
	tag := audit.FlowTag{FlowID: flowContext.FlowID.String(), Tag: formatTag(namespace, key, value), Source: "flow-context", ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now}
	event := s.tagEvent(flowContext, tag.Tag, "set", identity, expiresAt, namespace, key, value)
	if s.store == nil {
		return audit.FlowTag{}, ErrTagStore
	}
	if err := s.store.ApplyFlowTag(flowEntityFromContext(flowContext), event, &tag, prefix); err != nil {
		return audit.FlowTag{}, err
	}
	s.auditTag("set_flow_tag", flowContext, identity, tag.Tag)
	return tag, nil
}

func (s *Service) UnsetFlowTag(ctx context.Context, request TagRequest, identity Identity) error {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, identity)
	if err != nil {
		return err
	}
	namespace, key, _, _, prefix, err := s.validateTag(request, identity, false)
	if err != nil {
		return err
	}
	if s.store == nil {
		return ErrTagStore
	}
	tag := formatTag(namespace, key, "")
	event := s.tagEvent(flowContext, tag, "unset", identity, nil, namespace, key, "")
	return s.store.RemoveFlowTag(flowEntityFromContext(flowContext), event, prefix)
}

func (s *Service) SetClientTag(ctx context.Context, request TagRequest, identity Identity) (audit.ClientTag, error) {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, identity)
	if err != nil {
		return audit.ClientTag{}, err
	}
	namespace, key, value, expiresAt, prefix, err := s.validateTag(request, identity, true)
	if err != nil {
		return audit.ClientTag{}, err
	}
	now := time.Now().UTC()
	tag := audit.ClientTag{ClientIP: clientIP(flowContext), Tag: formatTag(namespace, key, value), Source: "flow-context", ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now}
	event := s.tagEvent(flowContext, tag.Tag, "set_client", identity, expiresAt, namespace, key, value)
	if s.store == nil {
		return audit.ClientTag{}, ErrTagStore
	}
	if err := s.store.ApplyClientTag(flowEntityFromContext(flowContext), event, &tag, prefix); err != nil {
		return audit.ClientTag{}, err
	}
	s.auditTag("set_client_tag", flowContext, identity, tag.Tag)
	return tag, nil
}

func (s *Service) UnsetClientTag(ctx context.Context, request TagRequest, identity Identity) error {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, identity)
	if err != nil {
		return err
	}
	namespace, key, _, _, prefix, err := s.validateTag(request, identity, false)
	if err != nil {
		return err
	}
	if s.store == nil {
		return ErrTagStore
	}
	tag := formatTag(namespace, key, "")
	event := s.tagEvent(flowContext, tag, "unset_client", identity, nil, namespace, key, "")
	return s.store.RemoveClientTag(flowEntityFromContext(flowContext), event, clientIP(flowContext), prefix)
}

func (s *Service) ListFlowTags(ctx context.Context, request ListFlowTagsRequest, identity Identity) ([]audit.FlowTag, error) {
	flowContext, err := s.authorizedFlow(ctx, request.FlowID, identity)
	if err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, ErrTagStore
	}
	return s.store.QueryFlowTags(flowContext.FlowID.String())
}

func (s *Service) authorizedFlow(_ context.Context, flowID string, identity Identity) (Context, error) {
	if strings.TrimSpace(flowID) == "" {
		return Context{}, ErrFlowNotFound
	}
	result, ok := s.registry.Lookup(flow.ID(strings.TrimSpace(flowID)))
	if !ok {
		return Context{}, ErrFlowNotFound
	}
	if err := authorizeBackend(result, identity); err != nil {
		return Context{}, err
	}
	return result, nil
}

func authorizeBackend(value Context, identity Identity) error {
	if strings.TrimSpace(identity.ID) == "" || !identity.allowsContext(value) {
		return ErrForbidden
	}
	return nil
}

func (s *Service) validateTag(request TagRequest, identity Identity, setting bool) (string, string, string, *time.Time, string, error) {
	namespace := strings.TrimSpace(request.Namespace)
	key := strings.TrimSpace(request.Key)
	value := strings.TrimSpace(request.Value)
	if !tagPartPattern.MatchString(namespace) || !tagPartPattern.MatchString(key) || len(namespace) == 0 || len(namespace) > 32 || len(key) == 0 {
		return "", "", "", nil, "", ErrInvalidTag
	}
	if !identity.allowsNamespace(namespace) || len(key) > s.options.MaxKeyLength {
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

func flowEntityFromContext(value Context) audit.FlowEntity {
	entity := audit.FlowEntity{
		FlowID: value.FlowID.String(), Protocol: value.Protocol, ClientPort: 0,
		ClientIP: clientIP(value), Listener: value.Listener, Route: value.Route, Upstream: value.Upstream,
		BackendKey: value.BackendKey, BackendProtocol: value.BackendTuple.Protocol,
		BackendLocal: value.BackendTuple.LocalAddr.String(), BackendRemote: value.BackendTuple.RemoteAddr.String(),
		CreatedAt: value.CreatedAt, State: value.State, Generation: value.Generation,
		LastActivity: value.LastActivity, BytesUp: value.BytesUp, BytesDown: value.BytesDown,
	}
	if address, err := netip.ParseAddrPort(value.ClientAddr); err == nil {
		entity.ClientPort = int(address.Port())
	}
	if !value.EndedAt.IsZero() {
		ended := value.EndedAt
		entity.EndedAt = &ended
	}
	if !value.ResolveUntil.IsZero() {
		until := value.ResolveUntil
		entity.ResolveUntil = &until
	}
	return entity
}

func (value Context) AuditEntity() audit.FlowEntity {
	return flowEntityFromContext(value)
}

func (s *Service) tagEvent(value Context, tag, operation string, identity Identity, expiresAt *time.Time, namespace, key, tagValue string) audit.FlowTagEvent {
	metadata, _ := json.Marshal(map[string]string{"namespace": namespace, "key": key, "value": tagValue})
	return audit.FlowTagEvent{FlowID: value.FlowID.String(), Tag: tag, Operation: operation, Source: "flow-context", Actor: identity.ID, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC(), Metadata: string(metadata)}
}

func (s *Service) auditTag(event string, value Context, identity Identity, tag string) {
	if s.logger != nil {
		util.Event(s.logger, slog.LevelInfo, event, "flow.id", value.FlowID, "backend.identity", identity.ID, "tag", tag)
	}
}
