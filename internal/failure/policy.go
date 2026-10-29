/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

// Package failure defines the small failure-policy vocabulary used by the
// ManagedDatabase case study. It does not classify arbitrary errors or perform
// reconciliation, reporting, logging, or queue operations.
package failure

import "errors"

// Class describes what kind of change can make a failed operation progress.
type Class string

const (
	// ClassTransient may resolve without a desired-state change.
	ClassTransient Class = "Transient"
	// ClassUserCorrectable requires a relevant user-visible state change.
	ClassUserCorrectable Class = "UserCorrectable"
	// ClassTerminal means retrying the same observed state through the same
	// branch is not expected to help.
	ClassTerminal Class = "Terminal"
)

// QueueDisposition describes how the failed reconcile attempt should affect
// queue scheduling. It contains no duration or queue implementation.
type QueueDisposition string

const (
	// QueueRateLimitedRetry requests ordinary failure backoff.
	QueueRateLimitedRetry QueueDisposition = "RateLimitedRetry"
	// QueueAwaitChange waits for a relevant watch or another documented wake-up.
	QueueAwaitChange QueueDisposition = "AwaitChange"
	// QueueStopCurrentState stops retrying the same state through the same path.
	QueueStopCurrentState QueueDisposition = "StopCurrentState"
)

// Failure carries an explicitly selected class, stable public reason, safe
// public message, and an inspectable cause. Error deliberately returns only
// the safe message; Unwrap preserves errors.Is and errors.As for local policy.
type Failure struct {
	class         Class
	reason        string
	publicMessage string
	cause         error
}

// Transient constructs a failure that may resolve without desired-state
// change and should receive rate-limited retry.
func Transient(reason, publicMessage string, cause error) *Failure {
	return newFailure(ClassTransient, reason, publicMessage, cause)
}

// UserCorrectable constructs a failure that waits for relevant state to
// change rather than repeatedly retrying unchanged intent.
func UserCorrectable(reason, publicMessage string, cause error) *Failure {
	return newFailure(ClassUserCorrectable, reason, publicMessage, cause)
}

// Terminal constructs a failure for which retrying the same observed state
// through the same branch is not expected to help.
func Terminal(reason, publicMessage string, cause error) *Failure {
	return newFailure(ClassTerminal, reason, publicMessage, cause)
}

func newFailure(class Class, reason, publicMessage string, cause error) *Failure {
	return &Failure{
		class:         class,
		reason:        reason,
		publicMessage: publicMessage,
		cause:         cause,
	}
}

// Error returns the sanitized public message, not the wrapped cause.
func (f *Failure) Error() string {
	return f.publicMessage
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (f *Failure) Unwrap() error {
	return f.cause
}

// Class returns the explicitly selected failure class.
func (f *Failure) Class() Class {
	return f.class
}

// Reason returns the stable programmatic reporting reason.
func (f *Failure) Reason() string {
	return f.reason
}

// PublicMessage returns sanitized human context suitable for status or an
// Event after the caller applies the policy of that channel.
func (f *Failure) PublicMessage() string {
	return f.publicMessage
}

// Decision describes the local class and queue disposition selected for a
// typed Failure. It does not choose Ready status or emit any report.
type Decision struct {
	Class Class
	Queue QueueDisposition
}

// Decide inspects a typed Failure, including through wrapping. Arbitrary or
// nil errors remain unclassified instead of silently acquiring retry policy.
func Decide(err error) (Decision, bool) {
	var classified *Failure
	if !errors.As(err, &classified) {
		return Decision{}, false
	}

	return Decision{
		Class: classified.class,
		Queue: queueDisposition(classified.class),
	}, true
}

func queueDisposition(class Class) QueueDisposition {
	switch class {
	case ClassTransient:
		return QueueRateLimitedRetry
	case ClassUserCorrectable:
		return QueueAwaitChange
	case ClassTerminal:
		return QueueStopCurrentState
	default:
		return ""
	}
}
