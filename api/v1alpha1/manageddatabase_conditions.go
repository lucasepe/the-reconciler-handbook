/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package v1alpha1

const (
	// ManagedDatabaseConditionReady reports whether the desired database and
	// its connection Secret are ready for use.
	ManagedDatabaseConditionReady = "Ready"

	// ManagedDatabaseReasonObserving means current evidence is insufficient
	// to decide whether the ManagedDatabase is ready.
	ManagedDatabaseReasonObserving = "Observing"
	// ManagedDatabaseReasonProvisioning means the provider database is not
	// yet ready.
	ManagedDatabaseReasonProvisioning = "Provisioning"
	// ManagedDatabaseReasonConnectionSecretPending means the provider database
	// is ready but the deterministic connection Secret is not yet observable.
	ManagedDatabaseReasonConnectionSecretPending = "ConnectionSecretPending"
	// ManagedDatabaseReasonProviderUnavailable means a transient provider
	// failure prevents a current readiness observation.
	ManagedDatabaseReasonProviderUnavailable = "ProviderUnavailable"
	// ManagedDatabaseReasonInvalidConfiguration means provider-dependent
	// validation rejected the current desired configuration.
	ManagedDatabaseReasonInvalidConfiguration = "InvalidConfiguration"
	// ManagedDatabaseReasonConnectionSecretConflict means the deterministic
	// connection Secret is not controlled by this ManagedDatabase UID.
	ManagedDatabaseReasonConnectionSecretConflict = "ConnectionSecretConflict"
	// ManagedDatabaseReasonReconciliationBlocked means an invariant or contract
	// failure prevents a current readiness conclusion.
	ManagedDatabaseReasonReconciliationBlocked = "ReconciliationBlocked"
	// ManagedDatabaseReasonReady means the provider database and deterministic
	// connection Secret are observable and ready for use.
	ManagedDatabaseReasonReady = "Ready"
)
