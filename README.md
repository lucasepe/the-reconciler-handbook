# The Reconciler's Handbook — Companion Code

Executable examples for *The Reconciler's Handbook: Designing, Reviewing, and
Owning Kubernetes Controllers* by Luca Sepe.

The book explains the design decisions. This repository provides optional,
focused experiments around the fictional `ManagedDatabase` API.

## Scope

Includes API types and a generated CRD, decision helpers, scripted provider
models, API-server experiments, and an isolated Kind infrastructure scenario.

This is not a complete database operator. The manager registers no
`ManagedDatabase` reconciler, and there is no concrete provider adapter or
resize implementation. Running it does not provision databases.

## Explore

- `api/v1alpha1/`: API types and API-server contract experiments.
- `internal/`: provider contracts, status, ownership, failure, queue, cache,
  and recovery examples.
- `config/`: generated manifests, sample requests, and test configuration.
- `cmd/`: manager scaffold.
- `test/e2e/`: isolated complete-cluster infrastructure experiment.

See [Testing](TESTING.md) for commands and prerequisites, and
[Experiment Evidence](docs/testing-evidence.md) for the claims and limits of
each experiment. Neither document requires the book to be understood.

## Run checks

Run commands from this module's root. The pinned Go toolchain is 1.26.8;
dependency versions are recorded in [go.mod](go.mod).

```sh
make test
make build
```

`make test` generates artifacts and obtains the pinned Kubernetes API-server
and etcd binaries for envtest. It does not run the Kind scenario.

The separate cluster experiment requires Docker, Kind 0.33.0, and kubectl:

```sh
make test-e2e
```

It creates an isolated cluster with a dedicated kubeconfig, refuses to reuse
an existing cluster of the same name, and cleans up the cluster it created.
It tests selected infrastructure behavior, not a deployed database lifecycle.

## License

Original project code is licensed under the [MIT License](LICENSE).
Dependencies retain their respective licenses.
