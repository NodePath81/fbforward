# Diagram inventory

This document catalogs all diagrams required for the fbforward documentation. Each entry includes purpose, target section, and a Mermaid template where applicable.

---

## Architecture diagrams

### D1: Three-plane architecture

**Section:** 1.2 Architecture overview
**Purpose:** Show high-level system architecture with data plane, control plane, and measurement plane
**Type:** Block diagram

```mermaid
graph TB
    subgraph Control Plane
        API[HTTP API]
        WS[WebSocket]
        Metrics[Prometheus]
    end

    subgraph Data Plane
        TCP[TCP Listener]
        UDP[UDP Listener]
        FT[Flow Table]
    end

    subgraph Measurement Plane
        ICMP[ICMP Prober]
        BW[bwprobe Client]
        Score[Scoring Engine]
    end

    subgraph Upstreams
        U1[Upstream 1]
        U2[Upstream 2]
    end

    TCP --> FT
    UDP --> FT
    FT --> U1
    FT --> U2

    ICMP --> U1
    ICMP --> U2
    BW --> U1
    BW --> U2

    Score --> FT
    API --> Score
```

### D2: Component dependency graph

**Section:** 7.1 Architecture deep dive
**Purpose:** Show Go package dependencies within internal/
**Type:** Directed graph

```mermaid
graph LR
    app[app] --> config
    app --> control
    app --> forwarding
    app --> measure
    app --> metrics
    app --> probe
    app --> upstream
    app --> shaping

    control --> upstream
    control --> metrics

    forwarding --> upstream

    measure --> upstream

    upstream --> config

    shaping --> config
```

### D3: Binary relationships

**Section:** 1.2 Architecture overview
**Purpose:** Show how fbforward, bwprobe, and fbmeasure relate
**Type:** Block diagram

```mermaid
graph LR
    subgraph fbforward host
        FB[fbforward]
    end

    subgraph Upstream hosts
        FM1[fbmeasure]
        FM2[fbmeasure]
    end

    subgraph Standalone
        BP[bwprobe CLI]
    end

    FB -->|measurement| FM1
    FB -->|measurement| FM2
    FB -->|forwarding| FM1
    FB -->|forwarding| FM2

    BP -->|test| FM1
```

---

## Data flow diagrams

### D4: Startup sequence

**Section:** 1.3 Component relationships
**Purpose:** Show initialization order from main() to running state
**Type:** Sequence diagram

```mermaid
sequenceDiagram
    participant Main
    participant Supervisor
    participant Runtime
    participant Upstream
    participant Listeners
    participant Probes

    Main->>Supervisor: New()
    Supervisor->>Runtime: Load config, create
    Runtime->>Upstream: Create UpstreamManager
    Runtime->>Listeners: Start TCP/UDP listeners
    Runtime->>Probes: Start ICMP probes
    Runtime->>Probes: Start bwprobe measurements
    Runtime-->>Supervisor: Running
```

### D5: Flow pinning lifecycle

**Section:** 6.1.1 Overview
**Purpose:** Show TCP/UDP flow creation, pinning, and removal
**Type:** State diagram

```mermaid
stateDiagram-v2
    [*] --> New: Packet arrives
    New --> Lookup: Check flow table
    Lookup --> Existing: Found
    Lookup --> Create: Not found
    Create --> Assign: Pin to primary
    Assign --> Forward
    Existing --> Forward
    Forward --> Active: Continue
    Active --> Forward: More packets
    Active --> Remove: FIN/RST or idle timeout
    Remove --> [*]
```

### D6: Request flow (TCP)

**Section:** 3.1.1 Overview
**Purpose:** Show client connection through fbforward to upstream
**Type:** Sequence diagram

```mermaid
sequenceDiagram
    participant Client
    participant Listener
    participant FlowTable
    participant Upstream

    Client->>Listener: TCP connect
    Listener->>FlowTable: Lookup/create mapping
    FlowTable-->>Listener: Upstream assignment
    Listener->>Upstream: TCP connect

    loop Bidirectional copy
        Client->>Listener: Data
        Listener->>Upstream: Data
        Upstream->>Listener: Response
        Listener->>Client: Response
    end

    Client->>Listener: FIN
    Listener->>Upstream: FIN
    Listener->>FlowTable: Remove mapping
```

### D7: Configuration flow

**Section:** 3.1.2 Configuration
**Purpose:** Show config loading from YAML to runtime components
**Type:** Flowchart

```mermaid
flowchart LR
    YAML[config.yaml] --> Load[config.Load]
    Load --> Validate[Validation]
    Validate --> Struct[Config struct]
    Struct --> Runtime
    Runtime --> Upstream[UpstreamManager]
    Runtime --> Forward[Forwarders]
    Runtime --> Measure[MeasureCollector]
    Runtime --> Control[ControlServer]
```

---

## Measurement diagrams

### D8: bwprobe two-channel design

**Section:** 6.2.1 Overview
**Purpose:** Show control and data channel separation
**Type:** Block diagram

```mermaid
graph LR
    subgraph Client
        CC[Control Client]
        DC[Data Client]
    end

    subgraph Server
        CS[Control Server]
        DS[Data Server]
    end

    CC -->|JSON-RPC| CS
    DC -->|Paced data| DS
    CS -.->|Stats| CC
```

### D9: Sample-based testing model

**Section:** 6.2.1 Overview
**Purpose:** Show sample lifecycle with control messages
**Type:** Sequence diagram

```mermaid
sequenceDiagram
    participant Client
    participant Server

    Client->>Server: session.hello
    Server-->>Client: session_id

    loop Per sample
        Client->>Server: sample.start
        Client->>Server: [Data transfer at target rate]
        Client->>Server: sample.stop
        Server-->>Client: Sample report (intervals, stats)
    end

    Client->>Server: session.goodbye
```

### D10: Scoring algorithm flow

**Section:** 6.1.2 Formal description
**Purpose:** Show score calculation from metrics to final score
**Type:** Flowchart

```mermaid
flowchart TB
    subgraph Inputs
        BW[Bandwidth]
        RTT[RTT]
        Jit[Jitter]
        Loss[Loss/Retrans]
    end

    subgraph SubScores
        SBW[s_bandwidth]
        SRTT[s_rtt]
        SJit[s_jitter]
        SLoss[s_loss]
    end

    subgraph Aggregation
        TCP[TCP Score]
        UDP[UDP Score]
        Blend[Protocol Blend]
    end

    subgraph Adjustments
        Util[Utilization Penalty]
        Bias[Bias Transform]
        Pri[Priority]
    end

    Final[Final Score]

    BW --> SBW
    RTT --> SRTT
    Jit --> SJit
    Loss --> SLoss

    SBW --> TCP
    SRTT --> TCP
    SJit --> TCP
    SLoss --> TCP

    SBW --> UDP
    SRTT --> UDP
    SJit --> UDP
    SLoss --> UDP

    TCP --> Blend
    UDP --> Blend

    Blend --> Util
    Util --> Bias
    Bias --> Pri
    Pri --> Final
```

---

## Switching diagrams

### D11: Upstream state machine

**Section:** 6.1.4 Edge cases
**Purpose:** Show upstream usability states and transitions
**Type:** State diagram

```mermaid
stateDiagram-v2
    [*] --> Unknown: Startup
    Unknown --> Usable: Probe success
    Unknown --> Unusable: Probe failure

    Usable --> Primary: Selected
    Usable --> Backup: Not selected
    Primary --> Backup: Switch away
    Backup --> Primary: Switch to

    Usable --> Unusable: Failover triggered
    Unusable --> Usable: Probe recovery

    Primary --> Unusable: Fast failover
```

### D12: Auto mode switching decision

**Section:** 4.8 Switching section
**Purpose:** Show decision tree for automatic upstream switching
**Type:** Flowchart

```mermaid
flowchart TB
    Start[Score update] --> Delta{Score delta > threshold?}
    Delta -->|No| Stay[Keep current]
    Delta -->|Yes| Hold{Hold time elapsed?}
    Hold -->|No| Wait[Wait for hold time]
    Wait --> Start
    Hold -->|Yes| Confirm{Confirm duration met?}
    Confirm -->|No| Track[Track candidate]
    Track --> Start
    Confirm -->|Yes| Switch[Switch primary]
    Switch --> Reset[Reset timers]
```

---

## Protocol diagrams

### D13: JSON-RPC message framing

**Section:** 6.3.1 Overview
**Purpose:** Show wire format of control messages
**Type:** Block diagram

```text
+----------------+------------------------+
| Length (4B BE) | JSON-RPC message       |
+----------------+------------------------+
|   0x00000042   | {"jsonrpc":"2.0",...}  |
+----------------+------------------------+
```

### D14: Control plane API structure

**Section:** 5.2.1 Overview
**Purpose:** Show HTTP endpoint hierarchy
**Type:** Tree

```text
/
├── GET  /          → Web UI (SPA)
├── POST /rpc       → JSON-RPC methods
├── GET  /metrics   → Prometheus metrics
└── GET  /status    → WebSocket stream
```

---

## Shaping diagrams

### D15: Traffic shaping architecture

**Section:** 4.10 Shaping section
**Purpose:** Show Linux tc integration with IFB for bidirectional shaping
**Type:** Block diagram

```mermaid
graph LR
    subgraph Egress
        APP1[fbforward] --> TC1[tc qdisc]
        TC1 --> ETH[eth0]
    end

    subgraph Ingress
        ETH --> IFB[ifb0]
        IFB --> TC2[tc qdisc]
        TC2 --> APP2[fbforward]
    end

    ETH --> UP[Upstream]
```

---

## Control plane diagrams

### D16: Control plane data flow

**Section:** 5.2 Control plane API, 3.1.3 Operation
**Purpose:** Show how UI/clients interact with control plane endpoints
**Type:** Sequence diagram

```mermaid
sequenceDiagram
    participant UI as Web UI
    participant Auth as /auth
    participant RPC as /rpc
    participant Metrics as /metrics
    participant WS as /status (WebSocket)
    participant Store as StatusStore

    Note over UI,Store: Initial Authentication
    UI->>Auth: GET /auth (enter token)
    UI->>RPC: POST GetStatus (validate)
    RPC-->>UI: Token valid
    UI->>UI: Store token in localStorage

    Note over UI,Store: Metrics Polling
    loop Every N seconds
        UI->>Metrics: GET /metrics (Bearer token)
        Metrics->>Store: Read metrics
        Store-->>Metrics: Current metrics
        Metrics-->>UI: Prometheus format
    end

    Note over UI,Store: Real-time Events
    UI->>WS: WebSocket connect (subprotocol token)
    WS->>Store: Subscribe
    loop On events
        Store-->>WS: Event (switch, test_complete, etc.)
        WS-->>UI: Push notification
    end

    Note over UI,Store: RPC Operations
    UI->>RPC: POST SetUpstream (Bearer token)
    RPC->>Store: Update primary
    Store-->>RPC: OK
    RPC-->>UI: Success response
```

**Data flow patterns:**

| Endpoint | Method | Purpose | Auth |
|----------|--------|---------|------|
| `/` | GET | Serve web UI SPA | None |
| `/auth` | GET | Token input page | None |
| `/rpc` | POST | JSON-RPC operations | Bearer token |
| `/metrics` | GET | Prometheus scraping | Bearer token |
| `/status` | GET | WebSocket stream | Subprotocol token |

---

## Summary

| ID | Name | Type | Section |
|----|------|------|---------|
| D1 | Three-plane architecture | Block | 1.2 |
| D2 | Component dependency graph | Directed graph | 7.1 |
| D3 | Binary relationships | Block | 1.2 |
| D4 | Startup sequence | Sequence | 1.3 |
| D5 | Flow pinning lifecycle | State | 6.1.1 |
| D6 | Request flow (TCP) | Sequence | 3.1.1 |
| D7 | Configuration flow | Flowchart | 3.1.2 |
| D8 | bwprobe two-channel design | Block | 6.2.1 |
| D9 | Sample-based testing model | Sequence | 6.2.1 |
| D10 | Scoring algorithm flow | Flowchart | 6.1.2 |
| D11 | Upstream state machine | State | 6.1.4 |
| D12 | Auto mode switching decision | Flowchart | 4.8 |
| D13 | JSON-RPC message framing | Block | 6.3.1 |
| D14 | Control plane API structure | Tree | 5.2.1 |
| D15 | Traffic shaping architecture | Block | 4.10 |
| D16 | Control plane data flow | Sequence | 5.2, 3.1.3 |
