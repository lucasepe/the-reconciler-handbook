/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package ownership

import (
	"errors"
	"reflect"
	"testing"

	databasev1alpha1 "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	testOwnerName      = "orders"
	testOwnerNamespace = "payments"
	testOwnerUID       = types.UID("88fc5dd6-f041-4e5c-9d43-f94de7dd97b1")
)

func TestNewConnectionSecretMetadata(t *testing.T) {
	t.Parallel()

	owner, scheme := testOwnerAndScheme(t)
	secret, err := NewConnectionSecretMetadata(owner, scheme)
	if err != nil {
		t.Fatalf("construct connection Secret metadata: %v", err)
	}

	if secret.Name != "orders-connection" || secret.Namespace != testOwnerNamespace {
		t.Fatalf("Secret identity = %s/%s, want payments/orders-connection", secret.Namespace, secret.Name)
	}
	if secret.Data != nil || secret.StringData != nil {
		t.Fatal("metadata helper added Secret data")
	}

	controller := metav1.GetControllerOf(secret)
	if controller == nil {
		t.Fatal("connection Secret has no controller reference")
	}
	if controller.APIVersion != databasev1alpha1.GroupVersion.String() ||
		controller.Kind != "ManagedDatabase" ||
		controller.Name != testOwnerName ||
		controller.UID != testOwnerUID {
		t.Fatalf("controller reference = %+v, want current ManagedDatabase identity", *controller)
	}
	if controller.Controller == nil || !*controller.Controller {
		t.Fatal("owner reference is not controlling")
	}
	if controller.BlockOwnerDeletion == nil || !*controller.BlockOwnerDeletion {
		t.Fatal("owner reference does not use the pinned helper's blockOwnerDeletion default")
	}
}

func TestNewConnectionSecretMetadataRequiresOwnerIdentity(t *testing.T) {
	t.Parallel()

	_, scheme := testOwnerAndScheme(t)
	for name, owner := range map[string]*databasev1alpha1.ManagedDatabase{
		"nil owner":         nil,
		"missing name":      testOwner("", testOwnerNamespace, testOwnerUID),
		"missing namespace": testOwner(testOwnerName, "", testOwnerUID),
		"missing UID":       testOwner(testOwnerName, testOwnerNamespace, ""),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewConnectionSecretMetadata(owner, scheme); !errors.Is(err, ErrIncompleteOwnerIdentity) {
				t.Fatalf("construct metadata: got %v, want ErrIncompleteOwnerIdentity", err)
			}
		})
	}
}

func TestValidateConnectionSecretOwnership(t *testing.T) {
	t.Parallel()

	owner, scheme := testOwnerAndScheme(t)
	owned, err := NewConnectionSecretMetadata(owner, scheme)
	if err != nil {
		t.Fatalf("construct owned Secret: %v", err)
	}
	if err := ValidateConnectionSecretOwnership(owner, owned, scheme); err != nil {
		t.Fatalf("validate owned Secret: %v", err)
	}

	nonController := metav1.OwnerReference{
		APIVersion: databasev1alpha1.GroupVersion.String(),
		Kind:       "ManagedDatabase",
		Name:       testOwnerName,
		UID:        testOwnerUID,
	}
	otherController := *metav1.GetControllerOf(owned)
	otherController.Name = "other"
	otherController.UID = types.UID("2dc5cb8b-1575-47d6-ae6d-d529863a2371")

	tests := map[string]*corev1.Secret{
		"unowned": {
			ObjectMeta: metav1.ObjectMeta{Name: owned.Name, Namespace: owned.Namespace},
		},
		"labels only": {
			ObjectMeta: metav1.ObjectMeta{
				Name:      owned.Name,
				Namespace: owned.Namespace,
				Labels:    map[string]string{"app.kubernetes.io/name": "manageddatabase"},
			},
		},
		"non-controlling reference": {
			ObjectMeta: metav1.ObjectMeta{
				Name:            owned.Name,
				Namespace:       owned.Namespace,
				OwnerReferences: []metav1.OwnerReference{nonController},
			},
		},
		"different controller": {
			ObjectMeta: metav1.ObjectMeta{
				Name:            owned.Name,
				Namespace:       owned.Namespace,
				OwnerReferences: []metav1.OwnerReference{otherController},
			},
		},
		"wrong deterministic name": {
			ObjectMeta: metav1.ObjectMeta{
				Name:            "other-connection",
				Namespace:       owned.Namespace,
				OwnerReferences: owned.OwnerReferences,
			},
		},
		"wrong namespace": {
			ObjectMeta: metav1.ObjectMeta{
				Name:            owned.Name,
				Namespace:       "other",
				OwnerReferences: owned.OwnerReferences,
			},
		},
	}

	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var before *corev1.Secret
			if candidate != nil {
				before = candidate.DeepCopy()
			}
			err := ValidateConnectionSecretOwnership(owner, candidate, scheme)
			if _, ok := errors.AsType[*ConnectionSecretOwnershipConflictError](err); !ok {
				t.Fatalf("validate ownership: got %v, want ConnectionSecretOwnershipConflictError", err)
			}
			if !reflect.DeepEqual(candidate, before) {
				t.Fatal("ownership validation mutated the existing Secret")
			}
		})
	}
}

func TestValidateConnectionSecretOwnershipRequiresSecret(t *testing.T) {
	t.Parallel()

	owner, scheme := testOwnerAndScheme(t)
	if err := ValidateConnectionSecretOwnership(owner, nil, scheme); !errors.Is(err, ErrConnectionSecretRequired) {
		t.Fatalf("validate nil Secret: got %v, want ErrConnectionSecretRequired", err)
	}
}

func TestValidateConnectionSecretOwnershipRejectsRecreatedOwner(t *testing.T) {
	t.Parallel()

	original, scheme := testOwnerAndScheme(t)
	secret, err := NewConnectionSecretMetadata(original, scheme)
	if err != nil {
		t.Fatalf("construct original Secret: %v", err)
	}
	before := secret.DeepCopy()

	recreated := testOwner(testOwnerName, testOwnerNamespace, types.UID("b10a8aa6-b66f-47d4-aef1-b78f416a76c1"))
	err = ValidateConnectionSecretOwnership(recreated, secret, scheme)
	if _, ok := errors.AsType[*ConnectionSecretOwnershipConflictError](err); !ok {
		t.Fatalf("validate recreated owner: got %v, want ConnectionSecretOwnershipConflictError", err)
	}
	if !reflect.DeepEqual(secret, before) {
		t.Fatal("ownership validation replaced the historical UID")
	}
}

func testOwnerAndScheme(t *testing.T) (*databasev1alpha1.ManagedDatabase, *runtime.Scheme) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := databasev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register ManagedDatabase API: %v", err)
	}
	return testOwner(testOwnerName, testOwnerNamespace, testOwnerUID), scheme
}

func testOwner(name, namespace string, uid types.UID) *databasev1alpha1.ManagedDatabase {
	return &databasev1alpha1.ManagedDatabase{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		UID:       uid,
	}}
}
