# Documentation and code style

Use plain technical language, stable YAML examples, and small focused Go
interfaces. Runtime code must not perform background downloads or configure
host kernel policy. Use structured logs and redact bearer credentials.

The operator UI is dependency-free HTML/CSS/JavaScript with polling RPC
snapshots. Keep tables horizontally scrollable on narrow screens and avoid
client-side state beyond the current snapshot.
