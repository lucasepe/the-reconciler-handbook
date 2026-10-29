/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package cache_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	databasev1alpha1 "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	testNamespace = "cache-visibility"
	testName      = "orders"
	testRegion    = "eu-west"
	testEngine    = "postgres"
	testSizeSmall = "small"
	testSizeLarge = "large"
)

// TestManagedDatabaseCacheVisibility creates a controlled visibility window.
// The transform is a test synchronization hook, not production cache policy
// and not a measurement of normal informer latency.
func TestManagedDatabaseCacheVisibility(t *testing.T) {
	scheme := testScheme(t)
	ctx, config, directClient := startTestAPI(t, scheme)
	cachedClient, updateEntered, release := startTestCache(t, ctx, config, scheme)

	key := types.NamespacedName{Name: testName, Namespace: testNamespace}
	cachedBefore := &databasev1alpha1.ManagedDatabase{}
	if err := cachedClient.Get(ctx, key, cachedBefore); err != nil {
		t.Fatalf("read initial object through cache: %v", err)
	}
	if cachedBefore.Spec.Size != testSizeSmall {
		t.Fatalf("initial cached size = %q, want %q", cachedBefore.Spec.Size, testSizeSmall)
	}
	oldResourceVersion := cachedBefore.ResourceVersion

	cachedBefore.Spec.Size = testSizeLarge
	if err := cachedClient.Update(ctx, cachedBefore); err != nil {
		t.Fatalf("update through cache-backed client: %v", err)
	}
	writeResourceVersion := cachedBefore.ResourceVersion
	if writeResourceVersion == oldResourceVersion {
		t.Fatalf("write resourceVersion = %q, want a revision after %q", writeResourceVersion, oldResourceVersion)
	}

	select {
	case <-updateEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("updated object did not reach the controlled cache transform")
	}

	cachedDuringDelay := &databasev1alpha1.ManagedDatabase{}
	if err := cachedClient.Get(ctx, key, cachedDuringDelay); err != nil {
		t.Fatalf("read delayed object through cache: %v", err)
	}
	if cachedDuringDelay.Spec.Size != testSizeSmall || cachedDuringDelay.ResourceVersion != oldResourceVersion {
		t.Fatalf(
			"delayed cache returned size=%q resourceVersion=%q, want size=%q resourceVersion=%q",
			cachedDuringDelay.Spec.Size,
			cachedDuringDelay.ResourceVersion,
			testSizeSmall,
			oldResourceVersion,
		)
	}

	directDuringDelay := &databasev1alpha1.ManagedDatabase{}
	if err := directClient.Get(ctx, key, directDuringDelay); err != nil {
		t.Fatalf("read updated object directly: %v", err)
	}
	if directDuringDelay.Spec.Size != testSizeLarge || directDuringDelay.ResourceVersion != writeResourceVersion {
		t.Fatalf(
			"direct read returned size=%q resourceVersion=%q, want size=%q resourceVersion=%q",
			directDuringDelay.Spec.Size,
			directDuringDelay.ResourceVersion,
			testSizeLarge,
			writeResourceVersion,
		)
	}

	release()
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			observed := &databasev1alpha1.ManagedDatabase{}
			if err := cachedClient.Get(ctx, key, observed); err != nil {
				return false, err
			}
			return observed.Spec.Size == testSizeLarge &&
				observed.ResourceVersion == writeResourceVersion, nil
		}); err != nil {
		t.Fatalf("observe updated revision through cache: %v", err)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("register core API: %v", err)
	}
	if err := databasev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register ManagedDatabase API: %v", err)
	}
	return scheme
}

func startTestAPI(t *testing.T, scheme *runtime.Scheme) (context.Context, *rest.Config, client.Client) {
	t.Helper()

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

	directClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("create direct client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := directClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
	}); err != nil {
		t.Fatalf("create test namespace: %v", err)
	}

	database := &databasev1alpha1.ManagedDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testName, Namespace: testNamespace},
		Spec: databasev1alpha1.ManagedDatabaseSpec{
			Engine: testEngine,
			Size:   testSizeSmall,
			Region: testRegion,
		},
	}
	if err := directClient.Create(ctx, database); err != nil {
		t.Fatalf("create ManagedDatabase: %v", err)
	}
	return ctx, config, directClient
}

func startTestCache(
	t *testing.T,
	ctx context.Context,
	config *rest.Config,
	scheme *runtime.Scheme,
) (client.Client, <-chan struct{}, func()) {
	t.Helper()
	cacheContext, cancelCache := context.WithCancel(ctx)

	updateEntered := make(chan struct{})
	releaseUpdate := make(chan struct{})
	var enterOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUpdate) }) }
	t.Cleanup(release)

	visibilityGate := func(object any) (any, error) {
		candidate, ok := object.(*databasev1alpha1.ManagedDatabase)
		if !ok || candidate.Namespace != testNamespace || candidate.Name != testName ||
			candidate.Spec.Size != testSizeLarge {
			return object, nil
		}

		enterOnce.Do(func() { close(updateEntered) })
		select {
		case <-releaseUpdate:
		case <-cacheContext.Done():
		}
		return object, nil
	}

	localCache, err := ctrlcache.New(config, ctrlcache.Options{
		Scheme:           scheme,
		DefaultTransform: toolscache.TransformFunc(visibilityGate),
	})
	if err != nil {
		t.Fatalf("create controller-runtime cache: %v", err)
	}
	if _, err := localCache.GetInformer(cacheContext, &databasev1alpha1.ManagedDatabase{}); err != nil {
		t.Fatalf("register ManagedDatabase informer: %v", err)
	}

	cacheStopped := make(chan error, 1)
	go func() {
		cacheStopped <- localCache.Start(cacheContext)
	}()
	t.Cleanup(func() {
		release()
		cancelCache()
		select {
		case err := <-cacheStopped:
			if err != nil {
				t.Errorf("stop controller-runtime cache: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("controller-runtime cache did not stop")
		}
	})

	syncContext, cancelSync := context.WithTimeout(cacheContext, 10*time.Second)
	defer cancelSync()
	if !localCache.WaitForCacheSync(syncContext) {
		t.Fatal("wait for initial ManagedDatabase cache synchronization")
	}

	cachedClient, err := client.New(config, client.Options{
		Scheme: scheme,
		Cache:  &client.CacheOptions{Reader: localCache},
	})
	if err != nil {
		t.Fatalf("create cache-backed client: %v", err)
	}
	return cachedClient, updateEntered, release
}
