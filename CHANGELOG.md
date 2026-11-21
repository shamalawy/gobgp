# Changelog

Versions follow [Semantic Versioning](https://semver.org).

## 0.1.0

First release.

- BGP-4 daemon on the GoBGP v4 engine: eBGP and iBGP over IPv4 and IPv6, route
  reflection, confederations, ADD-PATH, graceful restart, TCP MD5, TCP-AO, GTSM
  and BFD.
- Configuration in one YAML file, saved on every change.
- Policy: prefix, AS path and community sets, route maps, per neighbor or for
  all neighbors.
- RIB explorer over the Loc-RIB, Adj-RIB-In and Adj-RIB-Out with exact,
  longest, covering and covered prefix lookups and attribute filters.
- Route injection: static routes, and full tables from MRT TABLE_DUMP_V2, CSV
  and JSON with rate limiting and attribute rewriting.
- RPKI origin validation over RTR, BMP export, Prometheus metrics.
- REST API, server-sent events, optional GoBGP gRPC API.
- Web UI embedded in the binary.
- Linux binaries for amd64 and arm64, and a Docker image.
