// Package auditdsl parses the deliberately small query language used by the
// operator audit page. It never produces SQL; callers map the typed result to
// the existing parameterized audit queries.
package auditdsl

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	MaxQueryBytes = 4096
	DefaultLimit  = 200
	MaxLimit      = 1000
)

type Source string

const (
	SourceFlows      Source = "flows"
	SourceRejections Source = "rejections"
	SourceEvents     Source = "events"
	SourceTopClients Source = "top clients"
	SourceTopASNs    Source = "top asns"
)

type Query struct {
	Source    Source
	Filters   map[string]string
	SortBy    string
	SortOrder string
	Limit     int
	Offset    int
}

var commonFilters = map[string]bool{
	"protocol": true, "cidr": true, "ip": true, "asn": true,
	"country": true, "upstream": true, "reason": true,
	"since": true, "until": true, "tag": true,
}

var sourceFilters = map[Source]map[string]bool{
	SourceFlows:      {"protocol": true, "cidr": true, "ip": true, "asn": true, "country": true, "upstream": true, "tag": true, "since": true, "until": true},
	SourceRejections: {"protocol": true, "cidr": true, "ip": true, "asn": true, "country": true, "reason": true, "since": true, "until": true},
	SourceEvents:     commonFilters,
	SourceTopClients: {"protocol": true, "upstream": true, "tag": true, "since": true, "until": true},
	SourceTopASNs:    {"protocol": true, "upstream": true, "tag": true, "since": true, "until": true},
}

var sortFields = map[Source]map[string]bool{
	SourceFlows:      {"recorded_at": true, "bytes_up": true, "bytes_down": true, "bytes_total": true, "duration_ms": true, "ip": true, "asn": true, "country": true, "protocol": true, "upstream": true},
	SourceRejections: {"recorded_at": true, "ip": true, "asn": true, "country": true, "protocol": true, "port": true, "reason": true},
	SourceEvents:     {"recorded_at": true, "ip": true, "asn": true, "country": true, "protocol": true, "port": true, "entry_type": true},
	SourceTopClients: {"bytes_total": true, "bytes_up": true, "bytes_down": true, "flow_count": true, "client_ip": true},
	SourceTopASNs:    {"bytes_total": true, "bytes_up": true, "bytes_down": true, "flow_count": true, "asn": true},
}

type token struct {
	value  string
	quoted bool
}

func tokenize(input string) ([]token, error) {
	var out []token
	for i := 0; i < len(input); {
		for i < len(input) && (input[i] == ' ' || input[i] == '\t' || input[i] == '\r' || input[i] == '\n') {
			i++
		}
		if i == len(input) {
			break
		}
		if input[i] == '|' {
			out = append(out, token{value: "|"})
			i++
			continue
		}
		var value strings.Builder
		quoted := false
		closedQuote := false
		for i < len(input) {
			if input[i] == ' ' || input[i] == '\t' || input[i] == '\r' || input[i] == '\n' || input[i] == '|' {
				break
			}
			if input[i] == '"' {
				if closedQuote {
					return nil, fmt.Errorf("quoted value must be separated")
				}
				start := i
				i++
				for i < len(input) {
					if input[i] == '\\' {
						i += 2
						continue
					}
					if input[i] == '"' {
						raw, err := strconv.Unquote(input[start : i+1])
						if err != nil {
							return nil, fmt.Errorf("invalid quoted value")
						}
						value.WriteString(raw)
						i++
						quoted, closedQuote = true, true
						break
					}
					i++
				}
				if !closedQuote {
					return nil, fmt.Errorf("unterminated quoted value")
				}
				continue
			}
			value.WriteByte(input[i])
			i++
		}
		if value.Len() == 0 {
			return nil, fmt.Errorf("invalid token")
		}
		out = append(out, token{value: value.String(), quoted: quoted})
	}
	return out, nil
}

func Parse(input string) (Query, error) {
	if len(input) == 0 || len(input) > MaxQueryBytes {
		return Query{}, fmt.Errorf("query must be between 1 and %d bytes", MaxQueryBytes)
	}
	tokens, err := tokenize(input)
	if err != nil {
		return Query{}, err
	}
	if len(tokens) == 0 {
		return Query{}, fmt.Errorf("query is empty")
	}
	q := Query{Filters: make(map[string]string), Limit: DefaultLimit, SortOrder: "desc"}
	i := 0
	switch tokens[i].value {
	case string(SourceFlows):
		q.Source = SourceFlows
	case string(SourceRejections):
		q.Source = SourceRejections
	case string(SourceEvents):
		q.Source = SourceEvents
	case "top":
		if len(tokens) < 2 || (tokens[1].value != "clients" && tokens[1].value != "asns") {
			return Query{}, fmt.Errorf("top must be followed by clients or asns")
		}
		if tokens[1].value == "clients" {
			q.Source = SourceTopClients
		} else {
			q.Source = SourceTopASNs
		}
		i++
	default:
		return Query{}, fmt.Errorf("unknown source %q", tokens[i].value)
	}
	i++
	for i < len(tokens) && tokens[i].value != "|" {
		item := tokens[i].value
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" || value == "" {
			return Query{}, fmt.Errorf("filter must be key=value")
		}
		key = strings.ToLower(key)
		if !sourceFilters[q.Source][key] {
			return Query{}, fmt.Errorf("filter %q is not valid for %s", key, q.Source)
		}
		if _, exists := q.Filters[key]; exists {
			return Query{}, fmt.Errorf("filter %q is repeated", key)
		}
		q.Filters[key] = value
		i++
	}
	for i < len(tokens) {
		if tokens[i].value != "|" {
			return Query{}, fmt.Errorf("expected pipeline separator")
		}
		i++
		if i >= len(tokens) {
			return Query{}, fmt.Errorf("pipeline stage is missing")
		}
		switch tokens[i].value {
		case "sort":
			if i+2 >= len(tokens) || tokens[i+1].value == "|" || tokens[i+2].value == "|" {
				return Query{}, fmt.Errorf("sort requires field and order")
			}
			if !sortFields[q.Source][tokens[i+1].value] {
				return Query{}, fmt.Errorf("sort field %q is not valid for %s", tokens[i+1].value, q.Source)
			}
			order := strings.ToLower(tokens[i+2].value)
			if order != "asc" && order != "desc" {
				return Query{}, fmt.Errorf("sort order must be asc or desc")
			}
			if q.SortBy != "" && q.SortBy != "recorded_at" {
				return Query{}, fmt.Errorf("sort may be specified once")
			}
			q.SortBy, q.SortOrder = tokens[i+1].value, order
			i += 3
		case "limit", "offset":
			if i+1 >= len(tokens) || tokens[i+1].value == "|" {
				return Query{}, fmt.Errorf("%s requires a number", tokens[i].value)
			}
			n, parseErr := strconv.Atoi(tokens[i+1].value)
			if parseErr != nil || n < 0 {
				return Query{}, fmt.Errorf("%s must be a non-negative number", tokens[i].value)
			}
			if tokens[i].value == "limit" {
				if n == 0 || n > MaxLimit {
					return Query{}, fmt.Errorf("limit must be between 1 and %d", MaxLimit)
				}
				if q.Limit != DefaultLimit {
					return Query{}, fmt.Errorf("limit may be specified once")
				}
				q.Limit = n
			} else {
				if q.Offset != 0 {
					return Query{}, fmt.Errorf("offset may be specified once")
				}
				q.Offset = n
			}
			i += 2
		default:
			return Query{}, fmt.Errorf("unknown pipeline stage %q", tokens[i].value)
		}
	}
	if q.SortBy == "" {
		q.SortBy = "recorded_at"
	}
	for key, value := range q.Filters {
		switch key {
		case "protocol":
			if value != "tcp" && value != "udp" {
				return Query{}, fmt.Errorf("protocol must be tcp or udp")
			}
		case "asn":
			if _, err := strconv.Atoi(value); err != nil {
				return Query{}, fmt.Errorf("asn must be an integer")
			}
		case "since", "until":
			if _, err := ParseTime(value, time.Now().UTC()); err != nil {
				return Query{}, err
			}
		case "cidr":
			if strings.ContainsAny(value, "'\"") {
				return Query{}, fmt.Errorf("invalid cidr")
			}
		case "country":
			if len(value) != 2 {
				return Query{}, fmt.Errorf("country must be a two-letter code")
			}
		}
	}
	return q, nil
}

func ParseTime(raw string, now time.Time) (int64, error) {
	if strings.HasPrefix(raw, "-") {
		value := strings.TrimPrefix(raw, "-")
		if strings.HasSuffix(value, "d") {
			n, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
			if err == nil && n > 0 {
				return now.Add(-time.Duration(n) * 24 * time.Hour).Unix(), nil
			}
		}
		if d, err := time.ParseDuration("-" + value); err == nil && d < 0 {
			return now.Add(d).Unix(), nil
		}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, fmt.Errorf("time must be a relative duration or RFC3339 timestamp")
	}
	return parsed.Unix(), nil
}

func (q Query) Time(key string, now time.Time) (*int64, error) {
	raw, ok := q.Filters[key]
	if !ok {
		return nil, nil
	}
	value, err := ParseTime(raw, now)
	if err != nil {
		return nil, err
	}
	return &value, nil
}
