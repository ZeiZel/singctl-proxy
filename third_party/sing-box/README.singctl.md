# sing-box vendored source

This directory contains the pinned `github.com/sagernet/sing-box` v1.13.12
module used by singctl, retained with its upstream LICENSE and module metadata.
The local patch is intentionally limited to REALITY client compatibility
(`common/tls`, `option/tls.go`), V2Ray gRPC dial cancellation (`transport/v2raygrpc`) and lite gRPC error forwarding (`transport/v2raygrpclite`), and URLTest
error history/Clash delay diagnostics. REALITY authentication follows Xray's
plain-X25519 preference when that share is advertised and uses the hybrid share
only when plain X25519 is absent. Rebase this copy when upgrading the pinned
version and re-run the compatibility tests.
