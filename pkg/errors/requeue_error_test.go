// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package errors_test

import (
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctrl "sigs.k8s.io/controller-runtime"

	pkgerr "github.com/vmware-tanzu/vm-operator/pkg/errors"
)

var _ = Describe("RequeueError", func() {

	DescribeTable("Error",
		func(e error, expErr string) {
			Expect(e).To(MatchError(expErr))
		},

		Entry(
			"after is 0",
			pkgerr.RequeueError{},
			"requeue immediately",
		),

		Entry(
			"after is 1s",
			pkgerr.RequeueError{After: time.Second * 1},
			"requeue after 1s",
		),
	)
})

var _ = Describe("NoRequeueError", func() {

	DescribeTable("Error",
		func(e error, expErr string) {
			Expect(e).To(MatchError(expErr))
		},

		Entry(
			"no message",
			pkgerr.NoRequeueError{},
			"no requeue",
		),

		Entry(
			"with message",
			pkgerr.NoRequeueError{Message: "hi"},
			"hi",
		),
	)
})

var _ = Describe("ResultFromError", func() {

	DescribeTable("ResultFromError",
		func(e error, expResult ctrl.Result, expErr string) {
			res, resErr := pkgerr.ResultFromError(e)
			Expect(res).To(Equal(expResult))
			if expErr == "" {
				Expect(resErr).ToNot(HaveOccurred())
			} else {
				Expect(resErr).To(MatchError(expErr))
			}
		},

		Entry(
			"err is nil",
			nil,
			ctrl.Result{},
			"",
		),

		Entry(
			"err is not a (No)RequeueError",
			errors.New("hi"),
			ctrl.Result{},
			"hi",
		),

		Entry(
			"err is RequeueError",
			pkgerr.RequeueError{},
			ctrl.Result{Requeue: true},
			"",
		),

		Entry(
			"err is NoRequeueError",
			pkgerr.NoRequeueError{Message: "hi"},
			ctrl.Result{},
			"terminal error: hi",
		),

		Entry(
			"err is wrapped RequeueError",
			fmt.Errorf("hi: %w", pkgerr.RequeueError{After: time.Second * 1}),
			ctrl.Result{RequeueAfter: time.Second * 1},
			"",
		),

		Entry(
			"err is RequeueError wrapped with multiple errors",
			fmt.Errorf(
				"hi: %w",
				fmt.Errorf("there: %w, %w",
					errors.New("hello"),
					pkgerr.RequeueError{After: time.Minute * 1},
				),
			),
			ctrl.Result{RequeueAfter: time.Minute * 1},
			"",
		),

		Entry(
			"err is wrapped NoRequeueError",
			fmt.Errorf("hi: %w", pkgerr.NoRequeueError{Message: "hi"}),
			ctrl.Result{},
			"terminal error: hi",
		),

		Entry(
			"err is NoRequeueError wrapped with multiple errors",
			fmt.Errorf(
				"hi: %w",
				fmt.Errorf("there: %w, %w",
					errors.New("hello"),
					pkgerr.NoRequeueError{Message: "hi"},
				),
			),
			ctrl.Result{},
			"terminal error: hi", // How TerminalError Error() behaves.
		),

		Entry(
			"err is wrapped NoRequeueError and RequeueError",
			fmt.Errorf("%w, %w",
				pkgerr.NoRequeueError{Message: "hi"},
				pkgerr.RequeueError{After: time.Second * 1}),
			ctrl.Result{RequeueAfter: time.Second * 1},
			"",
		),
	)
})
