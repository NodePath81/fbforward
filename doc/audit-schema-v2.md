# Audit SQLite schema v3

`internal/audit` is the authoritative local audit store. SQLite stores UTC
Unix milliseconds; compatibility callers under `internal/iplog` retain the
existing RPC response shape.

The migration is transactional and controlled by `PRAGMA user_version`. The
current version is `3`. Existing `ip_log` and `rejection_log` tables are kept
for one compatibility period and their rows are copied with stable IDs:
`legacy-ip-log:<id>` and `legacy-rejection:<id>`.

The `flows` table contains one complete TCP stream or UDP mapping lifecycle.
`flow_entities` stores the identity and backend tuple while a Flow is active
and through the Registry grace period. `flow_checkpoints` contains coalesced
cumulative snapshots, never packet-level records. Flow tag events and current
tags reference `flow_entities`, so active Flow tagging never creates a partial
row in `flows`.

Queries perform filtering, ordering, aggregation, and pagination in SQLite.
The derived binary IP columns allow CIDR ranges to be pushed into SQL instead
of loading the whole database into Go memory. `GetTopTalkers` is an additive
read-only control RPC backed by a grouped SQL query.
