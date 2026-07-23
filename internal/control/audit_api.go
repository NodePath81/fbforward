package control

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/NodePath81/fbforward/internal/audit"
)

type queryIPLogParams struct {
	StartTime *int64 `json:"start_time,omitempty"`
	EndTime   *int64 `json:"end_time,omitempty"`
	CIDR      string `json:"cidr,omitempty"`
	IP        string `json:"ip,omitempty"`
	ASN       *int   `json:"asn,omitempty"`
	Country   string `json:"country,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	SortBy    string `json:"sort_by,omitempty"`
	SortOrder string `json:"sort_order,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

type queryRejectionLogParams struct {
	StartTime        *int64 `json:"start_time,omitempty"`
	EndTime          *int64 `json:"end_time,omitempty"`
	CIDR             string `json:"cidr,omitempty"`
	IP               string `json:"ip,omitempty"`
	ASN              *int   `json:"asn,omitempty"`
	Country          string `json:"country,omitempty"`
	Tag              string `json:"tag,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	Port             *int   `json:"port,omitempty"`
	MatchedRuleType  string `json:"matched_rule_type,omitempty"`
	MatchedRuleValue string `json:"matched_rule_value,omitempty"`
	SortBy           string `json:"sort_by,omitempty"`
	SortOrder        string `json:"sort_order,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	Offset           int    `json:"offset,omitempty"`
}

type queryLogEventsParams struct {
	StartTime        *int64 `json:"start_time,omitempty"`
	EndTime          *int64 `json:"end_time,omitempty"`
	CIDR             string `json:"cidr,omitempty"`
	IP               string `json:"ip,omitempty"`
	ASN              *int   `json:"asn,omitempty"`
	Country          string `json:"country,omitempty"`
	Tag              string `json:"tag,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	Upstream         string `json:"upstream,omitempty"`
	Port             *int   `json:"port,omitempty"`
	Reason           string `json:"reason,omitempty"`
	MatchedRuleType  string `json:"matched_rule_type,omitempty"`
	MatchedRuleValue string `json:"matched_rule_value,omitempty"`
	EntryType        string `json:"entry_type,omitempty"`
	SortBy           string `json:"sort_by,omitempty"`
	SortOrder        string `json:"sort_order,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	Offset           int    `json:"offset,omitempty"`
}

type ipLogStatusResponse struct {
	DBPath               string `json:"db_path"`
	FileSize             int64  `json:"file_size"`
	RecordCount          int    `json:"record_count"`
	FlowRecordCount      int    `json:"flow_record_count"`
	RejectionRecordCount int    `json:"rejection_record_count"`
	TotalRecordCount     int    `json:"total_record_count"`
	OldestRecordAt       int64  `json:"oldest_record_at"`
	NewestRecordAt       int64  `json:"newest_record_at"`
	Retention            string `json:"retention"`
	PruneInterval        string `json:"prune_interval"`
}

func (c *ControlServer) auditDB() *audit.Store {
	c.iplogMu.RLock()
	defer c.iplogMu.RUnlock()
	return c.auditStore
}

func (c *ControlServer) rpcGetIPLogStatus(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	if fault := decodeOptionalParams(raw, &struct{}{}); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	result, err := c.getIPLogStatus(store)
	if err != nil {
		return rpcError(http.StatusInternalServerError, err.Error())
	}
	return rpcOK(result)
}

func (c *ControlServer) rpcQueryIPLog(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	var params queryIPLogParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	result, err := store.Query(audit.QueryParams{
		StartTime: params.StartTime, EndTime: params.EndTime, CIDR: params.CIDR, IP: params.IP, ASN: params.ASN,
		Country: params.Country, Tag: params.Tag, Protocol: params.Protocol, Upstream: params.Upstream, SortBy: params.SortBy, SortOrder: params.SortOrder,
		Limit: params.Limit, Offset: params.Offset,
	})
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(result)
}

func (c *ControlServer) rpcQueryRejectionLog(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	var params queryRejectionLogParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	result, err := store.QueryRejections(audit.RejectionQueryParams{
		StartTime: params.StartTime, EndTime: params.EndTime, CIDR: params.CIDR, IP: params.IP, ASN: params.ASN,
		Country: params.Country, Reason: params.Reason, Protocol: params.Protocol, Port: params.Port,
		MatchedRuleType: params.MatchedRuleType, MatchedRuleValue: params.MatchedRuleValue,
		SortBy: params.SortBy, SortOrder: params.SortOrder, Limit: params.Limit, Offset: params.Offset,
	})
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(result)
}

func (c *ControlServer) rpcQueryLogEvents(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	var params queryLogEventsParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	result, err := store.QueryLogEvents(audit.LogEventQueryParams{
		StartTime: params.StartTime, EndTime: params.EndTime, CIDR: params.CIDR, IP: params.IP, ASN: params.ASN,
		Country: params.Country, Tag: params.Tag, Protocol: params.Protocol, Upstream: params.Upstream, Port: params.Port, Reason: params.Reason,
		MatchedRuleType: params.MatchedRuleType, MatchedRuleValue: params.MatchedRuleValue,
		EntryType: params.EntryType, SortBy: params.SortBy, SortOrder: params.SortOrder,
		Limit: params.Limit, Offset: params.Offset,
	})
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(result)
}

type topTalkersParams struct {
	StartTime *int64 `json:"start_time,omitempty"`
	EndTime   *int64 `json:"end_time,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func (c *ControlServer) rpcGetTopTalkers(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	var params topTalkersParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	result, err := store.GetTopTalkers(audit.TopTalkerParams{
		StartTime: params.StartTime,
		EndTime:   params.EndTime,
		Protocol:  params.Protocol,
		Upstream:  params.Upstream,
		Tag:       params.Tag,
		Limit:     params.Limit,
	})
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(result)
}

func (c *ControlServer) getIPLogStatus(store *audit.Store) (ipLogStatusResponse, error) {
	stats, err := store.Stats()
	if err != nil {
		return ipLogStatusResponse{}, err
	}
	return ipLogStatusResponse{
		DBPath:               c.fullCfg.IPLog.DBPath,
		FileSize:             dbFileSize(c.fullCfg.IPLog.DBPath),
		RecordCount:          stats.TotalRecordCount,
		FlowRecordCount:      stats.FlowRecordCount,
		RejectionRecordCount: stats.RejectionRecordCount,
		TotalRecordCount:     stats.TotalRecordCount,
		OldestRecordAt:       stats.OldestRecordAt,
		NewestRecordAt:       stats.NewestRecordAt,
		Retention:            c.fullCfg.IPLog.Retention.Duration().String(),
		PruneInterval:        c.fullCfg.IPLog.PruneInterval.Duration().String(),
	}, nil
}

func dbFileSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
