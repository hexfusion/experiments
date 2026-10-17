# Hub gateway handoff: certificates and configuration

Recorded 2026-10-17 from the live hub and site-d, after the site identities were renewed
and site-d moved to SPIFFE peer trust. This is what the hub grid gateway
behind the MaaS gateway needs to serve inference, and who supplies each piece today.
Open items are in [gaps](gaps.md).

## Path

1. A client calls the MaaS gateway (`maas-default-gateway`, Route
   `maas.apps.dagobah.hexfusion.local`) with a MaaS API key.
2. MaaS checks the key and the subscription, then calls `ExternalProvider/grid` at
   `grid-gateway.grid.svc:443` over TLS with its own provider key.
3. The hub grid gateway (namespace `grid`) checks the provider key, removes
   `Authorization`, and forwards over mutual TLS to site-d at `192.168.1.150:32403`.
4. The site-d provider gateway (`grid-provider`, namespace `grid-canary`) admits only
   `spiffe://grid.internal/site/hub` with organization `hub`, and forwards in plaintext
   to `qwen-canary:8000`.

## Certificates

| Hop | Presented by | Verified by | Material | Issued by | Expires | Created by |
|---|---|---|---|---|---|---|
| MaaS to hub | Hub listener, Secret `grid/grid-gateway-tls` (`grid-gateway.grid.svc`) | MaaS gateway, CA at `/etc/istio/grid-ca/service-ca.crt` from ConfigMap `openshift-ingress/grid-upstream-ca` | Service serving certificate | OpenShift service CA | 2028-10-14 | service-ca, from the Service annotation `service.beta.openshift.io/serving-cert-secret-name` |
| Hub to site-d, server | Site-d listener, `grid-canary/grid-canary-identity` (`CN=site-d, O=site-d`, `site-d.grid.internal`, `spiffe://grid.internal/site/site-d`) | Hub, SNI `site-d.grid.internal` against `grid/grid-canary-ca` | Site identity | Canary CA | 2027-01-15 | Hand |
| Hub to site-d, client | Hub, `grid/grid-canary-identity` (`CN=hub, O=hub`, `hub.grid.internal`, `spiffe://grid.internal/site/hub`) | Site-d, `client_ca` `grid-canary/grid-canary-ca`, `trusted_spiffe_ids` and `peer_identity_trust` organization `hub` | Site identity | Canary CA | 2027-01-15 | Hand |

The service CA rotates its serving certificates and the injected bundle on its own.
The canary CA and both identities do not: they are a [gap](gaps.md#certificates) until
enrollment issues and the operator renews them.

## Keys

| Secret | Contents | Used by |
|---|---|---|
| `models-as-a-service/grid-provider-key` | `api-key`: plaintext provider key | `ExternalProvider/grid` `auth.secretRef` |
| `grid/grid-maas-hop-validation` | `keys.yaml`: `sha256:` hash of the same key, `username: maas-front-door`, plus `policy.yaml` | Hub gateway, mounted at `/etc/praxis/maas-hop` |

Mint one key, write the plaintext to the first Secret and its hash to the second.

## Configuration

| File | Applied by | Holds |
|---|---|---|
| `sites/dagobah/gateway.yaml` | Argo, `dagobah-grid-gateway` | Chart pin, image digest, inline Praxis config, listener and grid TLS Secrets, hop key mount, NetworkPolicy |
| `sites/site-d/gateway.yaml` | Argo, `site-d-grid-gateway` | Provider role, SPIFFE peer trust, backend `10.43.125.89:8000`, NodePort, NetworkPolicy |
| `sites/dagobah/maas.yaml` | Hand | MaaS objects, the gateway CA patch, the RHCL timeout and memory |

These values must agree across files:

| Value | Set in | Must match |
|---|---|---|
| `grid-gateway.grid.svc` | `ExternalProvider/grid` `endpoint` | Hub release name and `fullnameOverride` `grid-gateway` in namespace `grid` |
| `/etc/istio/grid-ca/service-ca.crt` | `ExternalProvider/grid` `config.tls.caCertificates` | `gatewayCAPatch` mount path plus the ConfigMap key `service-ca.crt` |
| `192.168.1.150:32403` | Hub inline `load_balancer` endpoint | Site-d node address and the `grid-provider` NodePort |
| `site-d.grid.internal` | Hub inline `sni` | Site-d identity SAN |
| `spiffe://grid.internal/site/hub` | Site-d `peerTrust.spiffeIds` | Hub identity URI SAN, with `O=hub` |
| MaaS gateway pod labels | Hub `networkPolicy.from` | Namespace `openshift-ingress`, `gateway.networking.k8s.io/gateway-name: maas-default-gateway` |
| `192.168.1.0/24` | Site-d `networkPolicy.from` | Network the hub nodes reach site-d from |

## Order

1. Canary CA, then both site identities, as Secrets on each cluster.
2. The hop key pair.
3. Site-d provider gateway (Argo).
4. Hub gateway (Argo). Service-ca issues `grid-gateway-tls` once the Service exists.
5. The `grid-upstream-ca` ConfigMap and the MaaS gateway CA patch.
6. The MaaS objects in `sites/dagobah/maas.yaml`.
7. Run `tests/serving-payload/probe_live_grid.py`. It expects 401 for a missing or
   invalid key and 200 for four inference cases.

## Rotation

Renewal is manual today, which is a [gap](gaps.md#certificates). Under SPIFFE trust a
renewed identity keeps its SPIFFE ID and organization, so no peer configuration changes.

- Renewing a site identity: replace that site's `grid-canary-identity` and restart its
  gateway.
- Renewing the CA: put the old and new CA in `grid-canary-ca` on both clusters, restart
  both gateways, reissue both identities, then remove the old CA and restart again.
- Rotating the hop key means writing both halves together. The gateway re-reads
  `keys.yaml` every 5 seconds. MaaS reads `grid-provider-key` when it reconciles
  `ExternalProvider/grid`.

## After the chart change

Once the Grid chart carries `auth.secretKeyRef`, the hub drops the inline config:

- `gatewayConfig.auth.mode: api-key` with `secretKeyRef` naming
  `grid-maas-hop-validation` and key `keys.yaml`. The Secret no longer needs
  `policy.yaml`.
- `gridServing.enabled` and a serving config listing the site-d candidate, so the
  gateway routes with `grid_site_route`.
- `gatewayConfig.backends` for site-d, which renders the mutual TLS `load_balancer`
  cluster from the same identity and SNI.

The `/_grid/integration-check` preflight response has no chart equivalent and goes away.
