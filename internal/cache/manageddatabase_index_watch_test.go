/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package cache_test

import (
	"context"
	"testing"
	"time"

	databasev1alpha1 "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testNameLarge  = "analytics"
	testSizeMedium = "medium"
	testSizeIndex  = "spec.size"
)

type observedSizeUpdate struct {
	key                types.NamespacedName
	oldSize            string
	newSize            string
	oldResourceVersion string
	newResourceVersion string
}

// TestManagedDatabaseSizeIndexFollowsWatchUpdate observes one API-server write
// arrive through an informer and change membership in a test-only cache index.
// It does not exercise a production controller or a server-side field selector.
func TestManagedDatabaseSizeIndexFollowsWatchUpdate(t *testing.T) {
	scheme := testScheme(t)
	ctx, config, directClient := startTestAPI(t, scheme)

	largeDatabase := &databasev1alpha1.ManagedDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testNameLarge, Namespace: testNamespace},
		Spec: databasev1alpha1.ManagedDatabaseSpec{
			Engine: testEngine,
			Size:   testSizeLarge,
			Region: testRegion,
		},
	}
	if err := directClient.Create(ctx, largeDatabase); err != nil {
		t.Fatalf("create large ManagedDatabase: %v", err)
	}

	cachedClient, updates := startIndexedTestCache(t, ctx, config, scheme)
	assertIndexedNames(t, ctx, cachedClient, testSizeSmall, testName)
	assertIndexedNames(t, ctx, cachedClient, testSizeLarge, testNameLarge)
	assertIndexedNames(t, ctx, cachedClient, testSizeMedium)

	targetKey := types.NamespacedName{Name: testName, Namespace: testNamespace}
	current := &databasev1alpha1.ManagedDatabase{}
	if err := directClient.Get(ctx, targetKey, current); err != nil {
		t.Fatalf("read current ManagedDatabase directly: %v", err)
	}
	oldResourceVersion := current.ResourceVersion
	current.Spec.Size = testSizeMedium
	if err := directClient.Update(ctx, current); err != nil {
		t.Fatalf("update ManagedDatabase size directly: %v", err)
	}
	writtenResourceVersion := current.ResourceVersion
	if writtenResourceVersion == oldResourceVersion {
		t.Fatalf("write resourceVersion = %q, want a revision after %q", writtenResourceVersion, oldResourceVersion)
	}

	updateContext, cancelUpdate := context.WithTimeout(ctx, 10*time.Second)
	defer cancelUpdate()
	for {
		select {
		case update := <-updates:
			if update.key != targetKey || update.newResourceVersion != writtenResourceVersion {
				continue
			}
			if update.oldSize != testSizeSmall || update.newSize != testSizeMedium {
				t.Fatalf("observed size transition %q -> %q, want %q -> %q", update.oldSize, update.newSize, testSizeSmall, testSizeMedium)
			}
			if update.oldResourceVersion != oldResourceVersion {
				t.Fatalf("observed old resourceVersion = %q, want %q", update.oldResourceVersion, oldResourceVersion)
			}
			goto observedTargetUpdate
		case <-updateContext.Done():
			t.Fatalf("observe target informer update: %v", updateContext.Err())
		}
	}

observedTargetUpdate:
	if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			oldSize, err := indexedNames(ctx, cachedClient, testSizeSmall)
			if err != nil {
				return false, err
			}
			newSize, err := indexedNames(ctx, cachedClient, testSizeMedium)
			if err != nil {
				return false, err
			}
			return len(oldSize) == 0 && len(newSize) == 1 && newSize[0] == testName, nil
		}); err != nil {
		t.Fatalf("observe updated cache index membership: %v", err)
	}
	assertIndexedNames(t, ctx, cachedClient, testSizeLarge, testNameLarge)
}

func startIndexedTestCache(
	t *testing.T,
	ctx context.Context,
	config *rest.Config,
	scheme *runtime.Scheme,
) (client.Client, <-chan observedSizeUpdate) {
	t.Helper()

	cacheContext, cancelCache := context.WithCancel(ctx)
	localCache, err := ctrlcache.New(config, ctrlcache.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("create controller-runtime cache: %v", err)
	}
	if err := localCache.IndexField(cacheContext, &databasev1alpha1.ManagedDatabase{}, testSizeIndex,
		func(object client.Object) []string {
			database, ok := object.(*databasev1alpha1.ManagedDatabase)
			if !ok {
				return nil
			}
			return []string{database.Spec.Size}
		}); err != nil {
		t.Fatalf("register size field index: %v", err)
	}

	informer, err := localCache.GetInformer(cacheContext, &databasev1alpha1.ManagedDatabase{})
	if err != nil {
		t.Fatalf("register ManagedDatabase informer: %v", err)
	}
	updates := make(chan observedSizeUpdate, 8)
	registration, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObject, newObject any) {
			oldDatabase, oldOK := oldObject.(*databasev1alpha1.ManagedDatabase)
			newDatabase, newOK := newObject.(*databasev1alpha1.ManagedDatabase)
			if !oldOK || !newOK || newDatabase.Namespace != testNamespace || newDatabase.Name != testName {
				return
			}
			updates <- observedSizeUpdate{
				key:                client.ObjectKeyFromObject(newDatabase),
				oldSize:            oldDatabase.Spec.Size,
				newSize:            newDatabase.Spec.Size,
				oldResourceVersion: oldDatabase.ResourceVersion,
				newResourceVersion: newDatabase.ResourceVersion,
			}
		},
	})
	if err != nil {
		t.Fatalf("register ManagedDatabase event handler: %v", err)
	}

	cacheStopped := make(chan error, 1)
	go func() {
		cacheStopped <- localCache.Start(cacheContext)
	}()
	t.Cleanup(func() {
		if err := informer.RemoveEventHandler(registration); err != nil {
			t.Errorf("remove ManagedDatabase event handler: %v", err)
		}
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
	return cachedClient, updates
}

func assertIndexedNames(t *testing.T, ctx context.Context, cachedClient client.Client, size string, want ...string) {
	t.Helper()

	got, err := indexedNames(ctx, cachedClient, size)
	if err != nil {
		t.Fatalf("list size %q through cache index: %v", size, err)
	}
	if len(got) != len(want) {
		t.Fatalf("indexed names for size %q = %v, want %v", size, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("indexed names for size %q = %v, want %v", size, got, want)
		}
	}
}

func indexedNames(ctx context.Context, cachedClient client.Client, size string) ([]string, error) {
	list := &databasev1alpha1.ManagedDatabaseList{}
	if err := cachedClient.List(ctx, list,
		client.InNamespace(testNamespace),
		client.MatchingFields{testSizeIndex: size},
	); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(list.Items))
	for index := range list.Items {
		names = append(names, list.Items[index].Name)
	}
	return names, nil
}
