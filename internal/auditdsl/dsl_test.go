package auditdsl

import (
	"strings"
	"testing"
	"time"
)

func TestParseFlowPipeline(t *testing.T) {
	query, err := Parse(`events tag=app:test reason="connection limit" since=-24h | sort recorded_at desc | limit 50 | offset 10`)
	if err != nil {
		t.Fatal(err)
	}
	if query.Source != SourceEvents || query.Filters["tag"] != "app:test" || query.Filters["reason"] != "connection limit" {
		t.Fatalf("unexpected query: %#v", query)
	}
	if query.SortBy != "recorded_at" || query.SortOrder != "desc" || query.Limit != 50 || query.Offset != 10 {
		t.Fatalf("unexpected pipeline: %#v", query)
	}
}

func TestParseSourcesAndValidation(t *testing.T) {
	tests := []struct {
		input string
		want  Source
	}{
		{"rejections reason=deny", SourceRejections},
		{"events protocol=tcp", SourceEvents},
		{"top clients tag=app:test", SourceTopClients},
		{"top asns protocol=udp", SourceTopASNs},
	}
	for _, test := range tests {
		query, err := Parse(test.input)
		if err != nil || query.Source != test.want {
			t.Errorf("Parse(%q) = %#v, %v", test.input, query, err)
		}
	}
	for _, input := range []string{
		"flows unknown=x",
		"flows protocol=icmp",
		"flows | sort client_ip desc",
		"flows | limit 1001",
		"flows | offset -1",
		"flows | sort bytes_total desc | sort ip asc",
		"flows | sort recorded_at desc | sort ip asc",
		"flows | limit 200 | limit 200",
		"top",
		"flows tag=x | raw_sql=bad",
	} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", input)
		}
	}
}

func TestParseTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	got, err := ParseTime("-15m", now)
	if err != nil || got != now.Add(-15*time.Minute).Unix() {
		t.Fatalf("relative time = %d, %v", got, err)
	}
	got, err = ParseTime("-7d", now)
	if err != nil || got != now.Add(-7*24*time.Hour).Unix() {
		t.Fatalf("day time = %d, %v", got, err)
	}
	if _, err := ParseTime("yesterday", now); err == nil {
		t.Fatal("invalid time accepted")
	}
}

func TestQueryLimit(t *testing.T) {
	if _, err := Parse(strings.Repeat("x", MaxQueryBytes+1)); err == nil {
		t.Fatal("oversized query accepted")
	}
}
