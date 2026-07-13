# Pre-phase-10 health and selection model

This archive records the retired model for historical context only. It is not
part of the configuration or runtime contract.

The retired implementation used protocol-specific TCP/UDP quality metrics,
composite scoring, ICMP reachability, fast-start probes, and global switching
hysteresis. Phase 10 replaced those paths with fbmeasure TCP/UDP RTT probes,
one unified health snapshot, and route-local selection.

Current behavior is documented in the active configuration and developer
guides. Do not copy configuration examples from this archive into new files.
