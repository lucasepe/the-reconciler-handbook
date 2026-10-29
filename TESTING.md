# Testing

Run these commands from the module root. Use the Go toolchain recorded in
[go.mod](go.mod) (currently 1.26.8). Initial runs may download dependencies and
test tools.

## Fast deterministic checks

No cluster or envtest binaries are needed for these packages:

```sh
go test -count=1 ./internal/provider ./internal/status \
  ./internal/ownership ./internal/queue ./internal/failure
```

These test local decisions, scripted provider behavior, and queue mechanics.
They do not verify a real provider.

## API-server experiments

```sh
make test
```

This target regenerates artifacts, formats Go files, runs vet, and obtains
the Kubernetes API-server and etcd binaries selected by the Makefile. It runs
the non-e2e packages, including envtest experiments, without using your cluster.

The envtest control plane does not include the usual cluster controllers.
Its privileged test client does not establish the deployed manager's permissions.

To run only the session-replacement experiment with the selected binaries:

```sh
make setup-envtest
export KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.37 --bin-dir bin -p path)"
go test -count=1 ./internal/recovery
```

This replaces test sessions while the API server and scripted backend remain
alive. It does not kill or restart a manager process.

## Isolated cluster experiment

Requires a running Docker engine, Kind 0.33.0, and kubectl:

```sh
make test-e2e
```

The target builds an image and creates a dedicated Kind cluster and kubeconfig.
It refuses to reuse an existing cluster or kubeconfig at the selected name/path.
Cleanup removes only the cluster created by that run when the cleanup trap
can execute. Do not substitute a development or production cluster.

This checks selected deployment, authorization, metrics-access,
garbage-collection, and leadership behavior, not a database control loop.

## Interpreting results

For each experiment's real components, controlled premises, supported claims,
and missing mechanisms, see [Experiment Evidence](docs/testing-evidence.md).
A passing suite supports those bounded claims, not production readiness.

