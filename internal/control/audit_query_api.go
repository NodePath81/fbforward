package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NodePath81/fbforward/internal/audit"
	"github.com/NodePath81/fbforward/internal/auditdsl"
)

type queryAuditParams struct {
	Query string `json:"query"`
}

type topASNsParams struct {
	StartTime *int64 `json:"start_time,omitempty"`
	EndTime   *int64 `json:"end_time,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	Tag       string `json:"tag,omitempty"`
	SortBy    string `json:"sort_by,omitempty"`
	SortOrder string `json:"sort_order,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

type auditQueryResponse struct {
	Query  string      `json:"query"`
	Source string      `json:"source"`
	Result interface{} `json:"result"`
}

func (c *ControlServer) rpcGetTopASNs(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	var params topASNsParams
	if fault := decodeOptionalParams(raw, &params); fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	result, err := store.GetTopASNs(audit.TopASNParams{
		StartTime: params.StartTime, EndTime: params.EndTime, Protocol: params.Protocol,
		Upstream: params.Upstream, Tag: params.Tag, SortBy: params.SortBy,
		SortOrder: params.SortOrder, Limit: params.Limit, Offset: params.Offset,
	})
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(result)
}

func decodeAuditQuery(raw json.RawMessage) (queryAuditParams, *rpcFault) {
	if len(raw) == 0 || string(raw) == "null" {
		return queryAuditParams{}, &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var params queryAuditParams
	if err := decoder.Decode(&params); err != nil || strings.TrimSpace(params.Query) == "" {
		return queryAuditParams{}, &rpcFault{Status: http.StatusBadRequest, Message: "invalid params"}
	}
	return params, nil
}

func (c *ControlServer) rpcQueryAudit(_ *rpcContext, raw json.RawMessage) (any, *rpcFault) {
	store := c.auditDB()
	if store == nil {
		return rpcError(http.StatusServiceUnavailable, "ip log store not available")
	}
	params, fault := decodeAuditQuery(raw)
	if fault != nil {
		return rpcError(fault.Status, fault.Message)
	}
	query, err := auditdsl.Parse(params.Query)
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	if query.Source == auditdsl.SourceTopClients || query.Source == auditdsl.SourceTopASNs || query.Source == auditdsl.SourceTopTags {
		start, end, err := auditQueryTimes(query, now)
		if err != nil {
			return rpcError(http.StatusBadRequest, err.Error())
		}
		if query.Source == auditdsl.SourceTopClients {
			result, err := store.GetTopTalkers(audit.TopTalkerParams{
				StartTime: start, EndTime: end, Protocol: query.Filters["protocol"],
				Upstream: query.Filters["upstream"], Tag: query.Filters["tag"],
				SortBy: query.SortBy, SortOrder: query.SortOrder, Limit: query.Limit, Offset: query.Offset,
			})
			if err != nil {
				return rpcError(http.StatusBadRequest, err.Error())
			}
			return rpcOK(auditQueryResponse{Query: params.Query, Source: string(query.Source), Result: result})
		}
		if query.Source == auditdsl.SourceTopTags {
			result, err := store.GetTopTags(audit.TopTagParams{
				StartTime: start, EndTime: end, Protocol: query.Filters["protocol"],
				Upstream: query.Filters["upstream"], SortBy: query.SortBy,
				SortOrder: query.SortOrder, Limit: query.Limit, Offset: query.Offset,
			})
			if err != nil {
				return rpcError(http.StatusBadRequest, err.Error())
			}
			return rpcOK(auditQueryResponse{Query: params.Query, Source: string(query.Source), Result: result})
		}
		result, err := store.GetTopASNs(audit.TopASNParams{
			StartTime: start, EndTime: end, Protocol: query.Filters["protocol"],
			Upstream: query.Filters["upstream"], Tag: query.Filters["tag"],
			SortBy: query.SortBy, SortOrder: query.SortOrder, Limit: query.Limit, Offset: query.Offset,
		})
		if err != nil {
			return rpcError(http.StatusBadRequest, err.Error())
		}
		return rpcOK(auditQueryResponse{Query: params.Query, Source: string(query.Source), Result: result})
	}
	start, end, err := auditQueryTimes(query, now)
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	asn, err := auditQueryASN(query)
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	entryType := audit.EntryTypeAll
	if query.Source == auditdsl.SourceFlows {
		entryType = audit.EntryTypeFlow
	} else if query.Source == auditdsl.SourceRejections {
		entryType = audit.EntryTypeRejection
	}
	result, err := store.QueryLogEvents(audit.LogEventQueryParams{
		StartTime: start, EndTime: end, CIDR: query.Filters["cidr"], IP: query.Filters["ip"], ASN: asn,
		Country: query.Filters["country"], Tag: query.Filters["tag"], Protocol: query.Filters["protocol"],
		Upstream: query.Filters["upstream"], Reason: query.Filters["reason"], EntryType: entryType,
		SortBy: query.SortBy, SortOrder: query.SortOrder, Limit: query.Limit, Offset: query.Offset,
	})
	if err != nil {
		return rpcError(http.StatusBadRequest, err.Error())
	}
	return rpcOK(auditQueryResponse{Query: params.Query, Source: string(query.Source), Result: result})
}

func auditQueryTimes(query auditdsl.Query, now time.Time) (*int64, *int64, error) {
	start, err := query.Time("since", now)
	if err != nil {
		return nil, nil, err
	}
	end, err := query.Time("until", now)
	if err != nil {
		return nil, nil, err
	}
	return start, end, nil
}

func auditQueryASN(query auditdsl.Query) (*int, error) {
	raw := query.Filters["asn"]
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("asn must be an integer")
	}
	return &value, nil
}
