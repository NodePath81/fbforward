# fbnotify documentation

This directory is the canonical home for current fbnotify documentation.

fbnotify is a standalone Cloudflare Worker notification bridge with a built-in
operator UI and admin API. The current implementation provides:

- authenticated event ingress at `POST /v1/events`
- provider-target management for `webhook`, `pushover`, and `capture`
- operator-configured routing to one or more targets
- operator-token and node-token management
- a built-in capture inbox for deterministic testing

Use these documents by purpose:

- [user-guide.md](user-guide.md): deployment, operation, UI workflows, and
  troubleshooting
- [api.md](api.md): HTTP route reference, auth/session model, event-ingress
  contract, and response shapes

Legacy top-level docs such as
[../user-guide-fbnotify.md](../user-guide-fbnotify.md) remain as short
redirect stubs so existing links can stay stable.
