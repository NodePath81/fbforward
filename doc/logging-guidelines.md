# Logging guidelines

Use structured `slog` events with stable event names and component fields.
Never log bearer tokens, full request bodies, or client data that is not needed
for diagnosis. Forwarding-path logs must be non-blocking. Include Flow ID,
route, upstream, and close reason when available.
