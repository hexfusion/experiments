# gateway.hexfusion.io tunnel — DO NOT BREAK

External access is a HOST-SIDE cloudflared process (not in-cluster):

    cloudflared tunnel run gateway     # ~/.local/bin/cloudflared, tunnel id 4bf66a79-…

Config: ~/.cloudflared/config.yml
    gateway.hexfusion.io  ->  https://192.168.1.201:443   (site-a MaaS gateway)
    httpHostHeader: site-a.apps.dagobah.hexfusion.local

Teardown/recreate of THIS package touches only in-cluster grid-system /
ai-grid-demo objects. It does NOT touch:
  - the cloudflared host process (leave it running)
  - maas-site-a-gateway @ 192.168.1.201 (the tunnel's origin — keep the IP stable)
So the tunnel keeps working across a demo recreate.
