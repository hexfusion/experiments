# Running real EPP without Kubernetes

Useful beyond this experiment: `llm-d-router`'s EPP can run outside a cluster. The config field
doc says so directly, on `DataLayerConfig.Discovery`: *"When set, the EPP bypasses Kubernetes CRD
reconcilers and relies entirely on the referenced plugin to enumerate and track inference
endpoints. This enables running the EPP without a Kubernetes cluster."*

    apiVersion: llm-d.ai/v1alpha1
    kind: EndpointPickerConfig
    plugins:
      - type: file-discovery
        parameters:
          path: /path/to/endpoints.yaml
      - type: single-profile-handler
      - type: max-score-picker
      - type: queue-scorer
    dataLayer:
      discovery:
        pluginRef: file-discovery
    schedulingProfiles:
      - name: default
        plugins:
          - pluginRef: queue-scorer
          - pluginRef: max-score-picker

Endpoints file:

    endpoints:
      - name: sim-a
        namespace: default
        address: 127.0.0.1
        port: "9301"

Run:

    epp --config-file epp-config.yaml --secure-serving=false --grpc-port=9002 \
        --pool-name=test --pool-namespace=default

## What was verified, and what was not

Verified at `llm-d-router` `f56f3bd9`, built with `GOTOOLCHAIN=auto` since go.mod requires 1.26.5:

- EPP starts, loads the config, instantiates every plugin, and serves ext_proc on 9002.
- The gateway in the parent directory completes a real ext_proc exchange with it.
- EPP returned an `ImmediateResponse` and the gateway propagated it as HTTP 503 carrying EPP's
  own message, which exercises the admission-control path end to end against the real binary.

Not verified: a successful routing decision. EPP answered `failed to find endpoint candidates`,
so the endpoints in the file were not becoming pickable candidates. Whether that needs additional
wiring, a readiness signal, or the metrics source to have scraped them first is unresolved.

Two environment notes. EPP still contacts a Kubernetes API at startup for the controller manager
even with file discovery, so a reachable API server is required; a stopped kind cluster can be
restarted with `podman start <cluster>-control-plane` without the sudo that creating one needs.
And plugin type names are not guessable: the queue scorer is `queue-scorer`, not
`queue-depth-scorer`. Grep the `*Type = "` constants.
