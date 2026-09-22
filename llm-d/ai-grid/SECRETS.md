# Secrets (prerequisite — NOT committed)

The demo's mTLS/CA trust material is provisioned out of band (grid trust
bootstrap / enrollment). This package references these secrets by name but does
not carry their contents. Create them before `apply`, and teardown must NOT
delete them if you want the tunnel and site trust to survive a recreate.

## grid-system
| secret | type | purpose |
|--------|------|---------|
| `hub-ca` | Opaque | grid CA (hub) |
| `hub-identity` | Opaque | hub's own grid identity cert |
| `hub-swim-key` | Opaque | SWIM gossip key |
| `enrollment-ca` | Opaque | enrollment signer CA |

## ai-grid-demo
| secret | type | purpose |
|--------|------|---------|
| `grid-ca` | Opaque | grid CA the envoys trust |
| `grid-client-certs` | Opaque | grid peer client certs (SPIFFE) |
| `grid-envoy-server-tls` | kubernetes.io/tls | envoy server TLS |
