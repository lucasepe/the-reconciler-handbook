/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	testEnginePostgres = "postgres"
	testSizeSmall      = "small"
	testSizeMedium     = "medium"
	testRegionEUWest   = "eu-west"
)

func TestManagedDatabaseRegistrationAndDeepCopy(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("register ManagedDatabase API: %v", err)
	}

	original := &ManagedDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "payments"},
		Spec: ManagedDatabaseSpec{
			Engine: testEnginePostgres,
			Size:   testSizeSmall,
			Region: testRegionEUWest,
		},
		Status: ManagedDatabaseStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse}},
		},
	}

	gvks, unversioned, err := scheme.ObjectKinds(original)
	if err != nil {
		t.Fatalf("look up ManagedDatabase kind: %v", err)
	}
	if unversioned || len(gvks) != 1 || gvks[0] != GroupVersion.WithKind("ManagedDatabase") {
		t.Fatalf("unexpected kind registration: gvks=%v unversioned=%t", gvks, unversioned)
	}

	copy := original.DeepCopy()
	copy.Status.Conditions[0].Type = "Changed"
	if original.Status.Conditions[0].Type != "Ready" {
		t.Fatal("DeepCopy shared the conditions backing array")
	}
}
