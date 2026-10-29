/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package provider

import "context"

// DatabaseState is an authoritative provider lifecycle observation for one
// ManagedDatabase key.
type DatabaseState string

const (
	// DatabaseStateCreating means the accepted create operation is incomplete.
	DatabaseStateCreating DatabaseState = "Creating"
	// DatabaseStateReady means the provider database exists and is usable.
	DatabaseStateReady DatabaseState = "Ready"
	// DatabaseStateDeleting means the accepted deletion operation is incomplete.
	DatabaseStateDeleting DatabaseState = "Deleting"
	// DatabaseStateAbsent affirmatively means that no database associated with
	// the lifecycle key remains. ErrNotFound is not equivalent to this state.
	DatabaseStateAbsent DatabaseState = "Absent"
)

// DatabaseObservation describes provider state for one UID-derived lifecycle
// key. Properties contains semantic observed values when a database exists.
type DatabaseObservation struct {
	State             DatabaseState
	CreateOperationID string
	DeleteOperationID string
	ProviderID        string
	Properties        CreateParameters
}

// DeleteResult identifies the logical provider cleanup operation accepted for
// one UID-derived lifecycle key.
type DeleteResult struct {
	OperationID string
}

// LifecycleClient adds provider lifecycle observation and repeat-safe deletion
// to the established repeat-safe creation contract.
//
// ObserveDatabase may return ErrNotFound while provider state is temporarily
// not visible. Only a successful observation with DatabaseStateAbsent is
// authoritative evidence that cleanup has completed.
//
// EnsureDatabaseAbsent is addressed by the same lifecycle key used for
// creation. Its first accepted call starts at most one logical cleanup
// operation. Replays return that operation, including after an ambiguous
// response. Callers resolve a pending or temporarily invisible create before
// requesting deletion; this interface does not promise cancellation of create.
type LifecycleClient interface {
	Client
	ObserveDatabase(context.Context, string) (DatabaseObservation, error)
	EnsureDatabaseAbsent(context.Context, string) (DeleteResult, error)
}
