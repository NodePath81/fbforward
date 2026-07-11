package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/NodePath81/fbforward/internal/policy"
	"github.com/NodePath81/fbforward/internal/util"
)

type validateFirewallPolicyParams struct {
	Content *string `json:"content"`
}

type firewallPolicyResponse struct {
	Version    int           `json:"version"`
	Default    string        `json:"default"`
	Rules      []policy.Rule `json:"rules"`
	Source     string        `json:"source"`
	Hash       string        `json:"hash"`
	Generation uint64        `json:"generation"`
	LoadedAt   time.Time     `json:"loaded_at"`
}

type firewallStatusResponse struct {
	Enabled      bool      `json:"enabled"`
	PolicyFile   string    `json:"policy_file"`
	Source       string    `json:"source"`
	Loaded       bool      `json:"loaded"`
	State        string    `json:"state"`
	Version      int       `json:"version"`
	Hash         string    `json:"hash"`
	Generation   uint64    `json:"generation"`
	LoadedAt     time.Time `json:"loaded_at"`
	LastError    string    `json:"last_error,omitempty"`
	LastReloadAt time.Time `json:"last_reload_at"`
}

func (c *ControlServer) rpcGetFirewallPolicy(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.firewallProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "firewall policy provider not available")
	}
	snapshot := provider.Policy()
	return rpcOK(firewallPolicyResponse{
		Version:    snapshot.Document.Version,
		Default:    snapshot.Document.Default,
		Rules:      snapshot.Document.Rules,
		Source:     snapshot.Source,
		Hash:       snapshot.Hash,
		Generation: snapshot.Generation,
		LoadedAt:   snapshot.LoadedAt,
	})
}

func (c *ControlServer) rpcGetFirewallStatus(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.firewallProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "firewall policy provider not available")
	}
	return rpcOK(toFirewallStatusResponse(provider.Status()))
}

func (c *ControlServer) rpcValidateFirewallPolicy(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	var params validateFirewallPolicyParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.firewallProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "firewall policy provider not available")
	}
	var (
		result policy.ValidationResult
		err    error
	)
	if params.Content != nil {
		result, err = provider.Validate([]byte(*params.Content))
	} else {
		result, err = provider.ValidateFile()
	}
	if err != nil {
		return rpcError(firewallErrorStatus(err), err.Error())
	}
	return rpcOK(map[string]any{
		"valid":  true,
		"policy": result.Document,
		"hash":   result.Hash,
	})
}

func (c *ControlServer) rpcReloadFirewallPolicy(ctx *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	provider := c.firewallProvider()
	if provider == nil {
		return rpcError(http.StatusServiceUnavailable, "firewall policy provider not available")
	}
	err := provider.Reload()
	status := provider.Status()
	if err != nil {
		util.Event(c.logger, slogLevelWarn(), "firewall.policy.reload",
			"request.id", ctx.Meta.id,
			"result", "failed",
			"policy.version", status.Version,
			"policy.hash", status.Hash,
			"error", err,
		)
		return rpcError(firewallErrorStatus(err), err.Error())
	}
	util.Event(c.logger, slogLevelInfo(), "firewall.policy.reload",
		"request.id", ctx.Meta.id,
		"result", "success",
		"policy.version", status.Version,
		"policy.hash", status.Hash,
		"policy.generation", status.Generation,
	)
	return rpcOK(toFirewallStatusResponse(status))
}

func toFirewallStatusResponse(status policy.Status) firewallStatusResponse {
	return firewallStatusResponse{
		Enabled:      status.Enabled,
		PolicyFile:   status.PolicyFile,
		Source:       status.Source,
		Loaded:       status.Loaded,
		State:        status.State,
		Version:      status.Version,
		Hash:         status.Hash,
		Generation:   status.Generation,
		LoadedAt:     status.LoadedAt,
		LastError:    status.LastError,
		LastReloadAt: status.LastReloadAt,
	}
}

func firewallErrorStatus(err error) int {
	if errors.Is(err, policy.ErrDisabled) || errors.Is(err, policy.ErrNoPolicyFile) {
		return http.StatusServiceUnavailable
	}
	var fileErr *policy.FileError
	if errors.As(err, &fileErr) {
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}
