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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestManagedDatabaseFinalizerLifecycle covers API-server finalizer mechanics.
// envtest does not run kube-controller-manager, so this is not a garbage-
// collection or provider-cleanup test.
func TestManagedDatabaseFinalizerLifecycle(t *testing.T) {
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
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "finalizer-lifecycle"}}
	if err := kubernetesClient.Create(ctx, namespace); err != nil {
		t.Fatalf("create test namespace: %v", err)
	}

	t.Run("Delete policy remains visible until its finalizer is removed", func(t *testing.T) {
		object := finalizerTestDatabase("delete-policy", namespace.Name, DeletionPolicyDelete)
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create ManagedDatabase: %v", err)
		}

		if !controllerutil.AddFinalizer(object, ManagedDatabaseProviderCleanupFinalizer) {
			t.Fatal("first AddFinalizer reported no local change")
		}
		if controllerutil.AddFinalizer(object, ManagedDatabaseProviderCleanupFinalizer) {
			t.Fatal("second AddFinalizer reported a duplicate local change")
		}
		if err := kubernetesClient.Update(ctx, object); err != nil {
			t.Fatalf("persist provider-cleanup finalizer: %v", err)
		}
		if err := kubernetesClient.Delete(ctx, object); err != nil {
			t.Fatalf("request deletion: %v", err)
		}

		key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
		terminating := &ManagedDatabase{}
		if err := kubernetesClient.Get(ctx, key, terminating); err != nil {
			t.Fatalf("read terminating ManagedDatabase: %v", err)
		}
		if terminating.DeletionTimestamp.IsZero() {
			t.Fatal("deletionTimestamp is zero after accepted delete request")
		}
		if !controllerutil.ContainsFinalizer(terminating, ManagedDatabaseProviderCleanupFinalizer) {
			t.Fatalf("terminating object lost finalizer: %v", terminating.Finalizers)
		}

		controllerutil.AddFinalizer(terminating, "data.example.io/late-finalizer")
		if err := kubernetesClient.Update(ctx, terminating); !apierrors.IsInvalid(err) {
			t.Fatalf("add a finalizer after deletion began: got %v, want Invalid", err)
		}

		if err := kubernetesClient.Get(ctx, key, terminating); err != nil {
			t.Fatalf("read current terminating ManagedDatabase: %v", err)
		}
		if !controllerutil.RemoveFinalizer(terminating, ManagedDatabaseProviderCleanupFinalizer) {
			t.Fatal("RemoveFinalizer reported no local change")
		}
		if controllerutil.RemoveFinalizer(terminating, ManagedDatabaseProviderCleanupFinalizer) {
			t.Fatal("second RemoveFinalizer reported another local change")
		}
		if err := kubernetesClient.Update(ctx, terminating); err != nil {
			t.Fatalf("persist finalizer removal: %v", err)
		}

		waitForManagedDatabaseNotFound(t, ctx, kubernetesClient, key)
	})

	t.Run("Retain policy without the cleanup finalizer is removed", func(t *testing.T) {
		object := finalizerTestDatabase("retain-policy", namespace.Name, DeletionPolicyRetain)
		if err := kubernetesClient.Create(ctx, object); err != nil {
			t.Fatalf("create ManagedDatabase: %v", err)
		}
		if controllerutil.ContainsFinalizer(object, ManagedDatabaseProviderCleanupFinalizer) {
			t.Fatalf("Retain object unexpectedly has cleanup finalizer: %v", object.Finalizers)
		}
		if err := kubernetesClient.Delete(ctx, object); err != nil {
			t.Fatalf("delete Retain ManagedDatabase: %v", err)
		}

		key := types.NamespacedName{Name: object.Name, Namespace: object.Namespace}
		waitForManagedDatabaseNotFound(t, ctx, kubernetesClient, key)
	})
}

func finalizerTestDatabase(name, namespace string, policy DeletionPolicy) *ManagedDatabase {
	return newManagedDatabase(name, namespace, ManagedDatabaseSpec{
		Engine:         testEnginePostgres,
		Size:           testSizeSmall,
		Region:         testRegionEUWest,
		DeletionPolicy: policy,
	})
}

func waitForManagedDatabaseNotFound(
	t *testing.T,
	ctx context.Context,
	kubernetesClient client.Client,
	key types.NamespacedName,
) {
	t.Helper()

	if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			err := kubernetesClient.Get(ctx, key, &ManagedDatabase{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}); err != nil {
		t.Fatalf("wait for ManagedDatabase removal: %v", err)
	}
}
