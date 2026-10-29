/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package recovery_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	data "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	"github.com/lucasepe/the-reconciler-handbook/internal/provider"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	errCreateResponseLost = errors.New("scripted create response lost")
	errDeleteResponseLost = errors.New("scripted delete response lost")
	errProviderThrottled  = errors.New("scripted provider throttle")
)

const (
	createOperationID = "create-operation-1"
	deleteOperationID = "delete-operation-1"
)

// TestManagedDatabaseRecoversFromSessionReplacement is a deterministic model
// of process-local state loss, not an operating-system process restart. The
// envtest API server and the providerBackend are the only durable collaborators.
func TestManagedDatabaseRecoversFromSessionReplacement(t *testing.T) {
	testEnvironment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	config, err := testEnvironment.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnvironment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("register core API: %v", err)
	}
	if err := data.AddToScheme(scheme); err != nil {
		t.Fatalf("register ManagedDatabase API: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	key := types.NamespacedName{Namespace: "restart-recovery", Name: "orders"}
	backend := newProviderBackend()

	bootstrap := newRecoveryDriver(t, config, scheme, backend, providerFaults{})
	bootstrap.createObjects(ctx, key)
	recoverAmbiguousCreate(t, ctx, config, scheme, backend, key)
	observeCreateProgress(t, ctx, config, scheme, backend, key)
	recoverAmbiguousDelete(t, ctx, config, scheme, backend, key)

	if backend.allocations != 1 || backend.deletionAcceptances != 1 {
		t.Fatalf("final provider counts = allocations:%d deletions:%d, want 1 and 1",
			backend.allocations, backend.deletionAcceptances)
	}
}

func recoverAmbiguousCreate(
	t *testing.T,
	ctx context.Context,
	config *rest.Config,
	scheme *runtime.Scheme,
	backend *providerBackend,
	key types.NamespacedName,
) {
	t.Helper()
	// Boundary 1: finalizer persisted, no provider create attempted. The next
	// phase receives no object pointer or provider result from this driver.
	driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{})
	driver.persistCleanupFinalizer(ctx, key)
	if backend.allocations != 0 {
		t.Fatalf("allocations before finalizer checkpoint = %d, want 0", backend.allocations)
	}

	// Boundary 2: provider accepts create, but its response is lost before any
	// operation identifier is written to Kubernetes.
	driver = newRecoveryDriver(t, config, scheme, backend, providerFaults{loseCreateResponse: true})
	if _, err := driver.ensureDatabase(ctx, key); !errors.Is(err, errCreateResponseLost) {
		t.Fatalf("ambiguous create error = %v, want lost response", err)
	}
	if backend.allocations != 1 {
		t.Fatalf("allocations after ambiguous create = %d, want 1", backend.allocations)
	}

	driver = newRecoveryDriver(t, config, scheme, backend, providerFaults{})
	driver.assertOperationCheckpoint(t, ctx, key, "")
	lookedUp, err := driver.provider.LookupByKey(ctx, driver.lifecycleKey(ctx, key))
	if err != nil || lookedUp.OperationID != createOperationID {
		t.Fatalf("lookup accepted create after replacement = %+v, error = %v", lookedUp, err)
	}
	result, err := driver.ensureDatabase(ctx, key)
	if err != nil {
		t.Fatalf("equivalent create replay: %v", err)
	}
	if result.OperationID != createOperationID || backend.allocations != 1 {
		t.Fatalf("create replay = %+v, allocations = %d", result, backend.allocations)
	}

	different := provider.CreateRequest{
		IdempotencyKey: driver.lifecycleKey(ctx, key),
		Parameters: provider.CreateParameters{
			Engine: "postgres",
			Size:   "medium",
			Region: "eu-west-1",
		},
	}
	_, err = driver.provider.EnsureDatabase(ctx, different)
	var conflict *provider.IdempotencyConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("different create replay error = %v, want IdempotencyConflictError", err)
	}
	if conflict.Accepted.Size != "small" || conflict.Requested.Size != "medium" || backend.allocations != 1 {
		t.Fatalf("different replay changed binding: conflict=%+v allocations=%d", conflict, backend.allocations)
	}
	driver.checkpointCreate(ctx, key, result)
}

func observeCreateProgress(
	t *testing.T,
	ctx context.Context,
	config *rest.Config,
	scheme *runtime.Scheme,
	backend *providerBackend,
	key types.NamespacedName,
) {
	t.Helper()
	// Boundary 3: a missing observation and a transient throttle are uncertainty,
	// not authoritative absence. Each observation starts with another new client,
	// provider session, and driver.
	{
		driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{})
		driver.assertFinalizerPresent(t, ctx, key)
		observation, err := driver.observeDatabase(ctx, key)
		if !errors.Is(err, provider.ErrNotFound) || observation.State == provider.DatabaseStateAbsent {
			t.Fatalf("lagging observation = %+v, error = %v", observation, err)
		}
	}

	{
		driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{throttleObservation: true})
		driver.assertFinalizerPresent(t, ctx, key)
		observation, err := driver.observeDatabase(ctx, key)
		if !errors.Is(err, errProviderThrottled) || observation.State == provider.DatabaseStateAbsent {
			t.Fatalf("throttled observation = %+v, error = %v", observation, err)
		}
	}

	backend.publishCreating()
	{
		driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{})
		observation, err := driver.observeDatabase(ctx, key)
		if err != nil || observation.State != provider.DatabaseStateCreating ||
			observation.CreateOperationID != createOperationID {
			t.Fatalf("creating observation = %+v, error = %v", observation, err)
		}
		driver.assertFinalizerPresent(t, ctx, key)
	}

	backend.advanceReady()
	{
		driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{})
		observation, err := driver.observeDatabase(ctx, key)
		if err != nil || observation.State != provider.DatabaseStateReady ||
			observation.CreateOperationID != createOperationID {
			t.Fatalf("ready observation = %+v, error = %v", observation, err)
		}
		driver.requestDeletion(ctx, key)
	}
}

func recoverAmbiguousDelete(
	t *testing.T,
	ctx context.Context,
	config *rest.Config,
	scheme *runtime.Scheme,
	backend *providerBackend,
	key types.NamespacedName,
) {
	t.Helper()
	// Boundary 4: provider accepts deletion, but the response is lost. A new
	// session replays by the freshly derived UID key and sees the same operation.
	driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{loseDeleteResponse: true})
	if _, err := driver.ensureDatabaseAbsent(ctx, key); !errors.Is(err, errDeleteResponseLost) {
		t.Fatalf("ambiguous delete error = %v, want lost response", err)
	}
	if backend.deletionAcceptances != 1 {
		t.Fatalf("delete acceptances after ambiguous response = %d, want 1", backend.deletionAcceptances)
	}

	{
		driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{throttleObservation: true})
		driver.assertFinalizerPresent(t, ctx, key)
		observation, err := driver.observeDatabase(ctx, key)
		if !errors.Is(err, errProviderThrottled) || observation.State == provider.DatabaseStateAbsent {
			t.Fatalf("throttled deletion observation = %+v, error = %v", observation, err)
		}
	}

	{
		driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{})
		driver.assertFinalizerPresent(t, ctx, key)
		deletion, err := driver.ensureDatabaseAbsent(ctx, key)
		if err != nil {
			t.Fatalf("equivalent delete replay: %v", err)
		}
		if deletion.OperationID != deleteOperationID || backend.deletionAcceptances != 1 {
			t.Fatalf("delete replay = %+v, acceptances = %d", deletion, backend.deletionAcceptances)
		}
		observation, err := driver.observeDatabase(ctx, key)
		if err != nil || observation.State != provider.DatabaseStateDeleting {
			t.Fatalf("deleting observation = %+v, error = %v", observation, err)
		}
		driver.assertFinalizerPresent(t, ctx, key)
	}

	backend.advanceAbsent()
	{
		driver := newRecoveryDriver(t, config, scheme, backend, providerFaults{})
		observation, err := driver.observeDatabase(ctx, key)
		if err != nil || observation.State != provider.DatabaseStateAbsent {
			t.Fatalf("absent observation = %+v, error = %v", observation, err)
		}
		driver.removeCleanupFinalizer(ctx, key)
		driver.waitForNotFound(t, ctx, key)
	}
}

// recoveryDriver contains only replaceable process-local collaborators. Tests
// construct it from the two durable fixtures rather than copying an old driver.
type recoveryDriver struct {
	t          *testing.T
	kubernetes client.Client
	provider   provider.LifecycleClient
}

func newRecoveryDriver(
	t *testing.T,
	config *rest.Config,
	scheme *runtime.Scheme,
	backend *providerBackend,
	faults providerFaults,
) *recoveryDriver {
	t.Helper()

	kubernetesClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("create fresh Kubernetes client: %v", err)
	}
	return &recoveryDriver{
		t:          t,
		kubernetes: kubernetesClient,
		provider:   &providerSession{backend: backend, faults: faults},
	}
}

func (d *recoveryDriver) createObjects(ctx context.Context, key types.NamespacedName) {
	d.t.Helper()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: key.Namespace}}
	if err := d.kubernetes.Create(ctx, namespace); err != nil {
		d.t.Fatalf("create namespace: %v", err)
	}
	database := &data.ManagedDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec: data.ManagedDatabaseSpec{
			Engine:         "postgres",
			Size:           "small",
			Region:         "eu-west-1",
			DeletionPolicy: data.DeletionPolicyDelete,
		},
	}
	if err := d.kubernetes.Create(ctx, database); err != nil {
		d.t.Fatalf("create ManagedDatabase: %v", err)
	}
}

func (d *recoveryDriver) persistCleanupFinalizer(ctx context.Context, key types.NamespacedName) {
	d.t.Helper()
	database := d.read(ctx, key)
	controllerutil.AddFinalizer(database, data.ManagedDatabaseProviderCleanupFinalizer)
	if err := d.kubernetes.Update(ctx, database); err != nil {
		d.t.Fatalf("persist cleanup finalizer: %v", err)
	}
}

func (d *recoveryDriver) ensureDatabase(
	ctx context.Context,
	key types.NamespacedName,
) (provider.CreateResult, error) {
	d.t.Helper()
	database := d.read(ctx, key)
	lifecycleKey, err := provider.ManagedDatabaseKey(database.UID)
	if err != nil {
		return provider.CreateResult{}, err
	}
	return d.provider.EnsureDatabase(ctx, provider.CreateRequest{
		IdempotencyKey: lifecycleKey,
		Parameters: provider.CreateParameters{
			Engine: database.Spec.Engine,
			Size:   database.Spec.Size,
			Region: database.Spec.Region,
		},
	})
}

func (d *recoveryDriver) checkpointCreate(
	ctx context.Context,
	key types.NamespacedName,
	result provider.CreateResult,
) {
	d.t.Helper()
	database := d.read(ctx, key)
	database.Status.OperationID = result.OperationID
	database.Status.ProviderID = result.ProviderID
	if err := d.kubernetes.Status().Update(ctx, database); err != nil {
		d.t.Fatalf("checkpoint create operation: %v", err)
	}
}

func (d *recoveryDriver) observeDatabase(
	ctx context.Context,
	key types.NamespacedName,
) (provider.DatabaseObservation, error) {
	d.t.Helper()
	return d.provider.ObserveDatabase(ctx, d.lifecycleKey(ctx, key))
}

func (d *recoveryDriver) ensureDatabaseAbsent(
	ctx context.Context,
	key types.NamespacedName,
) (provider.DeleteResult, error) {
	d.t.Helper()
	return d.provider.EnsureDatabaseAbsent(ctx, d.lifecycleKey(ctx, key))
}

func (d *recoveryDriver) lifecycleKey(
	ctx context.Context,
	key types.NamespacedName,
) string {
	d.t.Helper()
	database := d.read(ctx, key)
	lifecycleKey, err := provider.ManagedDatabaseKey(database.UID)
	if err != nil {
		d.t.Fatalf("derive provider lifecycle key: %v", err)
	}
	return lifecycleKey
}

func (d *recoveryDriver) requestDeletion(ctx context.Context, key types.NamespacedName) {
	d.t.Helper()
	database := d.read(ctx, key)
	if err := d.kubernetes.Delete(ctx, database); err != nil {
		d.t.Fatalf("request ManagedDatabase deletion: %v", err)
	}
}

func (d *recoveryDriver) removeCleanupFinalizer(ctx context.Context, key types.NamespacedName) {
	d.t.Helper()
	database := d.read(ctx, key)
	controllerutil.RemoveFinalizer(database, data.ManagedDatabaseProviderCleanupFinalizer)
	if err := d.kubernetes.Update(ctx, database); err != nil {
		d.t.Fatalf("remove cleanup finalizer: %v", err)
	}
}

func (d *recoveryDriver) assertFinalizerPresent(
	t *testing.T,
	ctx context.Context,
	key types.NamespacedName,
) {
	t.Helper()
	database := d.read(ctx, key)
	if !controllerutil.ContainsFinalizer(database, data.ManagedDatabaseProviderCleanupFinalizer) {
		t.Fatalf("cleanup finalizer absent in provider state %q", d.providerState())
	}
}

func (d *recoveryDriver) assertOperationCheckpoint(
	t *testing.T,
	ctx context.Context,
	key types.NamespacedName,
	want string,
) {
	t.Helper()
	if got := d.read(ctx, key).Status.OperationID; got != want {
		t.Fatalf("status.operationID = %q, want %q", got, want)
	}
}

func (d *recoveryDriver) waitForNotFound(
	t *testing.T,
	ctx context.Context,
	key types.NamespacedName,
) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			err := d.kubernetes.Get(ctx, key, &data.ManagedDatabase{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}); err != nil {
		t.Fatalf("wait for ManagedDatabase removal: %v", err)
	}
}

func (d *recoveryDriver) read(ctx context.Context, key types.NamespacedName) *data.ManagedDatabase {
	d.t.Helper()
	database := &data.ManagedDatabase{}
	if err := d.kubernetes.Get(ctx, key, database); err != nil {
		d.t.Fatalf("read current ManagedDatabase: %v", err)
	}
	return database
}

func (d *recoveryDriver) providerState() provider.DatabaseState {
	if session, ok := d.provider.(*providerSession); ok {
		if binding := session.backend.onlyBinding(); binding != nil {
			return binding.state
		}
	}
	return ""
}

// providerBackend is the scripted external system. Unlike providerSession, it
// survives replacement and owns the immutable binding and operation identities.
type providerBackend struct {
	bindings            map[string]*providerBinding
	allocations         int
	deletionAcceptances int
	visible             bool
}

type providerBinding struct {
	parameters   provider.CreateParameters
	createResult provider.CreateResult
	deleteResult provider.DeleteResult
	state        provider.DatabaseState
}

func newProviderBackend() *providerBackend {
	return &providerBackend{bindings: make(map[string]*providerBinding)}
}

func (b *providerBackend) publishCreating() {
	b.visible = true
}

func (b *providerBackend) advanceReady() {
	b.visible = true
	binding := b.onlyBinding()
	binding.state = provider.DatabaseStateReady
	binding.createResult.ProviderID = "database-1"
}

func (b *providerBackend) advanceAbsent() {
	b.visible = true
	b.onlyBinding().state = provider.DatabaseStateAbsent
}

func (b *providerBackend) onlyBinding() *providerBinding {
	for _, binding := range b.bindings {
		return binding
	}
	return nil
}

// providerSession owns only volatile transport outcomes. A replacement gets a
// new fault script and can recover only by consulting providerBackend.
type providerSession struct {
	backend *providerBackend
	faults  providerFaults
}

type providerFaults struct {
	loseCreateResponse  bool
	loseDeleteResponse  bool
	throttleObservation bool
}

func (s *providerSession) EnsureDatabase(
	_ context.Context,
	request provider.CreateRequest,
) (provider.CreateResult, error) {
	if binding, ok := s.backend.bindings[request.IdempotencyKey]; ok {
		if !binding.parameters.Equivalent(request.Parameters) {
			return provider.CreateResult{}, &provider.IdempotencyConflictError{
				Key:       request.IdempotencyKey,
				Accepted:  binding.parameters,
				Requested: request.Parameters,
			}
		}
		return binding.createResult, nil
	}

	s.backend.allocations++
	s.backend.bindings[request.IdempotencyKey] = &providerBinding{
		parameters:   request.Parameters,
		createResult: provider.CreateResult{OperationID: createOperationID},
		state:        provider.DatabaseStateCreating,
	}
	if s.faults.loseCreateResponse {
		s.faults.loseCreateResponse = false
		return provider.CreateResult{}, errCreateResponseLost
	}
	return s.backend.bindings[request.IdempotencyKey].createResult, nil
}

func (s *providerSession) LookupByKey(
	_ context.Context,
	key string,
) (provider.CreateResult, error) {
	binding, ok := s.backend.bindings[key]
	if !ok {
		return provider.CreateResult{}, provider.ErrNotFound
	}
	return binding.createResult, nil
}

func (s *providerSession) ObserveDatabase(
	_ context.Context,
	key string,
) (provider.DatabaseObservation, error) {
	if s.faults.throttleObservation {
		s.faults.throttleObservation = false
		return provider.DatabaseObservation{}, errProviderThrottled
	}
	binding, ok := s.backend.bindings[key]
	if !ok || !s.backend.visible {
		return provider.DatabaseObservation{}, provider.ErrNotFound
	}
	return provider.DatabaseObservation{
		State:             binding.state,
		CreateOperationID: binding.createResult.OperationID,
		DeleteOperationID: binding.deleteResult.OperationID,
		ProviderID:        binding.createResult.ProviderID,
		Properties:        binding.parameters,
	}, nil
}

func (s *providerSession) EnsureDatabaseAbsent(
	_ context.Context,
	key string,
) (provider.DeleteResult, error) {
	binding, ok := s.backend.bindings[key]
	if !ok || binding.state == provider.DatabaseStateCreating {
		return provider.DeleteResult{}, provider.ErrNotFound
	}
	if binding.deleteResult.OperationID != "" {
		return binding.deleteResult, nil
	}

	s.backend.deletionAcceptances++
	binding.deleteResult = provider.DeleteResult{OperationID: deleteOperationID}
	binding.state = provider.DatabaseStateDeleting
	if s.faults.loseDeleteResponse {
		s.faults.loseDeleteResponse = false
		return provider.DeleteResult{}, errDeleteResponseLost
	}
	return binding.deleteResult, nil
}

var _ provider.LifecycleClient = (*providerSession)(nil)
