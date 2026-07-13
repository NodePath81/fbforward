// Package auditdsl parses the deliberately small query language used by the
// operator audit page. It never produces SQL; callers map the typed result to
// the existing parameterized audit queries.
package auditdsl

import (
	"fmt"
	"net/netip"
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
	value string
	pos   int
}

func syntaxError(pos int, message string) error {
	return fmt.Errorf("byte %d: %s", pos+1, message)
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
			out = append(out, token{value: "|", pos: i})
			i++
			continue
		}
		var value strings.Builder
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
							return nil, syntaxError(start, "invalid quoted value")
						}
						value.WriteString(raw)
						i++
						closedQuote = true
						break
					}
					i++
				}
				if !closedQuote {
					return nil, syntaxError(start, "unterminated quoted value")
				}
				continue
			}
			value.WriteByte(input[i])
			i++
		}
		if value.Len() == 0 {
			return nil, syntaxError(i, "invalid token")
		}
		out = append(out, token{value: value.String(), pos: i - value.Len()})
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
		return Query{}, syntaxError(0, "query is empty")
	}
	q := Query{Filters: make(map[string]string), Limit: DefaultLimit, SortOrder: "desc"}
	filterPositions := make(map[string]int)
	sortSet, limitSet, offsetSet := false, false, false
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
			return Query{}, syntaxError(tokens[i].pos, "top must be followed by clients or asns")
		}
		if tokens[1].value == "clients" {
			q.Source = SourceTopClients
		} else {
			q.Source = SourceTopASNs
		}
		i++
	default:
		return Query{}, syntaxError(tokens[i].pos, fmt.Sprintf("unknown source %q", tokens[i].value))
	}
	i++
	for i < len(tokens) && tokens[i].value != "|" {
		item := tokens[i].value
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" || value == "" {
			return Query{}, syntaxError(tokens[i].pos, "filter must be key=value")
		}
		key = strings.ToLower(key)
		if !sourceFilters[q.Source][key] {
			return Query{}, syntaxError(tokens[i].pos, fmt.Sprintf("filter %q is not valid for %s", key, q.Source))
		}
		if _, exists := q.Filters[key]; exists {
			return Query{}, syntaxError(tokens[i].pos, fmt.Sprintf("filter %q is repeated", key))
		}
		q.Filters[key] = value
		filterPositions[key] = tokens[i-1].pos
		i++
	}
	for i < len(tokens) {
		if tokens[i].value != "|" {
			return Query{}, syntaxError(tokens[i].pos, "expected pipeline separator")
		}
		i++
		if i >= len(tokens) {
			return Query{}, syntaxError(tokens[i-1].pos, "pipeline stage is missing")
		}
		switch tokens[i].value {
		case "sort":
			if i+2 >= len(tokens) || tokens[i+1].value == "|" || tokens[i+2].value == "|" {
				return Query{}, syntaxError(tokens[i].pos, "sort requires field and order")
			}
			if !sortFields[q.Source][tokens[i+1].value] {
				return Query{}, syntaxError(tokens[i+1].pos, fmt.Sprintf("sort field %q is not valid for %s", tokens[i+1].value, q.Source))
			}
			order := strings.ToLower(tokens[i+2].value)
			if order != "asc" && order != "desc" {
				return Query{}, syntaxError(tokens[i+2].pos, "sort order must be asc or desc")
			}
			if sortSet {
				return Query{}, syntaxError(tokens[i].pos, "sort may be specified once")
			}
			q.SortBy, q.SortOrder = tokens[i+1].value, order
			sortSet = true
			i += 3
		case "limit", "offset":
			if i+1 >= len(tokens) || tokens[i+1].value == "|" {
				return Query{}, syntaxError(tokens[i].pos, fmt.Sprintf("%s requires a number", tokens[i].value))
			}
			n, parseErr := strconv.Atoi(tokens[i+1].value)
			if parseErr != nil || n < 0 {
				return Query{}, syntaxError(tokens[i+1].pos, fmt.Sprintf("%s must be a non-negative number", tokens[i].value))
			}
			if tokens[i].value == "limit" {
				if n == 0 || n > MaxLimit {
					return Query{}, syntaxError(tokens[i+1].pos, fmt.Sprintf("limit must be between 1 and %d", MaxLimit))
				}
				if limitSet {
					return Query{}, syntaxError(tokens[i].pos, "limit may be specified once")
				}
				q.Limit = n
				limitSet = true
			} else {
				if offsetSet {
					return Query{}, syntaxError(tokens[i].pos, "offset may be specified once")
				}
				q.Offset = n
				offsetSet = true
			}
			i += 2
		default:
			return Query{}, syntaxError(tokens[i].pos, fmt.Sprintf("unknown pipeline stage %q", tokens[i].value))
		}
	}
	if q.SortBy == "" {
		if q.Source == SourceTopClients || q.Source == SourceTopASNs {
			q.SortBy = "bytes_total"
		} else {
			q.SortBy = "recorded_at"
		}
	}
	now := time.Now().UTC()
	var since, until *int64
	for key, value := range q.Filters {
		pos := filterPositions[key]
		switch key {
		case "protocol":
			value = strings.ToLower(value)
			if value != "tcp" && value != "udp" {
				return Query{}, syntaxError(pos, "protocol must be tcp or udp")
			}
			q.Filters[key] = value
		case "asn":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return Query{}, syntaxError(pos, "asn must be a non-negative integer")
			}
		case "since", "until":
			t, err := ParseTime(value, now)
			if err != nil {
				return Query{}, syntaxError(pos, err.Error())
			}
			if key == "since" {
				since = &t
			} else {
				until = &t
			}
		case "cidr":
			if _, err := netip.ParsePrefix(value); err != nil {
				return Query{}, syntaxError(pos, "invalid cidr")
			}
		case "ip":
			if _, err := netip.ParseAddr(value); err != nil {
				return Query{}, syntaxError(pos, "invalid ip")
			}
		case "country":
			if len(value) != 2 || !isLetter(value[0]) || !isLetter(value[1]) {
				return Query{}, syntaxError(pos, "country must be a two-letter code")
			}
			q.Filters[key] = strings.ToUpper(value)
		}
	}
	if since != nil && until != nil && *since > *until {
		return Query{}, syntaxError(0, "since must be earlier than or equal to until")
	}
	return q, nil
}

func isLetter(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
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
