/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

// Package ownership defines the Kubernetes ownership boundary for
// ManagedDatabase dependents. It does not read or write Kubernetes objects.
package ownership

import (
	"errors"
	"fmt"

	databasev1alpha1 "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const connectionSecretSuffix = "-connection"

var (
	ErrIncompleteOwnerIdentity  = errors.New("managed database owner identity is incomplete")
	ErrConnectionSecretRequired = errors.New("connection Secret is required for ownership validation")
)

// ConnectionSecretOwnershipConflictError reports that an existing Secret at
// the deterministic name is not controlled by the current ManagedDatabase.
// The existing Secret must remain unchanged when this error is returned.
type ConnectionSecretOwnershipConflictError struct {
	Secret types.NamespacedName
}

func (e *ConnectionSecretOwnershipConflictError) Error() string {
	return fmt.Sprintf("connection Secret %s is not controlled by the current ManagedDatabase", e.Secret)
}

// ConnectionSecretName returns the public deterministic name of the
// connection Secret for a ManagedDatabase name admitted by the CRD.
func ConnectionSecretName(managedDatabaseName string) string {
	return managedDatabaseName + connectionSecretSuffix
}

// NewConnectionSecretMetadata constructs an empty Secret carrying only the
// identity and controller reference required by the ownership contract.
func NewConnectionSecretMetadata(
	owner *databasev1alpha1.ManagedDatabase,
	scheme *runtime.Scheme,
) (*corev1.Secret, error) {
	if owner == nil || owner.Name == "" || owner.Namespace == "" || owner.UID == "" {
		return nil, ErrIncompleteOwnerIdentity
	}

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      ConnectionSecretName(owner.Name),
		Namespace: owner.Namespace,
	}}
	if err := controllerutil.SetControllerReference(owner, secret, scheme); err != nil {
		return nil, fmt.Errorf("set connection Secret controller reference: %w", err)
	}

	return secret, nil
}

// ValidateConnectionSecretOwnership checks whether an existing Secret is the
// dependent of the current ManagedDatabase. It never adopts or mutates the
// Secret.
func ValidateConnectionSecretOwnership(
	owner *databasev1alpha1.ManagedDatabase,
	secret *corev1.Secret,
	scheme *runtime.Scheme,
) error {
	desired, err := NewConnectionSecretMetadata(owner, scheme)
	if err != nil {
		return err
	}
	if secret == nil {
		return ErrConnectionSecretRequired
	}

	expectedController := metav1.GetControllerOf(desired)
	actualController := metav1.GetControllerOf(secret)
	if secret.Name != desired.Name ||
		secret.Namespace != desired.Namespace ||
		actualController == nil ||
		!sameOwner(*actualController, *expectedController) {
		return ownershipConflict(desired)
	}

	return nil
}

func sameOwner(actual, expected metav1.OwnerReference) bool {
	return actual.APIVersion == expected.APIVersion &&
		actual.Kind == expected.Kind &&
		actual.Name == expected.Name &&
		actual.UID == expected.UID
}

func ownershipConflict(secret *corev1.Secret) error {
	return &ConnectionSecretOwnershipConflictError{Secret: types.NamespacedName{
		Name:      secret.Name,
		Namespace: secret.Namespace,
	}}
}
