# Acceptance probes

This directory contains executable checks and their inputs. Write results to a temporary
output directory, review them for credentials, then save the selected results under
`../evidence/<date>/`. Source patches and image build recipes live in `../payload/patches/`.

- `maas-extproc/`: isolated ExternalModel streaming reproduction; use `config/dagobah.yaml`.
- `serving-payload/`: runtime and MaaS/Grid probes.
- `gateway-base-path/`: standalone runtime prefix handling against mock backends.

A mock-backend test does not prove generated Grid routing or protected KServe inference.
Do not recursively apply this directory to a cluster.
