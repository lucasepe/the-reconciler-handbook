/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package provider_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lucasepe/the-reconciler-handbook/internal/provider"
)

var (
	errCreateResponseLost = errors.New("create response lost")
	errDeleteResponseLost = errors.New("delete response lost")
)

func TestLifecycleSurvivesAmbiguousCreateAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newScriptedLifecycleProvider()
	client.loseNextCreateResponse = true
	key := "data.example.io/manageddatabase/uid-lifecycle"
	request := provider.CreateRequest{
		IdempotencyKey: key,
		Parameters: provider.CreateParameters{
			Engine: testEngine,
			Size:   testSize,
			Region: testRegion,
		},
	}

	assertAmbiguousCreateRecovery(t, ctx, client, request)
	assertReadyWithDesiredDrift(t, ctx, client, request)
	assertAmbiguousDeleteRecovery(t, ctx, client, key)
}

func assertAmbiguousCreateRecovery(
	t *testing.T,
	ctx context.Context,
	client *scriptedLifecycleProvider,
	request provider.CreateRequest,
) {
	t.Helper()

	if _, err := client.EnsureDatabase(ctx, request); !errors.Is(err, errCreateResponseLost) {
		t.Fatalf("first create error = %v, want lost response", err)
	}
	if client.allocations != 1 {
		t.Fatalf("allocations after lost response = %d, want 1", client.allocations)
	}

	client.observationMisses = 2
	for attempt := range 2 {
		observation, err := client.ObserveDatabase(ctx, request.IdempotencyKey)
		if !errors.Is(err, provider.ErrNotFound) {
			t.Fatalf("lagging observation %d error = %v, want ErrNotFound", attempt+1, err)
		}
		if observation.State == provider.DatabaseStateAbsent {
			t.Fatalf("lagging observation %d reported authoritative absence", attempt+1)
		}
	}

	creating, err := client.ObserveDatabase(ctx, request.IdempotencyKey)
	if err != nil {
		t.Fatalf("observe accepted create: %v", err)
	}
	if creating.State != provider.DatabaseStateCreating ||
		creating.CreateOperationID != "create-operation-1" {
		t.Fatalf("creating observation = %+v", creating)
	}

	// Deletion may have been requested in Kubernetes, but this provider contract
	// does not cancel a pending create. Resolving the original operation does not
	// submit another create or claim that the database is absent.
	if client.allocations != 1 || client.deletionAcceptances != 0 {
		t.Fatalf(
			"pending-create counts = allocations:%d deletions:%d, want 1 and 0",
			client.allocations,
			client.deletionAcceptances,
		)
	}
}

func assertReadyWithDesiredDrift(
	t *testing.T,
	ctx context.Context,
	client *scriptedLifecycleProvider,
	request provider.CreateRequest,
) {
	t.Helper()

	client.advanceReady(request.IdempotencyKey)
	ready, err := client.ObserveDatabase(ctx, request.IdempotencyKey)
	if err != nil {
		t.Fatalf("observe ready database: %v", err)
	}
	if ready.State != provider.DatabaseStateReady ||
		ready.ProviderID != "database-1" ||
		ready.Properties != request.Parameters {
		t.Fatalf("ready observation = %+v", ready)
	}

	desired := request.Parameters
	desired.Size = "medium"
	if ready.Properties.Size == desired.Size {
		t.Fatal("fixture does not expose desired-versus-observed size difference")
	}
	if client.allocations != 1 || client.bindings[request.IdempotencyKey].parameters != request.Parameters {
		t.Fatalf("desired-size difference changed create binding: %+v", client.bindings[request.IdempotencyKey])
	}
}

func assertAmbiguousDeleteRecovery(
	t *testing.T,
	ctx context.Context,
	client *scriptedLifecycleProvider,
	key string,
) {
	t.Helper()

	client.loseNextDeleteResponse = true
	if _, err := client.EnsureDatabaseAbsent(ctx, key); !errors.Is(err, errDeleteResponseLost) {
		t.Fatalf("first ensure-absent error = %v, want lost response", err)
	}
	if client.deletionAcceptances != 1 {
		t.Fatalf("deletion acceptances after lost response = %d, want 1", client.deletionAcceptances)
	}

	replayedDelete, err := client.EnsureDatabaseAbsent(ctx, key)
	if err != nil {
		t.Fatalf("replay ensure-absent: %v", err)
	}
	if replayedDelete.OperationID != "delete-operation-1" {
		t.Fatalf("replayed delete operation = %q, want delete-operation-1", replayedDelete.OperationID)
	}
	if client.deletionAcceptances != 1 {
		t.Fatalf("deletion acceptances after replay = %d, want 1", client.deletionAcceptances)
	}

	client.observationMisses = 1
	if observation, err := client.ObserveDatabase(ctx, key); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("lagging deletion observation error = %v, want ErrNotFound", err)
	} else if observation.State == provider.DatabaseStateAbsent {
		t.Fatal("lagging deletion observation reported authoritative absence")
	}

	deleting, err := client.ObserveDatabase(ctx, key)
	if err != nil {
		t.Fatalf("observe deleting database: %v", err)
	}
	if deleting.State != provider.DatabaseStateDeleting ||
		deleting.DeleteOperationID != replayedDelete.OperationID {
		t.Fatalf("deleting observation = %+v", deleting)
	}

	client.advanceAbsent(key)
	absent, err := client.ObserveDatabase(ctx, key)
	if err != nil {
		t.Fatalf("observe authoritative absence: %v", err)
	}
	if absent.State != provider.DatabaseStateAbsent ||
		absent.DeleteOperationID != replayedDelete.OperationID {
		t.Fatalf("absent observation = %+v", absent)
	}

	replayedAfterAbsence, err := client.EnsureDatabaseAbsent(ctx, key)
	if err != nil {
		t.Fatalf("replay ensure-absent after absence: %v", err)
	}
	if replayedAfterAbsence != replayedDelete ||
		client.deletionAcceptances != 1 ||
		client.allocations != 1 ||
		client.bindings[key].state != provider.DatabaseStateAbsent {
		t.Fatalf(
			"replay after absence changed lifecycle: result=%+v allocations=%d deletions=%d binding=%+v",
			replayedAfterAbsence,
			client.allocations,
			client.deletionAcceptances,
			client.bindings[key],
		)
	}
}

// scriptedLifecycleProvider models only the accepted fictional lifecycle
// contract. Explicit test hooks advance provider state; the model is not
// evidence of transport, timing, cancellation, or real-provider behavior.
type scriptedLifecycleProvider struct {
	bindings               map[string]*lifecycleBinding
	allocations            int
	deletionAcceptances    int
	observationMisses      int
	loseNextCreateResponse bool
	loseNextDeleteResponse bool
}

type lifecycleBinding struct {
	parameters   provider.CreateParameters
	createResult provider.CreateResult
	deleteResult provider.DeleteResult
	state        provider.DatabaseState
	providerID   string
}

func newScriptedLifecycleProvider() *scriptedLifecycleProvider {
	return &scriptedLifecycleProvider{bindings: make(map[string]*lifecycleBinding)}
}

func (p *scriptedLifecycleProvider) EnsureDatabase(
	_ context.Context,
	request provider.CreateRequest,
) (provider.CreateResult, error) {
	if existing, ok := p.bindings[request.IdempotencyKey]; ok {
		if !existing.parameters.Equivalent(request.Parameters) {
			return provider.CreateResult{}, &provider.IdempotencyConflictError{
				Key:       request.IdempotencyKey,
				Accepted:  existing.parameters,
				Requested: request.Parameters,
			}
		}
		return existing.createResult, nil
	}

	p.allocations++
	result := provider.CreateResult{OperationID: fmt.Sprintf("create-operation-%d", p.allocations)}
	p.bindings[request.IdempotencyKey] = &lifecycleBinding{
		parameters:   request.Parameters,
		createResult: result,
		state:        provider.DatabaseStateCreating,
	}
	if p.loseNextCreateResponse {
		p.loseNextCreateResponse = false
		return provider.CreateResult{}, errCreateResponseLost
	}

	return result, nil
}

func (p *scriptedLifecycleProvider) LookupByKey(
	_ context.Context,
	key string,
) (provider.CreateResult, error) {
	binding, ok := p.bindings[key]
	if !ok {
		return provider.CreateResult{}, provider.ErrNotFound
	}
	return binding.createResult, nil
}

func (p *scriptedLifecycleProvider) ObserveDatabase(
	_ context.Context,
	key string,
) (provider.DatabaseObservation, error) {
	if p.observationMisses > 0 {
		p.observationMisses--
		return provider.DatabaseObservation{}, provider.ErrNotFound
	}

	binding, ok := p.bindings[key]
	if !ok {
		return provider.DatabaseObservation{}, provider.ErrNotFound
	}
	return provider.DatabaseObservation{
		State:             binding.state,
		CreateOperationID: binding.createResult.OperationID,
		DeleteOperationID: binding.deleteResult.OperationID,
		ProviderID:        binding.providerID,
		Properties:        binding.parameters,
	}, nil
}

func (p *scriptedLifecycleProvider) EnsureDatabaseAbsent(
	_ context.Context,
	key string,
) (provider.DeleteResult, error) {
	binding, ok := p.bindings[key]
	if !ok || binding.state == provider.DatabaseStateCreating {
		return provider.DeleteResult{}, provider.ErrNotFound
	}
	if binding.deleteResult.OperationID != "" {
		return binding.deleteResult, nil
	}

	p.deletionAcceptances++
	binding.deleteResult = provider.DeleteResult{
		OperationID: fmt.Sprintf("delete-operation-%d", p.deletionAcceptances),
	}
	binding.state = provider.DatabaseStateDeleting
	if p.loseNextDeleteResponse {
		p.loseNextDeleteResponse = false
		return provider.DeleteResult{}, errDeleteResponseLost
	}

	return binding.deleteResult, nil
}

func (p *scriptedLifecycleProvider) advanceReady(key string) {
	binding := p.bindings[key]
	binding.state = provider.DatabaseStateReady
	binding.providerID = fmt.Sprintf("database-%d", p.allocations)
	binding.createResult.ProviderID = binding.providerID
}

func (p *scriptedLifecycleProvider) advanceAbsent(key string) {
	binding := p.bindings[key]
	binding.state = provider.DatabaseStateAbsent
	binding.providerID = ""
	// The semantic properties remain in this test-only tombstone so retries can
	// be checked against the accepted lifecycle without resurrecting it.
}

var _ provider.LifecycleClient = (*scriptedLifecycleProvider)(nil)
