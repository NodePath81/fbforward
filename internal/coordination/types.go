package coordination

type HelloMessage struct {
	Type   string `json:"type"`
	Pool   string `json:"pool"`
	NodeID string `json:"node_id"`
}

type PreferencesMessage struct {
	Type           string   `json:"type"`
	Upstreams      []string `json:"upstreams"`
	ActiveUpstream string   `json:"active_upstream,omitempty"`
}

type HeartbeatMessage struct {
	Type string `json:"type"`
}

type PickMessage struct {
	Type     string  `json:"type"`
	Version  int64   `json:"version"`
	Upstream *string `json:"upstream"`
}

type ErrorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
