# Diagrams

## Runtime flow

```text
client
  -> listener
  -> firewall / online policy
  -> route-local selector
  -> one pinned upstream
  -> audit + metrics observers
```

```text
fbmeasure sidecar -> unified health/RTT snapshot -> adaptive route selector
deployment timer  -> local MMDB replacement -> ReloadGeoIP RPC
```
