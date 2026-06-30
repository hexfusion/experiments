# Hub DataProducer (POC mock)

Proves **Goal 1**: from the hub, with only a **manual list** of endpoints (no cross-cluster watch), consume
the per-`(cluster, pool)` load from **multiple** spoke EPPs.

`produce.py` reads the manual list, scrapes each spoke EPP `/metrics`, and prints the load map. With load on
`cluster-a / model-b` only:

```
cluster    pool     endpoint            queue  running     kv
cluster-a  model-a  10.89.0.211:9090     0.00     0.00   0.00
cluster-a  model-b  10.89.0.213:9090     4.00    16.00   0.00   <- isolated correctly
cluster-b  model-a  10.89.0.221:9090     0.00     0.00   0.00
...
```

## This is a MOCK, not the design

`produce.py` is **testing/mocking only** - it proves the consume-logic is trivial (~30 lines). It is NOT how
production should work. Two facts decide the real shape:

- The stock EPP consumes metrics via `metrics-data-source`, which scrapes its **InferencePool endpoints**, and
  those come from the **Pod reconciler** (`For(&corev1.Pod{})`) - **local pods only**. It cannot natively
  consume a remote endpoint declared in YAML.
- So bridging remote spoke metrics needs a **translation layer**: either the **proxy** (a local pod making the
  remote look local - no code) or a new EPP **DataSource plugin** that reads declared remote endpoints and
  scrapes them (**code**, and *that* is what makes "declare in YAML, it just works" real).

`produce.py` mocks that DataSource plugin. The native end-state is a GIE/EPP DataSource that consumes an
`InferencePoolImport` (the declarative YAML object that resolves the remote pool) - an upstream contribution,
not a config trick. Do not ship the Python.

## Manual list

The `(cluster, pool) -> metrics endpoint` list is hardcoded in `produce.py` (the "hub has a manual list, no
cross-cluster watch" MVP constraint). Productized, this list becomes a set of declarative resolving objects
(`InferencePoolImport`, or selector-less `Service` + `EndpointSlice`) instead of hardcoded IPs.
