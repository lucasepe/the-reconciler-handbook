/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package queue_test

import (
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/util/workqueue"
)

func TestDelayingQueueDeliversAtDeadlineUnderVirtualTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue := workqueue.NewTypedDelayingQueue[string]()
		const delay = 30 * time.Second

		start := time.Now()
		delivered := make(chan string, 1)
		queue.AddAfter(testKey, delay)

		go func() {
			item, shutdown := queue.Get()
			if shutdown {
				return
			}
			delivered <- item
		}()

		// Sleep advances only the bubble's virtual clock. At this point the
		// queue's deadline has not arrived, so a delivery would be a real
		// ordering failure rather than an unscheduled-consumer artifact.
		time.Sleep(delay - time.Nanosecond)
		synctest.Wait()
		select {
		case item := <-delivered:
			queue.Done(item)
			queue.ShutDown()
			t.Fatalf("item %q delivered after %s, before delay %s", item, time.Since(start), delay)
		default:
		}

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		select {
		case item := <-delivered:
			if item != testKey {
				t.Fatalf("delivered item = %q, want %q", item, testKey)
			}
			if elapsed := time.Since(start); elapsed != delay {
				t.Fatalf("delivery elapsed time = %s, want %s", elapsed, delay)
			}
			queue.Done(item)
		default:
			queue.ShutDown()
			t.Fatalf("item unavailable when virtual time reached %s", delay)
		}

		queue.ShutDown()
		synctest.Wait()
	})
}
