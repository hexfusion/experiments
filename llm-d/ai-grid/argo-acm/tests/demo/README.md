# Routing demo scripts

Lab scripts behind work/llm-d/ai-grid/ROUTING-DEMO.md in the design repo. They assume the hub
context dagobah and the factory context site-d in ~/.kube/grid.kubeconfig, a MaaS key in
~/.config/grid-bench-maas-key, and a curl pod named demo-load in the factory llm-d namespace:

    kubectl --context site-d -n llm-d run demo-load --image=registry.access.redhat.com/ubi9/ubi-minimal:latest --restart=Never --command -- sleep 3600

five-minute-demo.sh LOG: baseline, 24 local streams at Portland, EPP scaled to zero, EPP back.
rebalance-demo.sh LOG: baseline, STREAMS local streams for 90 s, then 150 s untouched.
local-load.sh: runs inside demo-load, sh -s -- <streams> <seconds> <vllm addr>.
share-table.sh "<start>" "<end>": routed share per backend and Portland in flight, 10 s steps.
plot-run.py <start> <end> <t1> <t2> <out.png> [streams]: the two-panel chart on the slide.

Edit a script only while no run is using it: bash reads a script as it executes, and an edit
mid-run broke one timeline on 2026-10-08.
