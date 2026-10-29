/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeletionPolicy defines what happens to the external database when its
// ManagedDatabase is deleted.
type DeletionPolicy string

const (
	// DeletionPolicyRetain leaves the external database in place.
	DeletionPolicyRetain DeletionPolicy = "Retain"
	// DeletionPolicyDelete requires confirmed provider cleanup before removal.
	DeletionPolicyDelete DeletionPolicy = "Delete"

	// ManagedDatabaseProviderCleanupFinalizer protects provider cleanup for a
	// ManagedDatabase whose deletion policy is Delete.
	ManagedDatabaseProviderCleanupFinalizer = "data.example.io/provider-cleanup"
)

// ManagedDatabaseSpec defines the desired state of ManagedDatabase.
type ManagedDatabaseSpec struct {
	// Engine is the database engine to provision.
	// +kubebuilder:validation:Enum=postgres
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="engine is immutable"
	// +required
	Engine string `json:"engine"`

	// Size is the provider-independent capacity class to provision.
	// +kubebuilder:validation:Enum=small;medium;large
	// +required
	Size string `json:"size"`

	// Region is the provider region in which to provision the database.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="region is immutable"
	// +required
	Region string `json:"region"`

	// DeletionPolicy controls whether deletion of this object also deletes the
	// external database. Delete requires provider cleanup guarded by the
	// provider-cleanup finalizer; Retain never authorizes provider deletion.
	// +kubebuilder:validation:Enum=Retain;Delete
	// +kubebuilder:default=Retain
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="deletionPolicy is immutable"
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// ManagedDatabaseStatus defines the observed state of ManagedDatabase.
type ManagedDatabaseStatus struct {
	// ProviderID is the stable provider identity of the database.
	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// OperationID is the stable identity of an in-progress provider operation.
	// +optional
	OperationID string `json:"operationID,omitempty"`

	// ObservedGeneration is the ManagedDatabase generation reflected in this status.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions contains the current observations of the ManagedDatabase.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 242",message="metadata.name must be no more than 242 characters so the connection Secret name is valid"

// ManagedDatabase is the API for a database managed through an external provider.
type ManagedDatabase struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ManagedDatabase
	// +required
	Spec ManagedDatabaseSpec `json:"spec"`

	// status defines the observed state of ManagedDatabase
	// +optional
	Status ManagedDatabaseStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ManagedDatabaseList contains a list of ManagedDatabase objects.
type ManagedDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ManagedDatabase `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ManagedDatabase{}, &ManagedDatabaseList{})
		return nil
	})
}
