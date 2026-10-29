/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package status_test

import (
	"testing"
	"time"

	databasev1alpha1 "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	statusutil "github.com/lucasepe/the-reconciler-handbook/internal/status"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetReadyConditionMaintainsOneCurrentObservation(t *testing.T) {
	t.Parallel()

	managedStatus := databasev1alpha1.ManagedDatabaseStatus{}
	changed := statusutil.SetReadyCondition(
		&managedStatus,
		metav1.ConditionUnknown,
		1,
		databasev1alpha1.ManagedDatabaseReasonObserving,
		"Waiting for a current provider observation",
	)
	if !changed {
		t.Fatal("initial Ready condition was not reported as a change")
	}

	ready := requireReady(t, managedStatus.Conditions)
	if ready.Status != metav1.ConditionUnknown {
		t.Fatalf("initial Ready status = %q, want %q", ready.Status, metav1.ConditionUnknown)
	}
	if ready.ObservedGeneration != 1 {
		t.Fatalf("initial Ready observedGeneration = %d, want 1", ready.ObservedGeneration)
	}
	if ready.LastTransitionTime.IsZero() {
		t.Fatal("initial Ready lastTransitionTime is zero")
	}

	// A known old time makes the next status-transition assertion deterministic.
	ready.LastTransitionTime = metav1.NewTime(time.Unix(1, 0))
	changed = statusutil.SetReadyCondition(
		&managedStatus,
		metav1.ConditionFalse,
		1,
		databasev1alpha1.ManagedDatabaseReasonProvisioning,
		"Provider database is not ready",
	)
	if !changed {
		t.Fatal("Unknown to False was not reported as a change")
	}

	ready = requireReady(t, managedStatus.Conditions)
	if ready.Status != metav1.ConditionFalse {
		t.Fatalf("provisioning Ready status = %q, want %q", ready.Status, metav1.ConditionFalse)
	}
	if ready.LastTransitionTime.Equal(&metav1.Time{Time: time.Unix(1, 0)}) {
		t.Fatal("lastTransitionTime did not change when status changed")
	}
	falseTransition := ready.LastTransitionTime

	changed = statusutil.SetReadyCondition(
		&managedStatus,
		metav1.ConditionFalse,
		2,
		databasev1alpha1.ManagedDatabaseReasonConnectionSecretPending,
		"Connection Secret is not yet observable",
	)
	if !changed {
		t.Fatal("same-status reason, message, and generation update was not reported as a change")
	}

	ready = requireReady(t, managedStatus.Conditions)
	if ready.Reason != databasev1alpha1.ManagedDatabaseReasonConnectionSecretPending {
		t.Fatalf("Ready reason = %q, want %q", ready.Reason, databasev1alpha1.ManagedDatabaseReasonConnectionSecretPending)
	}
	if ready.ObservedGeneration != 2 {
		t.Fatalf("Ready observedGeneration = %d, want 2", ready.ObservedGeneration)
	}
	if !ready.LastTransitionTime.Equal(&falseTransition) {
		t.Fatal("lastTransitionTime changed while Ready remained False")
	}

	ready.LastTransitionTime = metav1.NewTime(time.Unix(2, 0))
	changed = statusutil.SetReadyCondition(
		&managedStatus,
		metav1.ConditionTrue,
		2,
		databasev1alpha1.ManagedDatabaseReasonReady,
		"Database and connection Secret are ready",
	)
	if !changed {
		t.Fatal("False to True was not reported as a change")
	}

	ready = requireReady(t, managedStatus.Conditions)
	if ready.Status != metav1.ConditionTrue {
		t.Fatalf("final Ready status = %q, want %q", ready.Status, metav1.ConditionTrue)
	}
	if ready.LastTransitionTime.Equal(&metav1.Time{Time: time.Unix(2, 0)}) {
		t.Fatal("lastTransitionTime did not change when Ready became True")
	}
	trueTransition := ready.LastTransitionTime

	changed = statusutil.SetReadyCondition(
		&managedStatus,
		metav1.ConditionTrue,
		2,
		databasev1alpha1.ManagedDatabaseReasonReady,
		"Database and connection Secret are ready",
	)
	if changed {
		t.Fatal("identical Ready update was reported as a change")
	}
	ready = requireReady(t, managedStatus.Conditions)
	if !ready.LastTransitionTime.Equal(&trueTransition) {
		t.Fatal("identical update changed lastTransitionTime")
	}
}

func TestSetReadyConditionDoesNotClaimCompleteSnapshotFreshness(t *testing.T) {
	t.Parallel()

	managedStatus := databasev1alpha1.ManagedDatabaseStatus{ObservedGeneration: 7}
	statusutil.SetReadyCondition(
		&managedStatus,
		metav1.ConditionUnknown,
		8,
		databasev1alpha1.ManagedDatabaseReasonObserving,
		"Waiting for a current provider observation",
	)

	if managedStatus.ObservedGeneration != 7 {
		t.Fatalf("top-level observedGeneration = %d, want unchanged value 7", managedStatus.ObservedGeneration)
	}
	ready := requireReady(t, managedStatus.Conditions)
	if ready.ObservedGeneration != 8 {
		t.Fatalf("Ready observedGeneration = %d, want 8", ready.ObservedGeneration)
	}
}

func TestFailureReasonsPreserveReadyPolarityAndTransitionTime(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status  metav1.ConditionStatus
		reason  string
		message string
	}{
		"provider unavailable": {
			status:  metav1.ConditionUnknown,
			reason:  databasev1alpha1.ManagedDatabaseReasonProviderUnavailable,
			message: "Provider observation is temporarily unavailable",
		},
		"invalid configuration": {
			status:  metav1.ConditionFalse,
			reason:  databasev1alpha1.ManagedDatabaseReasonInvalidConfiguration,
			message: "The requested configuration is not available in this region",
		},
		"connection Secret conflict": {
			status:  metav1.ConditionFalse,
			reason:  databasev1alpha1.ManagedDatabaseReasonConnectionSecretConflict,
			message: "The connection Secret is controlled by another object",
		},
		"reconciliation blocked": {
			status:  metav1.ConditionUnknown,
			reason:  databasev1alpha1.ManagedDatabaseReasonReconciliationBlocked,
			message: "An invariant prevents a current readiness conclusion",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			transition := metav1.NewTime(time.Unix(3, 0))
			managedStatus := databasev1alpha1.ManagedDatabaseStatus{
				Conditions: []metav1.Condition{{
					Type:               databasev1alpha1.ManagedDatabaseConditionReady,
					Status:             test.status,
					ObservedGeneration: 8,
					Reason:             databasev1alpha1.ManagedDatabaseReasonObserving,
					Message:            "Earlier observation",
					LastTransitionTime: transition,
				}},
			}

			changed := statusutil.SetReadyCondition(
				&managedStatus,
				test.status,
				9,
				test.reason,
				test.message,
			)
			if !changed {
				t.Fatal("new failure reason was not reported as a material change")
			}
			ready := requireReady(t, managedStatus.Conditions)
			if ready.Status != test.status || ready.Reason != test.reason || ready.Message != test.message {
				t.Fatalf("Ready condition = %+v", ready)
			}
			if ready.ObservedGeneration != 9 {
				t.Fatalf("Ready observedGeneration = %d, want 9", ready.ObservedGeneration)
			}
			if !ready.LastTransitionTime.Equal(&transition) {
				t.Fatal("lastTransitionTime changed while Ready status stayed the same")
			}

			changed = statusutil.SetReadyCondition(
				&managedStatus,
				test.status,
				9,
				test.reason,
				test.message,
			)
			if changed {
				t.Fatal("identical failure condition was reported as a change")
			}
		})
	}
}

func requireReady(t *testing.T, conditions []metav1.Condition) *metav1.Condition {
	t.Helper()

	if len(conditions) != 1 {
		t.Fatalf("condition count = %d, want 1", len(conditions))
	}
	ready := meta.FindStatusCondition(conditions, databasev1alpha1.ManagedDatabaseConditionReady)
	if ready == nil {
		t.Fatal("Ready condition not found")
	}
	return ready
}
