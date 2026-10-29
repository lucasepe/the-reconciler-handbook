/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package failure_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	databasev1alpha1 "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	"github.com/lucasepe/the-reconciler-handbook/internal/failure"
	"github.com/lucasepe/the-reconciler-handbook/internal/provider"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var errProviderPayload = errors.New("provider rejected token=secret-token")

func TestFailurePolicyDecisions(t *testing.T) {
	t.Parallel()

	conflictCause := apierrors.NewConflict(
		schema.GroupResource{Group: "data.example.io", Resource: "manageddatabases"},
		"orders",
		errors.New("stale resource version"),
	)
	tests := map[string]struct {
		failure *failure.Failure
		class   failure.Class
		queue   failure.QueueDisposition
	}{
		"provider observation unavailable": {
			failure: failure.Transient(
				databasev1alpha1.ManagedDatabaseReasonProviderUnavailable,
				"Provider observation is temporarily unavailable",
				errProviderPayload,
			),
			class: failure.ClassTransient,
			queue: failure.QueueRateLimitedRetry,
		},
		"Kubernetes write conflict": {
			failure: failure.Transient(
				"KubernetesWriteConflict",
				"Kubernetes state changed; the decision must be recomputed",
				conflictCause,
			),
			class: failure.ClassTransient,
			queue: failure.QueueRateLimitedRetry,
		},
		"provider-dependent configuration rejected": {
			failure: failure.UserCorrectable(
				databasev1alpha1.ManagedDatabaseReasonInvalidConfiguration,
				"The requested configuration is not available in this region",
				errProviderPayload,
			),
			class: failure.ClassUserCorrectable,
			queue: failure.QueueAwaitChange,
		},
		"connection Secret ownership collision": {
			failure: failure.UserCorrectable(
				databasev1alpha1.ManagedDatabaseReasonConnectionSecretConflict,
				"The connection Secret is controlled by another object",
				errors.New("secret has another controller owner"),
			),
			class: failure.ClassUserCorrectable,
			queue: failure.QueueAwaitChange,
		},
		"unexpected idempotency conflict": {
			failure: failure.Terminal(
				databasev1alpha1.ManagedDatabaseReasonReconciliationBlocked,
				"The accepted provider request conflicts with this object lifetime",
				&provider.IdempotencyConflictError{Key: "key"},
			),
			class: failure.ClassTerminal,
			queue: failure.QueueStopCurrentState,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			decision, ok := failure.Decide(fmt.Errorf("reconcile operation: %w", test.failure))
			if !ok {
				t.Fatal("typed failure was not classified through wrapping")
			}
			if decision.Class != test.class || decision.Queue != test.queue {
				t.Fatalf("decision = %+v, want class %q and queue %q", decision, test.class, test.queue)
			}
			if test.failure.Class() != test.class {
				t.Fatalf("failure class = %q, want %q", test.failure.Class(), test.class)
			}
			if test.failure.Reason() == "" || test.failure.PublicMessage() == "" {
				t.Fatal("failure omitted its stable reason or public message")
			}
		})
	}
	if !apierrors.IsConflict(failure.Transient(
		"KubernetesWriteConflict",
		"Kubernetes state changed; the decision must be recomputed",
		conflictCause,
	)) {
		t.Fatal("typed Kubernetes conflict was not preserved through failure wrapping")
	}
}

func TestFailurePreservesCauseWithoutPublishingIt(t *testing.T) {
	t.Parallel()

	classified := failure.Transient(
		databasev1alpha1.ManagedDatabaseReasonProviderUnavailable,
		"Provider observation is temporarily unavailable",
		errProviderPayload,
	)
	wrapped := fmt.Errorf("observe database: %w", classified)

	if !errors.Is(wrapped, errProviderPayload) {
		t.Fatal("wrapped sentinel cause is not inspectable with errors.Is")
	}
	var found *failure.Failure
	if !errors.As(wrapped, &found) || found != classified {
		t.Fatal("typed failure is not inspectable with errors.As")
	}
	if strings.Contains(classified.Error(), "secret-token") ||
		strings.Contains(classified.PublicMessage(), "secret-token") {
		t.Fatal("public failure text exposed the provider payload")
	}

	typedCause := &provider.IdempotencyConflictError{Key: "lifecycle-key"}
	terminal := failure.Terminal(
		databasev1alpha1.ManagedDatabaseReasonReconciliationBlocked,
		"The accepted provider request conflicts with this object lifetime",
		typedCause,
	)
	var foundConflict *provider.IdempotencyConflictError
	if !errors.As(terminal, &foundConflict) || foundConflict != typedCause {
		t.Fatal("typed provider cause is not inspectable with errors.As")
	}
}

func TestUnclassifiedErrorsAndProgressDoNotAcquireFailurePolicy(t *testing.T) {
	t.Parallel()

	if decision, ok := failure.Decide(nil); ok || decision != (failure.Decision{}) {
		t.Fatalf("nil error decision = %+v, classified = %t", decision, ok)
	}
	if decision, ok := failure.Decide(errors.New("unclassified")); ok || decision != (failure.Decision{}) {
		t.Fatalf("arbitrary error decision = %+v, classified = %t", decision, ok)
	}

	for _, state := range []provider.DatabaseState{
		provider.DatabaseStateCreating,
		provider.DatabaseStateDeleting,
	} {
		observation := provider.DatabaseObservation{State: state}
		var observationErr error
		if decision, ok := failure.Decide(observationErr); ok || decision != (failure.Decision{}) {
			t.Fatalf("progress state %q acquired failure decision %+v", observation.State, decision)
		}
		// Creating and Deleting are successful observations. A future controller
		// schedules their next observation; it does not manufacture an error for
		// this package to classify.
	}
}

func TestCleanupFailureNeverBecomesAbsence(t *testing.T) {
	t.Parallel()

	cleanup := failure.Transient(
		databasev1alpha1.ManagedDatabaseReasonProviderUnavailable,
		"Provider cleanup is temporarily unavailable",
		errors.New("delete timed out"),
	)
	decision, ok := failure.Decide(cleanup)
	if !ok || decision.Queue != failure.QueueRateLimitedRetry {
		t.Fatalf("cleanup decision = %+v, classified = %t", decision, ok)
	}

	observation := provider.DatabaseObservation{}
	if observation.State == provider.DatabaseStateAbsent {
		t.Fatal("failure fixture became authoritative provider absence")
	}
}
