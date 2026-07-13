# Flow schema v2

This document freezes the Flow vocabulary and lifecycle contract used by the
forwarding, status, metrics, audit, policy, and Flow Context components.

## Vocabulary

- **Flow**: one TCP stream or one UDP client mapping/session.
- **FlowID**: a 128-bit cryptographically random identifier. It is created once
  after admission and upstream selection, and remains unchanged for the Flow's
  lifetime.
- **FlowMeta**: immutable creation metadata: protocol, client address,
  listener, route, upstream, and start time.
- **FlowSummary**: immutable FlowMeta plus end time, last activity, byte
  counters, and close reason.
- **FlowTag**: a tag attached to one Flow through Flow Context.
- **ClientTag**: a tag attached to a client identity and usable by later Flows.
- **Persistent policy**: a rule declared in the versioned firewall policy file referenced by the managed configuration.
- **Online rule**: a temporary rule stored through the control API and subject
  to an explicit TTL.
- **Simple route**: a route uses one configured upstream.
- **Adaptive route**: a route chooses one upstream for each new Flow using
  health state and raw RTT; the choice is pinned for that Flow.

## Lifecycle contract

1. Firewall admission happens before a Flow is created. A rejected request
   produces a `Rejection`, not a Flow. Persistent firewall policy is loaded
   from a strict versioned YAML file and atomically reloaded; a reload only
   affects subsequently admitted Flows.
2. After admission and successful upstream selection, a Flow receives a random
   FlowID and immutable `FlowMeta`.
3. One Flow selects one upstream. A later health change never moves an
   existing TCP stream or UDP mapping to another upstream.
4. TCP close is the end of the accepted stream. UDP close is the end of the
   client mapping, normally caused by idle timeout or shutdown.
5. Counters sent in updates are cumulative snapshots. A Flow can emit zero or
   more updates and at most one close summary. Close is idempotent.
6. Persistent-policy reload affects new Flows by default. Existing Flows keep
   their admission and upstream decisions.
7. SQLite stores one primary record per complete Flow lifecycle. Packet-level
   records are not part of the v2 contract.

## FlowSummary draft

```text
flow_id
protocol
client_addr
listener
route
upstream
started_at
ended_at
last_activity
bytes_up
bytes_down
close_reason
```

Go uses `time.Time` and `netip.AddrPort` for these values. Future SQLite and
external representations use UTC Unix milliseconds for timestamps and a
canonical host:port representation for addresses. `close_reason` is an
extensible string; current reasons include `eof`, `idle_timeout`,
`context_done`, `read_error`, and `write_error`.

Flow tags and client tags are persisted through the Flow Context HTTP API.
Active Flow identity is kept in the separate `flow_entities` table; the
`flows` table remains a complete lifecycle summary and is written only at
close. Persistent policies and online rules remain internal control-plane
contracts and are not part of the Flow Context API.

Online rules are a separate, TTL-bound runtime policy layer. They are stored in
SQLite with create/delete/expire audit events and evaluated from an immutable
in-memory snapshot. Persistent policy reloads do not overwrite them. Online
denies run before the persistent decision; rate limits and route overrides only
apply after the persistent policy allows the Flow. Existing Flows retain their
original decision when either policy layer changes.

## Explicit non-goals

- PROXY protocol and TProxy are not used for client identity propagation.
- fbmeasure returns health and raw RTT; it does not own Flow state.
- upstream selection is local to each route; no distributed state is replicated.
