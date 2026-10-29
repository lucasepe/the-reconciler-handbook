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

	"k8s.io/apimachinery/pkg/types"

	"github.com/lucasepe/the-reconciler-handbook/internal/provider"
)

var errResponseLost = errors.New("provider response lost")

const (
	testEngine = "postgres"
	testRegion = "eu-west"
	testSize   = "small"
)

func TestCreateParametersEquivalent(t *testing.T) {
	t.Parallel()

	original := provider.CreateParameters{Engine: testEngine, Size: testSize, Region: testRegion}
	if !original.Equivalent(original) {
		t.Fatal("identical semantic fields were not equivalent")
	}

	tests := map[string]provider.CreateParameters{
		"engine": {Engine: "mysql", Size: testSize, Region: testRegion},
		"size":   {Engine: testEngine, Size: "large", Region: testRegion},
		"region": {Engine: testEngine, Size: testSize, Region: "us-east"},
	}
	for name, changed := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if original.Equivalent(changed) {
				t.Fatalf("payload with changed %s was equivalent", name)
			}
		})
	}
}

func TestManagedDatabaseKey(t *testing.T) {
	t.Parallel()

	firstUID := types.UID("8a3ca21d-65f4-4a4f-86c6-a3d9f45865b4")
	first, err := provider.ManagedDatabaseKey(firstUID)
	if err != nil {
		t.Fatalf("derive first key: %v", err)
	}
	if want := "data.example.io/manageddatabase/" + string(firstUID); first != want {
		t.Fatalf("key = %q, want %q", first, want)
	}

	repeated, err := provider.ManagedDatabaseKey(firstUID)
	if err != nil {
		t.Fatalf("derive repeated key: %v", err)
	}
	if repeated != first {
		t.Fatalf("repeated key = %q, want %q", repeated, first)
	}

	second, err := provider.ManagedDatabaseKey(types.UID("e4b3dddf-9f7f-4cd2-a8f1-299ac379df5d"))
	if err != nil {
		t.Fatalf("derive recreated-object key: %v", err)
	}
	if second == first {
		t.Fatalf("different UIDs produced key %q", first)
	}

	if key, err := provider.ManagedDatabaseKey(""); key != "" || !errors.Is(err, provider.ErrEmptyUID) {
		t.Fatalf("empty UID returned key %q and error %v", key, err)
	}
}

func TestEquivalentReplayAfterLostResponseAllocatesOnce(t *testing.T) {
	t.Parallel()

	client := newScriptedProvider()
	client.loseNextResponse = true
	request := provider.CreateRequest{
		IdempotencyKey: "data.example.io/manageddatabase/uid-1",
		Parameters: provider.CreateParameters{
			Engine: testEngine,
			Size:   testSize,
			Region: testRegion,
		},
	}

	if _, err := client.EnsureDatabase(context.Background(), request); !errors.Is(err, errResponseLost) {
		t.Fatalf("first ensure error = %v, want lost response", err)
	}

	result, err := client.EnsureDatabase(context.Background(), request)
	if err != nil {
		t.Fatalf("replay equivalent request: %v", err)
	}
	if result.OperationID != "operation-1" {
		t.Fatalf("operation ID = %q, want operation-1", result.OperationID)
	}
	if client.allocations != 1 {
		t.Fatalf("allocations = %d, want 1", client.allocations)
	}
}

func TestMismatchedReplayConflictsWithoutChangingOriginal(t *testing.T) {
	t.Parallel()

	client := newScriptedProvider()
	request := provider.CreateRequest{
		IdempotencyKey: "data.example.io/manageddatabase/uid-2",
		Parameters: provider.CreateParameters{
			Engine: testEngine,
			Size:   testSize,
			Region: testRegion,
		},
	}
	original, err := client.EnsureDatabase(context.Background(), request)
	if err != nil {
		t.Fatalf("accept original request: %v", err)
	}

	changed := request
	changed.Parameters.Size = "large"
	result, err := client.EnsureDatabase(context.Background(), changed)
	if err == nil {
		t.Fatal("mismatched replay succeeded")
	}
	if result != (provider.CreateResult{}) {
		t.Fatalf("mismatched replay returned result %+v", result)
	}

	var conflict *provider.IdempotencyConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("mismatched replay error = %T, want *IdempotencyConflictError", err)
	}
	if conflict.Key != request.IdempotencyKey ||
		conflict.Accepted != request.Parameters ||
		conflict.Requested != changed.Parameters {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	if client.allocations != 1 {
		t.Fatalf("allocations after conflict = %d, want 1", client.allocations)
	}
	if got := client.bindings[request.IdempotencyKey].parameters; got != request.Parameters {
		t.Fatalf("accepted parameters changed to %+v", got)
	}

	client.lookupMisses = 1
	if _, err := client.LookupByKey(context.Background(), request.IdempotencyKey); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("lagging lookup error = %v, want ErrNotFound", err)
	}
	discovered, err := client.LookupByKey(context.Background(), request.IdempotencyKey)
	if err != nil {
		t.Fatalf("discover original result after lag: %v", err)
	}
	if discovered != original {
		t.Fatalf("discovered result = %+v, want %+v", discovered, original)
	}
}

// scriptedProvider models only the fictional case-study contract. It is not
// evidence of real-provider, transport, or observation-lag behavior.
type scriptedProvider struct {
	bindings         map[string]binding
	allocations      int
	loseNextResponse bool
	lookupMisses     int
}

type binding struct {
	parameters provider.CreateParameters
	result     provider.CreateResult
}

func newScriptedProvider() *scriptedProvider {
	return &scriptedProvider{bindings: make(map[string]binding)}
}

func (p *scriptedProvider) EnsureDatabase(
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
		return existing.result, nil
	}

	p.allocations++
	result := provider.CreateResult{OperationID: fmt.Sprintf("operation-%d", p.allocations)}
	p.bindings[request.IdempotencyKey] = binding{parameters: request.Parameters, result: result}
	if p.loseNextResponse {
		p.loseNextResponse = false
		return provider.CreateResult{}, errResponseLost
	}

	return result, nil
}

func (p *scriptedProvider) LookupByKey(
	_ context.Context,
	key string,
) (provider.CreateResult, error) {
	if p.lookupMisses > 0 {
		p.lookupMisses--
		return provider.CreateResult{}, provider.ErrNotFound
	}

	existing, ok := p.bindings[key]
	if !ok {
		return provider.CreateResult{}, provider.ErrNotFound
	}
	return existing.result, nil
}

var _ provider.Client = (*scriptedProvider)(nil)
