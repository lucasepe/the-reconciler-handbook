/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package v1alpha1

import (
	"context"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestManagedDatabaseOptimisticConcurrency is a deterministic API-server
// interleaving. It verifies conflict mechanics, not concurrent worker timing.
func TestManagedDatabaseOptimisticConcurrency(t *testing.T) {
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
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "concurrency-lab"}}
	if err := kubernetesClient.Create(ctx, namespace); err != nil {
		t.Fatalf("create test namespace: %v", err)
	}

	t.Run("rejects an incompatible stale spec update", func(t *testing.T) {
		object := newManagedDatabase("stale-spec", namespace.Name, ManagedDatabaseSpec{
			Engine: testEnginePostgres,
			Size:   testSizeSmall,
			Region: testRegionEUWest,
		})
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create ManagedDatabase: %v", err)
		}

		winner := object.DeepCopy()
		stale := object.DeepCopy()
		if winner.ResourceVersion != stale.ResourceVersion {
			t.Fatalf("initial resource versions differ: winner=%q stale=%q", winner.ResourceVersion, stale.ResourceVersion)
		}

		winner.Spec.Size = testSizeMedium
		if err := kubernetesClient.Update(ctx, winner); err != nil {
			t.Fatalf("write winning size update: %v", err)
		}

		stale.Spec.Size = "large"
		if err := kubernetesClient.Update(ctx, stale); !apierrors.IsConflict(err) {
			t.Fatalf("write stale size update: got %v, want Conflict", err)
		}

		stored := &ManagedDatabase{}
		key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
		if err := kubernetesClient.Get(ctx, key, stored); err != nil {
			t.Fatalf("read after stale update: %v", err)
		}
		if stored.Spec.Size != testSizeMedium {
			t.Fatalf("stored size = %q, want winning size %q", stored.Spec.Size, testSizeMedium)
		}
		if stored.ResourceVersion != winner.ResourceVersion {
			t.Fatalf("stored resource version = %q, want winning version %q", stored.ResourceVersion, winner.ResourceVersion)
		}
	})

	t.Run("rejects stale status after a spec update", func(t *testing.T) {
		object := newManagedDatabase("stale-status", namespace.Name, ManagedDatabaseSpec{
			Engine: testEnginePostgres,
			Size:   testSizeSmall,
			Region: testRegionEUWest,
		})
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create ManagedDatabase: %v", err)
		}

		specWriter := object.DeepCopy()
		staleStatusWriter := object.DeepCopy()
		observedGeneration := staleStatusWriter.Generation

		specWriter.Spec.Size = testSizeMedium
		if err := kubernetesClient.Update(ctx, specWriter); err != nil {
			t.Fatalf("write size update: %v", err)
		}
		if specWriter.Generation <= observedGeneration {
			t.Fatalf("generation after spec update = %d, want greater than %d", specWriter.Generation, observedGeneration)
		}

		staleStatusWriter.Status.ProviderID = "db-stale-observation"
		staleStatusWriter.Status.ObservedGeneration = observedGeneration
		if err := kubernetesClient.Status().Update(ctx, staleStatusWriter); !apierrors.IsConflict(err) {
			t.Fatalf("write stale status: got %v, want Conflict", err)
		}

		key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
		fresh := &ManagedDatabase{}
		if err := kubernetesClient.Get(ctx, key, fresh); err != nil {
			t.Fatalf("read current ManagedDatabase: %v", err)
		}
		if fresh.Status.ProviderID != "" || fresh.Status.ObservedGeneration != 0 {
			t.Fatalf("stale status was persisted: %+v", fresh.Status)
		}

		fresh.Status.ProviderID = "db-current-observation"
		fresh.Status.ObservedGeneration = fresh.Generation
		if err := kubernetesClient.Status().Update(ctx, fresh); err != nil {
			t.Fatalf("write status from fresh object: %v", err)
		}

		stored := &ManagedDatabase{}
		if err := kubernetesClient.Get(ctx, key, stored); err != nil {
			t.Fatalf("read updated status: %v", err)
		}
		if stored.Status.ProviderID != fresh.Status.ProviderID ||
			stored.Status.ObservedGeneration != stored.Generation {
			t.Fatalf("stored status does not reflect current generation: generation=%d status=%+v", stored.Generation, stored.Status)
		}
	})
}
