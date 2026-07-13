# Audit query language

The Audit page and the `QueryAudit` RPC use a small, server-side query
language. It is intentionally not SQL. The daemon parses a fixed set of
sources and fields, then maps them to parameterized SQLite queries.

Examples:

```text
flows tag=app:test since=-24h | sort bytes_total desc | limit 50
top asns tag=app:test since=-24h | sort bytes_total desc | limit 20
rejections protocol=tcp reason="connection limit" | limit 100
```

Sources are `flows`, `rejections`, `events`, `top clients`, and `top asns`.
Filters use `key=value` and are combined with AND. Supported filters are
`tag`, `protocol`, `cidr`, `ip`, `asn`, `country`, `upstream`, `reason`,
`since`, and `until` where applicable. Relative times use `-15m`, `-24h`, or
`-7d`; absolute times use RFC3339.

Pipeline stages are `sort field asc|desc`, `limit number`, and `offset number`.
The query is bounded to 4096 bytes and 1000 rows. Unknown fields, SQL syntax,
OR, joins, functions, and subqueries are rejected. Results remain bounded and
are filtered, aggregated, sorted, and paginated by SQLite.

`QueryAudit` returns `{query, source, result}`. Flow, rejection, and event
results use the existing `{total, records}` shape. `top clients` and `top asns`
return aggregation rows; ASN rows contain `asn`, `as_org`, `country`, byte
totals, and flow count. The browser may store the query text in the Audit URL,
but never stores or appends the bearer token.
