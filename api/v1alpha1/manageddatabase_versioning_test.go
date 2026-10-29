/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package v1alpha1

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	managedDatabaseCRDName = "manageddatabases.data.example.io"
	temporaryAPIVersion    = "v1beta1"
	fieldAPIVersion        = "apiVersion"
	fieldKind              = "kind"
	fieldMetadata          = "metadata"
	fieldName              = "name"
	fieldSpec              = "spec"
	fieldEngine            = "engine"
	fieldSize              = "size"
	fieldRegion            = "region"
)

var (
	crdResource = schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}
	namespaceResource = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	alphaResource     = schema.GroupVersionResource{
		Group: GroupVersion.Group, Version: GroupVersion.Version, Resource: "manageddatabases",
	}
	temporaryResource = schema.GroupVersionResource{
		Group: GroupVersion.Group, Version: temporaryAPIVersion, Resource: "manageddatabases",
	}
)

// TestManagedDatabaseVersioning is a disposable API-server experiment. The
// temporary version is not a proposed ManagedDatabase beta contract and never
// appears in the generated production CRD.
func TestManagedDatabaseVersioning(t *testing.T) {
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

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("create dynamic client: %v", err)
	}

	ctx := context.Background()
	const namespace = "versioning-lab"
	_, err = dynamicClient.Resource(namespaceResource).Create(ctx, &unstructured.Unstructured{
		Object: map[string]any{
			fieldAPIVersion: "v1",
			fieldKind:       "Namespace",
			fieldMetadata: map[string]any{
				fieldName: namespace,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create test namespace: %v", err)
	}

	alphaObject := managedDatabaseObject(GroupVersion.Version, namespace, "created-as-alpha")
	if _, err := dynamicClient.Resource(alphaResource).Namespace(namespace).Create(
		ctx, alphaObject, metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("create through %s: %v", GroupVersion.Version, err)
	}

	crd, err := dynamicClient.Resource(crdResource).Get(
		ctx, managedDatabaseCRDName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("read installed CRD: %v", err)
	}
	addTemporaryStorageVersion(t, crd)
	if _, err := dynamicClient.Resource(crdResource).Update(ctx, crd, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("add temporary storage version: %v", err)
	}

	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			storedCRD, err := dynamicClient.Resource(crdResource).Get(
				ctx, managedDatabaseCRDName, metav1.GetOptions{},
			)
			if err != nil {
				return false, err
			}
			storedVersions, found, err := unstructured.NestedStringSlice(
				storedCRD.Object, "status", "storedVersions",
			)
			if err != nil {
				return false, fmt.Errorf("read status.storedVersions: %w", err)
			}
			return found && slices.Contains(storedVersions, GroupVersion.Version) &&
				slices.Contains(storedVersions, temporaryAPIVersion), nil
		}); err != nil {
		t.Fatalf("observe both storage versions in CRD status: %v", err)
	}

	var throughTemporary *unstructured.Unstructured
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			throughTemporary, err = dynamicClient.Resource(temporaryResource).Namespace(namespace).Get(
				ctx, alphaObject.GetName(), metav1.GetOptions{},
			)
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		}); err != nil {
		t.Fatalf("read alpha-created object through temporary version: %v", err)
	}
	assertVersionAndSpec(t, throughTemporary, temporaryAPIVersion)

	temporaryObject := managedDatabaseObject(temporaryAPIVersion, namespace, "created-as-temporary")
	if _, err := dynamicClient.Resource(temporaryResource).Namespace(namespace).Create(
		ctx, temporaryObject, metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("create through temporary version: %v", err)
	}
	throughAlpha, err := dynamicClient.Resource(alphaResource).Namespace(namespace).Get(
		ctx, temporaryObject.GetName(), metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("read temporary-created object through %s: %v", GroupVersion.Version, err)
	}
	assertVersionAndSpec(t, throughAlpha, GroupVersion.Version)

	crd, err = dynamicClient.Resource(crdResource).Get(
		ctx, managedDatabaseCRDName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("read CRD before attempted version removal: %v", err)
	}
	removeVersionFromSpec(t, crd, GroupVersion.Version)
	_, err = dynamicClient.Resource(crdResource).Update(ctx, crd, metav1.UpdateOptions{})
	if !apierrors.IsInvalid(err) {
		t.Fatalf("remove stored version from CRD: got %v, want Invalid", err)
	}
	if !strings.Contains(err.Error(), "status.storedVersions") {
		t.Fatalf("removal error = %q, want status.storedVersions detail", err)
	}
}

func managedDatabaseObject(version, namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		fieldAPIVersion: GroupVersion.Group + "/" + version,
		fieldKind:       "ManagedDatabase",
		fieldMetadata: map[string]any{
			fieldName:   name,
			"namespace": namespace,
		},
		fieldSpec: map[string]any{
			fieldEngine: testEnginePostgres,
			fieldSize:   testSizeSmall,
			fieldRegion: testRegionEUWest,
		},
	}}
}

func addTemporaryStorageVersion(t *testing.T, crd *unstructured.Unstructured) {
	t.Helper()

	versions, found, err := unstructured.NestedSlice(crd.Object, fieldSpec, "versions")
	if err != nil || !found || len(versions) != 1 {
		t.Fatalf("read production CRD versions: found=%t len=%d err=%v", found, len(versions), err)
	}
	alpha, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatalf("production CRD version has type %T, want map", versions[0])
	}
	alpha["storage"] = false
	temporary := runtime.DeepCopyJSONValue(alpha).(map[string]any)
	temporary[fieldName] = temporaryAPIVersion
	temporary["served"] = true
	temporary["storage"] = true
	if err := unstructured.SetNestedSlice(crd.Object, []any{alpha, temporary}, fieldSpec, "versions"); err != nil {
		t.Fatalf("set temporary CRD versions: %v", err)
	}
	if err := unstructured.SetNestedField(crd.Object, map[string]any{"strategy": "None"}, fieldSpec, "conversion"); err != nil {
		t.Fatalf("set None conversion: %v", err)
	}
}

func removeVersionFromSpec(t *testing.T, crd *unstructured.Unstructured, version string) {
	t.Helper()

	versions, found, err := unstructured.NestedSlice(crd.Object, fieldSpec, "versions")
	if err != nil || !found {
		t.Fatalf("read CRD versions before removal: found=%t err=%v", found, err)
	}
	kept := make([]any, 0, len(versions)-1)
	for _, item := range versions {
		candidate, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("CRD version has type %T, want map", item)
		}
		if candidate[fieldName] != version {
			kept = append(kept, candidate)
		}
	}
	if err := unstructured.SetNestedSlice(crd.Object, kept, fieldSpec, "versions"); err != nil {
		t.Fatalf("remove CRD version: %v", err)
	}
}

func assertVersionAndSpec(t *testing.T, object *unstructured.Unstructured, version string) {
	t.Helper()

	if got, want := object.GetAPIVersion(), GroupVersion.Group+"/"+version; got != want {
		t.Fatalf("apiVersion = %q, want %q", got, want)
	}
	for field, want := range map[string]string{
		fieldEngine: testEnginePostgres,
		fieldSize:   testSizeSmall,
		fieldRegion: testRegionEUWest,
	} {
		got, found, err := unstructured.NestedString(object.Object, fieldSpec, field)
		if err != nil || !found || got != want {
			t.Fatalf("spec.%s = %q, found=%t err=%v; want %q", field, got, found, err, want)
		}
	}
}
