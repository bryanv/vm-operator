// Copyright (c) 2019-2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mutation_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func uniTests() {
	Describe("Invoking Mutate", unitTestsMutating)
}

type unitMutationWebhookContext struct {
	builder.UnitTestContextForMutatingWebhook
	vm *vmopv1.VirtualMachine
}

func newUnitTestContextForMutatingWebhook() *unitMutationWebhookContext {
	vm := builder.DummyVirtualMachineA2()
	obj, err := builder.ToUnstructured(vm)
	Expect(err).ToNot(HaveOccurred())

	return &unitMutationWebhookContext{
		UnitTestContextForMutatingWebhook: *suite.NewUnitTestContextForMutatingWebhook(obj),
		vm:                                vm,
	}
}

func unitTestsMutating() {
	var (
		ctx *unitMutationWebhookContext
	)

	BeforeEach(func() {
		ctx = newUnitTestContextForMutatingWebhook()
	})

	AfterEach(func() {
		ctx = nil
	})

	It("noop", func() {
		_ = ctx
	})
}
