# bwprobe

bwprobe measures network quality at a user-specified bandwidth cap. It runs
repeatable, sample-based transfers and reports throughput derived from
server-side timing, plus RTT/jitter and loss/retransmits.

## Purpose

- Validate path quality at a fixed bandwidth budget.
- Make tests repeatable across runs and locations.
- Support upload and download tests with the same core logic.

## Architecture (control vs data)

- Control channel: JSON-RPC over TCP for session setup and per-sample
  start/stop. The client tries JSON-RPC first and falls back to the legacy
  text protocol if needed.
- Data channel: TCP or UDP data stream with per-sample framing.
  - Upload: client sends data to server.
  - Download (`-reverse`): server sends data to client over a reverse data
    connection while the client drives sample control.

## Sampling model

Each run executes a fixed number of samples:

1. Client sends `SAMPLE_START` (or `SAMPLE_START_REVERSE`) with a `sample_id`.
2. Data transfer runs at the target rate until `-sample-bytes` payload bytes are
   reached.
3. Client sends `SAMPLE_STOP`.
4. Server keeps receiving for `-recv-wait`, then replies with a per-sample
   report that includes total bytes, total duration, and per-interval stats.

Controls:

- `-samples`: number of samples to run.
- `-sample-bytes`: payload bytes per sample (headers excluded).
- `-wait`: pause between samples.
- `-max-duration`: upper bound on total test time (0 = unlimited).
- `-reverse`: measure download (server -> client).
- `-recv-wait`: server grace period after `SAMPLE_STOP` to capture in-flight
  packets.

## Throughput metrics

The server aggregates bytes into fixed 100ms intervals. For each sample, the
client derives interval rates and computes:

- Trimmed mean: drop the top 10% and bottom 10% of interval rates.
- P90 and P80 of interval rates.
- Sustained peak: maximum average rate over a rolling 1s window (10 intervals).

Each sample yields its own metrics; the final report averages the per-sample
values. The "achieved bandwidth" shown in the CLI is the trimmed-mean result.

## RTT and jitter

RTT is sampled in the background at `-rtt-rate` with lightweight TCP or UDP
ping probes. Reported RTT stats include mean/min/max and jitter (stdev).

## Loss / retransmits

- TCP: retransmits and segments sent are read from `TCP_INFO` on the sending
  side (server reports them in reverse mode).
- UDP: the server counts missing sequence numbers per sample and reports
  received/lost packets.

## Pacing and chunking

- TCP pacing uses `SO_MAX_PACING_RATE` and sets the send buffer to an estimated
  BDP (bandwidth * RTT / 8).
- UDP pacing uses a leaky-bucket limiter (bytes/sec).
- `-chunk-size` is the total size per write or datagram, including headers.
  TCP uses an 8-byte header; UDP uses a small type/sample/sequence header and
  is capped at 64KB.

## Usage

Start a server:

```
./build/bin/bwprobe -mode server
```

Run a TCP upload test:

```
./build/bin/bwprobe -mode client -target 10.0.0.1 -network tcp \
  -bandwidth 100Mbps -samples 10 -sample-bytes 20MB -wait 100ms
```

Run a UDP download test:

```
./build/bin/bwprobe -mode client -target 10.0.0.1 -network udp -reverse \
  -bandwidth 50Mbps -samples 10 -sample-bytes 10MB -wait 100ms
```

## Requirements

- Linux (uses `SO_MAX_PACING_RATE` and `TCP_INFO`).
- Go toolchain (see `go.mod`).
