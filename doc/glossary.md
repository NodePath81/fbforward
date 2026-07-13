# Glossary

- **Flow**: one TCP stream or UDP mapping, pinned to one upstream.
- **Route**: a named listener target set with `static` or `adaptive` selection.
- **Static**: configured upstream selection without automatic fallback.
- **Adaptive**: route-local health/RTT selection with fallback among configured upstreams.
- **HealthSnapshot**: unified healthy/down/stale/unknown state and RTT.
- **Flow Context**: in-memory mapping from a backend tuple to the real client Flow.
- **Webhook**: optional asynchronous generic event delivery endpoint.
- **ReloadGeoIP**: local-only atomic reopening of configured MMDB files.
