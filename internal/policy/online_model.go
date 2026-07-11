package policy

import (
	"errors"
	"time"

	"github.com/NodePath81/fbforward/internal/util"
)

const (
	MaxOnlineRuleTTL     = 24 * time.Hour
	OnlineRuleVersion    = "1"
	MaxOnlineRules       = 10000
	MaxOnlineRuleID      = 128
	MaxOnlineReason      = 1024
	MaxOnlineTicket      = 128
	MaxOnlineUpstream    = 128
	MaxOnlineMatcherJSON = 4096
	MaxOnlineParamsJSON  = 4096
	MinOnlinePriority    = -100000
	MaxOnlinePriority    = 100000
)

var (
	ErrOnlineStoreUnavailable = errors.New("online rule store is unavailable")
	ErrOnlineRuleInvalid      = errors.New("invalid online rule")
	ErrOnlineRuleCapacity     = errors.New("online rule capacity exceeded")
)

type OnlineMatcher struct {
	SourceCIDR string `json:"source_cidr,omitempty"`
	SourceIP   string `json:"source_ip,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Port       *int   `json:"port,omitempty"`
}

type OnlineParams struct {
	LimitBPS uint64 `json:"limit_bps,omitempty"`
	Upstream string `json:"upstream,omitempty"`
}

type OnlineRuleSpec struct {
	RuleID    string
	Action    string
	Matcher   OnlineMatcher
	Params    OnlineParams
	Priority  int
	TTL       time.Duration
	Reason    string
	TicketRef string
	CreatedBy string
	Source    string
}

type OnlineEvaluation struct {
	Matched          bool
	Allowed          bool
	RuleID           string
	RuleType         string
	RuleValue        string
	Action           string
	RateLimitBPS     uint64
	UpstreamOverride string
}

type OnlineTelemetry interface {
	SetOnlineRulesActive(int)
	IncOnlineRuleExpiryError()
}

type OnlineProviderOptions struct {
	MaxRules          int
	UpstreamAvailable func(string) bool
	ExpiryInterval    time.Duration
	Logger            util.Logger
	Telemetry         OnlineTelemetry
}

type OnlineProviderStatus struct {
	ActiveRules       int
	LastError         string
	LastExpiryAt      time.Time
	ExpiryErrorsTotal uint64
}
