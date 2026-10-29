/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

// Package status contains narrow helpers for constructing ManagedDatabase
// status. It does not decide whether an observation is true or write status to
// the Kubernetes API.
package status

import (
	databasev1alpha1 "github.com/lucasepe/the-reconciler-handbook/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetReadyCondition sets the current Ready observation and reports whether it
// materially changed. observedGeneration is the ManagedDatabase generation on
// which this condition, rather than the complete status snapshot, is based.
func SetReadyCondition(
	managedStatus *databasev1alpha1.ManagedDatabaseStatus,
	conditionStatus metav1.ConditionStatus,
	observedGeneration int64,
	reason string,
	message string,
) bool {
	return meta.SetStatusCondition(&managedStatus.Conditions, metav1.Condition{
		Type:               databasev1alpha1.ManagedDatabaseConditionReady,
		Status:             conditionStatus,
		ObservedGeneration: observedGeneration,
		Reason:             reason,
		Message:            message,
	})
}
