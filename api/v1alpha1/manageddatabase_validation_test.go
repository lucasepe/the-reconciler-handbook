/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package v1alpha1

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestManagedDatabaseAPIContract(t *testing.T) {
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
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("register ManagedDatabase API: %v", err)
	}

	kubernetesClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("create Kubernetes client: %v", err)
	}

	ctx := context.Background()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "payments"}}
	if err := kubernetesClient.Create(ctx, namespace); err != nil {
		t.Fatalf("create test namespace: %v", err)
	}

	t.Run("validates explicit intent", func(t *testing.T) {
		testIntentValidation(t, ctx, kubernetesClient, namespace.Name)
	})

	t.Run("enforces the update contract", func(t *testing.T) {
		testIntentUpdates(t, ctx, kubernetesClient, namespace.Name)
	})

	t.Run("separates status and validates observed generation", func(t *testing.T) {
		object := newManagedDatabase("status-contract", namespace.Name, ManagedDatabaseSpec{
			Engine: testEnginePostgres,
			Size:   testSizeSmall,
			Region: testRegionEUWest,
		})
		object.Status.ProviderID = "ignored-on-create"
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create ManagedDatabase with status: %v", err)
		}

		stored := &ManagedDatabase{}
		key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
		if err := kubernetesClient.Get(ctx, key, stored); err != nil {
			t.Fatalf("read ManagedDatabase: %v", err)
		}
		if stored.Status.ProviderID != "" {
			t.Fatalf("main-resource create persisted status %q", stored.Status.ProviderID)
		}

		stored.Status.ObservedGeneration = -1
		if err := kubernetesClient.Status().Update(ctx, stored); !apierrors.IsInvalid(err) {
			t.Fatalf("write negative observedGeneration: got %v, want Invalid", err)
		}

		if err := kubernetesClient.Get(ctx, key, stored); err != nil {
			t.Fatalf("read after rejected status: %v", err)
		}
		stored.Status.ProviderID = "db-8f2a"
		stored.Status.ObservedGeneration = stored.Generation
		if err := kubernetesClient.Status().Update(ctx, stored); err != nil {
			t.Fatalf("write valid status: %v", err)
		}
	})

	t.Run("tracks desired and observed generations separately", func(t *testing.T) {
		testGenerationBoundary(t, ctx, kubernetesClient, namespace.Name)
	})
}

func testIntentValidation(
	t *testing.T,
	ctx context.Context,
	kubernetesClient client.Client,
	namespace string,
) {
	t.Helper()

	validSpec := ManagedDatabaseSpec{
		Engine:         testEnginePostgres,
		Size:           testSizeSmall,
		Region:         testRegionEUWest,
		DeletionPolicy: DeletionPolicyRetain,
	}

	t.Run("accepts and preserves a complete spec", func(t *testing.T) {
		object := newManagedDatabase("complete-spec", namespace, validSpec)
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create complete ManagedDatabase: %v", err)
		}

		stored := &ManagedDatabase{}
		key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
		if err := kubernetesClient.Get(ctx, key, stored); err != nil {
			t.Fatalf("read complete ManagedDatabase: %v", err)
		}
		if stored.Spec != validSpec {
			t.Fatalf("stored spec = %+v, want %+v", stored.Spec, validSpec)
		}
	})

	t.Run("defaults an omitted deletion policy to Retain", func(t *testing.T) {
		object := newManagedDatabase("default-retention", namespace, ManagedDatabaseSpec{
			Engine: testEnginePostgres,
			Size:   testSizeSmall,
			Region: testRegionEUWest,
		})
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create ManagedDatabase without deletion policy: %v", err)
		}
		if object.Spec.DeletionPolicy != DeletionPolicyRetain {
			t.Fatalf("defaulted deletion policy = %q, want %q", object.Spec.DeletionPolicy, DeletionPolicyRetain)
		}
	})

	for _, policy := range []DeletionPolicy{DeletionPolicyRetain, DeletionPolicyDelete} {
		t.Run("accepts deletion policy "+string(policy), func(t *testing.T) {
			spec := validSpec
			spec.DeletionPolicy = policy
			object := newManagedDatabase("policy-"+strings.ToLower(string(policy)), namespace, spec)
			if err := kubernetesClient.Create(ctx, object); err != nil {
				t.Fatalf("create ManagedDatabase with deletion policy %q: %v", policy, err)
			}
		})
	}

	t.Run("rejects an unknown deletion policy", func(t *testing.T) {
		spec := validSpec
		spec.DeletionPolicy = "Destroy"
		object := newManagedDatabase("invalid-policy", namespace, spec)
		if err := kubernetesClient.Create(ctx, object); !apierrors.IsInvalid(err) {
			t.Fatalf("create with unknown deletion policy: got %v, want Invalid", err)
		}
	})

	for _, missingField := range []string{"engine", "size", "region"} {
		t.Run("requires "+missingField, func(t *testing.T) {
			spec := map[string]any{
				"engine": testEnginePostgres,
				"size":   testSizeSmall,
				"region": testRegionEUWest,
			}
			delete(spec, missingField)
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "data.example.io/v1alpha1",
				"kind":       "ManagedDatabase",
				"metadata": map[string]any{
					"name":      "missing-" + missingField,
					"namespace": namespace,
				},
				"spec": spec,
			}}
			if err := kubernetesClient.Create(ctx, object); !apierrors.IsInvalid(err) {
				t.Fatalf("create without %s: got %v, want Invalid", missingField, err)
			}
		})
	}

	for name, spec := range map[string]ManagedDatabaseSpec{
		"invalid-engine": {Engine: "mysql", Size: testSizeSmall, Region: testRegionEUWest},
		"invalid-size":   {Engine: testEnginePostgres, Size: "enormous", Region: testRegionEUWest},
		"invalid-region": {Engine: testEnginePostgres, Size: testSizeSmall, Region: "EU West"},
	} {
		t.Run(name, func(t *testing.T) {
			object := newManagedDatabase(name, namespace, spec)
			if err := kubernetesClient.Create(ctx, object); !apierrors.IsInvalid(err) {
				t.Fatalf("create invalid ManagedDatabase: got %v, want Invalid", err)
			}
		})
	}

	t.Run("keeps the derived connection Secret name valid", func(t *testing.T) {
		object := newManagedDatabase(strings.Repeat("a", 242), namespace, validSpec)
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create ManagedDatabase with 242-character name: %v", err)
		}

		object = newManagedDatabase(strings.Repeat("a", 243), namespace, validSpec)
		if err := kubernetesClient.Create(ctx, object); !apierrors.IsInvalid(err) {
			t.Fatalf("create ManagedDatabase with 243-character name: got %v, want Invalid", err)
		}
	})
}

func testIntentUpdates(
	t *testing.T,
	ctx context.Context,
	kubernetesClient client.Client,
	namespace string,
) {
	t.Helper()

	object := newManagedDatabase("intent-updates", namespace, ManagedDatabaseSpec{
		Engine:         testEnginePostgres,
		Size:           testSizeSmall,
		Region:         testRegionEUWest,
		DeletionPolicy: DeletionPolicyDelete,
	})
	if err := kubernetesClient.Create(ctx, object); err != nil {
		t.Fatalf("create valid ManagedDatabase: %v", err)
	}

	object.Spec.Size = testSizeMedium
	if err := kubernetesClient.Update(ctx, object); err != nil {
		t.Fatalf("update mutable size: %v", err)
	}

	key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
	stored := &ManagedDatabase{}
	if err := kubernetesClient.Get(ctx, key, stored); err != nil {
		t.Fatalf("read after size update: %v", err)
	}
	if stored.Spec.Size != testSizeMedium {
		t.Fatalf("stored size = %q, want medium", stored.Spec.Size)
	}

	stored.Spec.Region = "us-east"
	if err := kubernetesClient.Update(ctx, stored); !apierrors.IsInvalid(err) {
		t.Fatalf("update immutable region: got %v, want Invalid", err)
	}

	if err := kubernetesClient.Get(ctx, key, stored); err != nil {
		t.Fatalf("read before engine update: %v", err)
	}
	stored.Spec.Engine = "mysql"
	// This update violates both the one-value enum and the transition rule. Its
	// rejection does not isolate which of those published constraints fired.
	if err := kubernetesClient.Update(ctx, stored); !apierrors.IsInvalid(err) {
		t.Fatalf("update immutable engine: got %v, want Invalid", err)
	}

	if err := kubernetesClient.Get(ctx, key, stored); err != nil {
		t.Fatalf("read before deletion policy update: %v", err)
	}
	stored.Spec.DeletionPolicy = DeletionPolicyRetain
	if err := kubernetesClient.Update(ctx, stored); !apierrors.IsInvalid(err) {
		t.Fatalf("update immutable deletion policy: got %v, want Invalid", err)
	}
}

func testGenerationBoundary(
	t *testing.T,
	ctx context.Context,
	kubernetesClient client.Client,
	namespace string,
) {
	t.Helper()

	object := newManagedDatabase("generation-boundary", namespace, ManagedDatabaseSpec{
		Engine: testEnginePostgres,
		Size:   testSizeSmall,
		Region: testRegionEUWest,
	})
	if err := kubernetesClient.Create(ctx, object); err != nil {
		t.Fatalf("create ManagedDatabase: %v", err)
	}
	if object.Generation != 1 {
		t.Fatalf("initial generation = %d, want 1", object.Generation)
	}

	initialGeneration := object.Generation
	statusCandidate := object.DeepCopy()
	statusCandidate.Spec.Size = "large"
	statusCandidate.Status.ProviderID = "db-generation-boundary"
	statusCandidate.Status.ObservedGeneration = initialGeneration
	if err := kubernetesClient.Status().Update(ctx, statusCandidate); err != nil {
		t.Fatalf("update status with modified spec in request: %v", err)
	}

	key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
	stored := &ManagedDatabase{}
	if err := kubernetesClient.Get(ctx, key, stored); err != nil {
		t.Fatalf("read after status update: %v", err)
	}
	if stored.Spec.Size != testSizeSmall {
		t.Fatalf("status update changed spec size to %q", stored.Spec.Size)
	}
	if stored.Generation != initialGeneration {
		t.Fatalf("generation after status update = %d, want %d", stored.Generation, initialGeneration)
	}
	if stored.Status.ProviderID != statusCandidate.Status.ProviderID ||
		stored.Status.ObservedGeneration != initialGeneration {
		t.Fatalf("status update was not persisted: %+v", stored.Status)
	}

	stored.Labels = map[string]string{"review.example.io/chapter": "five"}
	if err := kubernetesClient.Update(ctx, stored); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if stored.Generation != initialGeneration {
		t.Fatalf("generation after metadata update = %d, want %d", stored.Generation, initialGeneration)
	}

	stored.Spec.Size = testSizeMedium
	if err := kubernetesClient.Update(ctx, stored); err != nil {
		t.Fatalf("update spec size: %v", err)
	}
	if stored.Generation != initialGeneration+1 {
		t.Fatalf("generation after spec update = %d, want %d", stored.Generation, initialGeneration+1)
	}
	if stored.Status.ObservedGeneration != initialGeneration {
		t.Fatalf(
			"observed generation after spec update = %d, want stale generation %d",
			stored.Status.ObservedGeneration,
			initialGeneration,
		)
	}

	currentGeneration := stored.Generation
	stored.Status.ObservedGeneration = currentGeneration
	if err := kubernetesClient.Status().Update(ctx, stored); err != nil {
		t.Fatalf("update observed generation: %v", err)
	}

	reported := &ManagedDatabase{}
	if err := kubernetesClient.Get(ctx, key, reported); err != nil {
		t.Fatalf("read current status: %v", err)
	}
	if reported.Generation != currentGeneration {
		t.Fatalf("generation after current status update = %d, want %d", reported.Generation, currentGeneration)
	}
	if reported.Status.ObservedGeneration != currentGeneration {
		t.Fatalf(
			"reported observed generation = %d, want %d",
			reported.Status.ObservedGeneration,
			currentGeneration,
		)
	}
}

func newManagedDatabase(name, namespace string, spec ManagedDatabaseSpec) *ManagedDatabase {
	return &ManagedDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       spec,
	}
}
