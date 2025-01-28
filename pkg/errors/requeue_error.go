// // © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"errors"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// RequeueError may be returned from any part of a reconcile call stack and the
// controller should requeue the request. If After > 0 then the request is
// requeued with the provided value, otherwise the request is requeued
// immediately.
type RequeueError struct {
	After time.Duration
}

func (e RequeueError) Error() string {
	if e.After == 0 {
		return "requeue immediately"
	}
	return fmt.Sprintf("requeue after %s", e.After)
}

// NoRequeueError may be returned from any part of a reconcile call stack and the
// controller will not requeue the request. This can be used to return an error
// but not cause the back-off retry to occur.
type NoRequeueError struct {
	Message string
}

func (e NoRequeueError) Error() string {
	if e.Message == "" {
		return "no requeue"
	}
	return e.Message
}

// ResultFromError returns a ReconcileResult based on the provided error. If
// the error contains an embedded RequeueError or NoRequeueError, then it is
// used to influence the result. An embedded RequeueError is favored in an
// error that also contains a NoRequeueError.
// An embedded NoRequeueError will return a controller-runtime TerminalError
// that will be logged and counted as an error but does not retry.
func ResultFromError(err error) (ctrl.Result, error) {
	if err == nil {
		return ctrl.Result{}, nil
	}

	var requeue RequeueError
	if errors.As(err, &requeue) {
		if requeue.After == 0 {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{RequeueAfter: requeue.After}, nil
	}

	var noRequeue NoRequeueError
	if errors.As(err, &noRequeue) {
		// TerminalError is confusingly named: it won't cause an error retry
		// to be requeue but the object is still watched.
		return ctrl.Result{}, reconcile.TerminalError(noRequeue)
	}

	return ctrl.Result{}, err
}
