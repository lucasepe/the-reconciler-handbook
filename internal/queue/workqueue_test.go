/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package queue_test

import (
	"testing"
	"time"

	"k8s.io/client-go/util/workqueue"
)

const testKey = "payments/orders"

func TestQueueCoalescesAndRemembersDirtyWork(t *testing.T) {
	queue := workqueue.NewTyped[string]()
	t.Cleanup(queue.ShutDown)

	queue.Add(testKey)
	queue.Add(testKey)
	if got := queue.Len(); got != 1 {
		t.Fatalf("queued duplicates = %d, want 1 item", got)
	}

	item, shutdown := queue.Get()
	if shutdown {
		t.Fatal("queue shut down before delivering the first item")
	}
	if item != testKey {
		t.Fatalf("first item = %q, want %q", item, testKey)
	}

	queue.Add(testKey)
	queue.Add(testKey)
	if got := queue.Len(); got != 0 {
		t.Fatalf("item was queued while still processing: length = %d", got)
	}

	queue.Done(item)
	if got := queue.Len(); got != 1 {
		t.Fatalf("dirty item deliveries after Done = %d, want 1", got)
	}

	item, shutdown = queue.Get()
	if shutdown {
		t.Fatal("queue shut down before delivering the dirty item")
	}
	if item != testKey {
		t.Fatalf("second item = %q, want %q", item, testKey)
	}
	queue.Done(item)
}

func TestExplicitFailureLimiterTracksAndForgetsOneKey(t *testing.T) {
	// These fixture durations expose the limiter's mechanics. They are not the
	// controller-runtime default or a provider polling recommendation.
	limiter := workqueue.NewTypedItemExponentialFailureRateLimiter[string](
		time.Second,
		4*time.Second,
	)

	wantDelays := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		4 * time.Second,
	}
	for attempt, want := range wantDelays {
		if got := limiter.When(testKey); got != want {
			t.Fatalf("attempt %d delay = %s, want %s", attempt+1, got, want)
		}
	}
	if got := limiter.NumRequeues(testKey); got != len(wantDelays) {
		t.Fatalf("retry count = %d, want %d", got, len(wantDelays))
	}

	limiter.Forget(testKey)
	if got := limiter.NumRequeues(testKey); got != 0 {
		t.Fatalf("retry count after Forget = %d, want 0", got)
	}
	if got := limiter.When(testKey); got != time.Second {
		t.Fatalf("delay after Forget = %s, want %s", got, time.Second)
	}
}
