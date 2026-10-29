# Experiment Evidence

For commands and prerequisites, see [Testing](../TESTING.md). Paths below are
relative to the module root. This ledger records what the executable examples test and where their evidence
stops. A passing test supports only the claim exercised by its real components,
supplied inputs, and assertions. A scripted collaborator is part of the test's
premise; it is not independent evidence that Kubernetes or an external provider
has the same semantics.

The labels below are local descriptions, not a universal taxonomy:

- a **decision test** calls deterministic code with explicit inputs and checks
  its output or mutation;
- a **scripted model** is a test-only implementation whose hooks select
  observations and failures;
- an **API-server experiment** uses `envtest` to run the pinned Kubernetes API
  server and etcd;
- a **contract test** is a reusable behavior suite intended to run against each
  implementation of an interface;
- a **protocol or adapter test** crosses the serialization or transport
  boundary of a concrete provider adapter;
- an **end-to-end test** runs the deployed control loop and its dependencies in
  an environment chosen for the claim under test.

The repository currently contains decision tests, scripted models, direct
client-go queue tests, API-server experiments, one cross-system session-
replacement model, and one isolated complete-cluster infrastructure
experiment. It has no production controller, provider adapter, provider
contract suite, operating-system restart harness, or complete end-to-end
control loop.

## Decision and scripted tests

### `api/v1alpha1/manageddatabase_types_test.go`

- **Subject and real components:** Go API registration and generated deep copies
- **Supplied or substituted semantics:** In-memory objects and scheme
- **Supported claim:** The type registers under the expected GVK; tested maps and slices do not alias after deep copy
- **Not supported:** API admission, storage, conversion, controller behavior
- **Next stronger evidence:** API-server experiment for stored API behavior

### `internal/provider/provider_test.go`

- **Subject and real components:** Create parameter comparison, UID-derived key, and `Client` contract decisions
- **Supplied or substituted semantics:** An in-memory scripted provider chooses acceptance, one lost response, lookup misses, and conflict behavior
- **Supported claim:** Engine, size, and region are semantic equality fields; equivalent replay returns one operation; mismatched replay returns the typed conflict without changing the original binding
- **Not supported:** Provider protocol, serialization, transport ambiguity, authentication, persistence, restart durability, or a vendor's idempotency policy
- **Next stronger evidence:** Reusable contract suite against a concrete adapter, then protocol tests against a documented provider or faithful test server

### `internal/provider/lifecycle_test.go`

- **Subject and real components:** `LifecycleClient` decisions and the accepted fictional lifecycle
- **Supplied or substituted semantics:** Explicit hooks advance `Creating` to `Ready` and `Deleting` to `Absent`; counters inject lost responses and observation misses
- **Supported claim:** One scripted lifecycle survives the selected ambiguous responses; `ErrNotFound` remains distinct from `Absent`; desired-size drift does not alter the immutable create binding
- **Not supported:** Asynchronous execution, elapsed time, cancellation, network failure, provider consistency, durable tombstones, restart, multi-process use, or eventual liveness
- **Next stronger evidence:** Adapter contract and protocol tests; the session-replacement experiment described below

### `internal/status/conditions_test.go`

- **Subject and real components:** `SetReadyCondition` with real apimachinery condition helpers
- **Supplied or substituted semantics:** The caller supplies status, reason, message, generation, and therefore the claimed evidence
- **Supported claim:** One Ready condition is maintained; selected polarity, generation, no-op, and transition-time mechanics hold
- **Not supported:** Whether an observation is true, whether Ready was selected correctly, status-subresource persistence, or conflict recovery
- **Next stronger evidence:** API-server status experiment, then a reconciler decision test with explicit observations

### `internal/ownership/connection_secret_test.go`

- **Subject and real components:** Pure Secret metadata construction and exact ownership decision using Kubernetes API types and controller-reference helpers
- **Supplied or substituted semantics:** Objects exist only in memory; no client performs a write
- **Supported claim:** Deterministic identity and exact current-owner UID are required; unsafe candidates remain unchanged
- **Not supported:** Create races, authorization, admission, storage, garbage collection, Secret content, or watch delivery
- **Next stronger evidence:** API-server create/update experiment; a complete-cluster test for garbage collection

### `internal/queue/workqueue_test.go`

- **Subject and real components:** Real client-go typed queue and exponential failure rate limiter
- **Supplied or substituted semantics:** Direct calls choose exact interleavings and fixture delays; no delaying queue or worker runs
- **Supported claim:** Duplicate keys coalesce; a dirty in-flight key is delivered later; the configured limiter tracks and forgets one key as asserted
- **Not supported:** Wall-clock delay, controller worker behavior, fairness, watch delivery, production defaults, provider polling, or convergence
- **Next stronger evidence:** Worker test with controlled clock for scheduler behavior; end-to-end observation for deployed liveness

### `internal/queue/delaying_queue_test.go`

- **Subject and real components:** Real client-go typed delaying queue and Go `testing/synctest` virtual clock
- **Supplied or substituted semantics:** One key, an artificial 30-second delay, and explicit consumer scheduling inside the test bubble
- **Supported claim:** Under the pinned implementations, the key is unavailable one virtual nanosecond before its deadline and available at the deadline without waiting on wall time
- **Not supported:** Production timer precision, fairness, load, controller-runtime result mapping, queue persistence, provider polling, or post-restart delivery
- **Next stronger evidence:** Controller worker test for mapping and cancellation; deployed observation for production timing and load

### `internal/failure/policy_test.go`

- **Subject and real components:** Explicit failure wrapper and decision function
- **Supplied or substituted semantics:** Tests choose the class, safe message, wrapped cause, and provider state
- **Supported claim:** Approved classes map to approved dispositions; causes remain inspectable; public fixture text omits the sentinel token; pending states remain outside failure policy
- **Not supported:** Correct classification of real errors, queue execution, status writes, Event delivery, logging redaction, remediation, or eventual progress
- **Next stronger evidence:** Adapter error-mapping tests and reconciler tests; operational tests for reporting paths


These tests deliberately make hard-to-reach branches cheap and deterministic.
That control is their strength. The same control is also their boundary: a
counter or state-advance hook does not show that a remote service loses a
response, delays visibility, preserves a key across process replacement, or
eventually completes work.

## API-server experiments

The following tests use controller-runtime `envtest` with the pinned Kubernetes
1.37 API server and etcd. This is materially different from an object-store
fake client, but it is still not a complete Kubernetes cluster or an external
provider.

### `api/v1alpha1/manageddatabase_validation_test.go`

- **Real components:** Kubernetes API server, etcd, generated CRD, direct client
- **Controlled fixture:** Submitted `ManagedDatabase` requests
- **Supported claim:** Tested defaults, schema and CEL rejection, status separation, and generation behavior are observed on the pinned API server
- **Not supported:** Older stored-object migration, webhook behavior, provider validation, or realization of accepted size changes
- **Next stronger evidence:** Upgrade/migration tests and deployed controller tests

### `api/v1alpha1/manageddatabase_versioning_test.go`

- **Real components:** Kubernetes API server, etcd, temporary CRD versions
- **Controlled fixture:** A copied schema and temporary served/storage version under `None` conversion
- **Supported claim:** Both selected endpoints serve the fixture; stored-version bookkeeping blocks unsafe removal in the tested sequence
- **Not supported:** Conversion semantics, storage migration, rollback compatibility, or real client compatibility
- **Next stronger evidence:** Conversion and migration tests across released binaries

### `api/v1alpha1/manageddatabase_concurrency_test.go`

- **Real components:** Kubernetes API server, etcd, real resource-version checks
- **Controlled fixture:** A deterministic sequential interleaving of independent object copies
- **Supported claim:** Stale spec and status writes conflict; fresh recomputation can persist current status
- **Not supported:** Concurrent worker timing, fairness, multi-replica exclusion, provider effects, or a complete retry loop
- **Next stronger evidence:** Reconciler concurrency tests and multi-process integration tests

### `api/v1alpha1/manageddatabase_finalizer_test.go`

- **Real components:** Kubernetes API server, etcd, generated CRD
- **Controlled fixture:** Direct metadata writes and delete requests
- **Supported claim:** The tested finalizer keeps deletion visible; adding a finalizer after deletion begins is rejected; removal permits storage deletion
- **Not supported:** External cleanup, controller retry, finalizer RBAC, garbage collection, restart recovery, or abandonment
- **Next stronger evidence:** Reconciler cleanup tests; complete-cluster test where garbage collection matters

### `internal/cache/manageddatabase_visibility_test.go`

- **Real components:** Kubernetes API server, etcd, controller-runtime informer cache and clients
- **Controlled fixture:** A test-only transform holds one selected update before local storage
- **Supported claim:** During the controlled window, the cache can return the old revision while a direct client and write response expose the new revision; release permits convergence in this run
- **Not supported:** Typical latency, production watch interruption, relisting, partitions, load, restart, or multi-replica cache behavior
- **Next stronger evidence:** Controller integration and deterministic watch-failure tests

### `internal/cache/manageddatabase_index_watch_test.go`

- **Real components:** Kubernetes API server, etcd, controller-runtime client and cache, client-go informer and local index
- **Controlled fixture:** A test-only `spec.size` extractor and event handler select the update to observe
- **Supported claim:** In this run, a valid direct size update reached the informer at the write response's resource version; the configured exact-match cache index removed the object from `small` and returned it under `medium`
- **Not supported:** Server-side field selection, event-to-request mapping, queue delivery, exactly-once watch delivery, disconnection/relist recovery, controller convergence, or production index registration
- **Next stronger evidence:** Manager/controller integration for wiring; deterministic watch-failure tests; complete-cluster test for deployed behavior

### `internal/recovery/manageddatabase_restart_test.go`

- **Real components:** Kubernetes API server, etcd, generated CRD, newly constructed direct clients, and the public provider lifecycle interfaces
- **Controlled fixture:** A scripted durable provider backend, replaceable sessions, named lost responses, visibility lag, throttle, and explicit lifecycle advances
- **Supported claim:** Kubernetes checkpoints and one fictional provider binding survive replacement of all driver sessions; replay keeps one create and delete operation; uncertainty retains the finalizer until authoritative `Absent`
- **Not supported:** OS or manager restart, network reconnection, informer reconstruction, real provider durability or throttling, credentials, queue recovery, multi-replica behavior, or unconditional liveness
- **Next stronger evidence:** Subprocess restart around a concrete adapter; complete-cluster and provider-sandbox scenarios for omitted mechanisms


The default envtest control plane starts the API server and etcd. It does not
start `kube-controller-manager`, so these experiments cannot prove garbage
collection. The default client used by these tests is provisioned as an admin
in `system:masters`, so successful calls do not prove the production service
account's RBAC. The harness also has no fictional database provider.

## Isolated complete-cluster experiment

`test/e2e` runs only through `make test-e2e`. The target refuses to reuse a
pre-existing cluster name or kubeconfig and acquires an atomic per-name run
lock before setup. It creates a named Kind cluster with the published
Kubernetes 1.37.0 node-image digest, passes every subprocess a dedicated
kubeconfig, and removes only the cluster it created. A shell trap runs cleanup
after success, test failure, or an interrupt that still permits the runner to
execute.

The suite builds and records the local manager image identity, loads it into
the named cluster, and deploys through `config/e2e`. That overlay selects the
test image and `imagePullPolicy: Never`; it does not edit the production
Kustomize files. The scenario installs no cert-manager or Prometheus.

### The installed artifact enforces selected API rules

- **Real component and identity:** Kubernetes 1.37 API server and generated CRD; setup administrator submits requests
- **Observation:** Valid object accepted, omitted deletion policy stored as `Retain`, invalid engine rejected
- **Important boundary:** Repeats one deployment boundary; does not establish realization or resize

### The current manager artifact can run under its declared Pod restrictions

- **Real component and identity:** Scheduler, kubelet, Deployment, probes, and local image; manager service account
- **Observation:** Deployment available, one Pod `Running` and Ready, selected restricted fields and image identity present
- **Important boundary:** `/readyz` uses `healthz.Ping`; it proves process responsiveness, not cache, provider, or reconciliation readiness

### The deployed manager identity has the current scaffold permissions

- **Real component and identity:** Kubernetes authorizer queried by an administrator using service-account impersonation
- **Observation:** Lease get/update allowed; Secret create and `ManagedDatabase` update denied
- **Important boundary:** This is the current scaffold surface, not final controller RBAC; it does not make requests from a reconciler

### Metrics authn/authz distinguishes identities

- **Real component and identity:** Projected-token Pods, controller-runtime filter, TokenReview and SubjectAccessReview
- **Observation:** Unbound scraper receives 403; scraper bound to the installed metrics-reader role receives 200
- **Important boundary:** TLS verification is disabled for the scaffold certificate; no Prometheus, PKI, metric semantics, or alerting is tested

### A selected dependent is garbage collected

- **Real component and identity:** `kube-controller-manager`; administrator creates one empty Secret with the exact current same-namespace owner UID
- **Observation:** Owner and dependent become absent under bounded observation
- **Important boundary:** No Secret writer, credential data, collision race, external resource, finalizer, or production timing is tested

### The selected manager infrastructure changes Lease holder

- **Real component and identity:** Two manager Pods, controller-runtime/client-go Lease lock, Deployment controller
- **Observation:** Holder is a live Pod hostname plus UUID; deleting it changes holder and resource version, increments transitions, and replicas return to two
- **Important boundary:** No controller runnable exists, so this proves no exactly-once effect, reconcile continuity, fencing, or provider failover


Administrative setup and service-account behavior remain distinct throughout
the scenario. The administrator creates fixtures and uses impersonation to ask
the authorizer a question; that is not evidence that the manager made the same
request. Scraper tokens stay in their projected volumes. Commands, repository
files, test output, and failure diagnostics contain no bearer token.

## Why no Kubernetes fake client appears here

controller-runtime 0.25.1 documents its fake client as a simple object store.
It performs no OpenAPI validation, has subresource caveats, and does not
reproduce `Generation` and `ResourceVersion` well enough to use those fields as
API-server evidence. The fake supports useful focused features, but adding more
behavior to a local store would still make that behavior a test premise.

This repository therefore uses pure objects for pure decisions and envtest for
the selected API-server contracts. That is a project choice, not a claim that a
fake client is never useful.

## Choosing the next test

Escalate because a named mechanism is missing, not because a slower test sounds
more realistic:

| Missing mechanism | Appropriate next layer |
| --- | --- |
| Concrete provider request encoding, response decoding, and error mapping | Protocol or adapter test |
| One interface contract across multiple provider implementations | Reusable contract suite run against each implementation |
| API admission, status isolation, resource-version conflict, CRD serving | envtest API-server experiment |
| Process replacement between effect and checkpoint | Restart and deterministic-failure harness |
| Garbage collection, deployed RBAC, manager wiring, or controller startup | Isolated complete-cluster test |
| Real provider idempotency, consistency, authorization, or quota behavior | Provider sandbox or documented integration environment |
| User-visible convergence across the deployed control loop | End-to-end scenario with bounded observations |

No single row is automatically required for every behavior. The useful
question is: which untested mechanism could make the current conclusion false?
