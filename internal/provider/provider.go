/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

// Package provider defines the narrow external-provider contract used by the
// ManagedDatabase examples. It does not contain a provider implementation.
package provider

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
)

const managedDatabaseKeyPrefix = "data.example.io/manageddatabase/"

var (
	// ErrEmptyUID reports an attempt to derive an external identity before
	// Kubernetes has assigned the ManagedDatabase UID.
	ErrEmptyUID = errors.New("managed database UID is empty")

	// ErrNotFound reports that a provider observation is not currently visible.
	// A provider can return this transiently while an accepted create propagates.
	ErrNotFound = errors.New("provider create result not found")
)

// CreateParameters is the semantic payload bound to an idempotency key by the
// first accepted create request.
type CreateParameters struct {
	Engine string
	Size   string
	Region string
}

// Equivalent reports whether two create payloads request the same engine,
// size, and region. It deliberately compares semantic fields rather than a
// serialized representation.
func (p CreateParameters) Equivalent(other CreateParameters) bool {
	return p == other
}

// CreateRequest asks the provider to ensure the database creation identified
// by IdempotencyKey.
type CreateRequest struct {
	IdempotencyKey string
	Parameters     CreateParameters
}

// CreateResult identifies the asynchronous operation or resulting provider
// resource bound to an accepted create request. ProviderID may be empty while
// the operation is still in progress.
type CreateResult struct {
	OperationID string
	ProviderID  string
}

// Client is the provider boundary needed for repeat-safe database creation.
//
// The first accepted EnsureDatabase call binds its key to its Parameters.
// Equivalent replays return the original result. A different payload for an
// existing key returns *IdempotencyConflictError without allocating or
// mutating a database. LookupByKey may temporarily return ErrNotFound because
// provider observation can lag, but the accepted result remains bound to the
// key for at least the lifetime of the database.
type Client interface {
	EnsureDatabase(context.Context, CreateRequest) (CreateResult, error)
	LookupByKey(context.Context, string) (CreateResult, error)
}

// IdempotencyConflictError reports that a key is already bound to a different
// immutable creation request.
type IdempotencyConflictError struct {
	Key       string
	Accepted  CreateParameters
	Requested CreateParameters
}

// Error implements error without serializing the accepted or requested
// parameters into an error message.
func (e *IdempotencyConflictError) Error() string {
	return fmt.Sprintf("idempotency key %q is bound to a different create request", e.Key)
}

// ManagedDatabaseKey returns the external create key for a server-assigned
// ManagedDatabase UID. Callers must not contact the provider when this returns
// an error.
func ManagedDatabaseKey(uid types.UID) (string, error) {
	if uid == "" {
		return "", ErrEmptyUID
	}

	return managedDatabaseKeyPrefix + string(uid), nil
}
