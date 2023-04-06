// Copyright (c) 2019-2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mutation_test

import (
	. "github.com/onsi/ginkgo"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func intgTests() {
	Describe("Invoking Mutation", intgTestsMutating)
}

type intgMutatingWebhookContext struct {
	builder.IntegrationTestContext
	vm *vmopv1.VirtualMachine
}

func newIntgMutatingWebhookContext() *intgMutatingWebhookContext {
	ctx := &intgMutatingWebhookContext{
		IntegrationTestContext: *suite.NewIntegrationTestContext(),
	}

	ctx.vm = builder.DummyVirtualMachineA2()
	ctx.vm.Namespace = ctx.Namespace

	return ctx
}

func intgTestsMutating() {
	var (
		ctx *intgMutatingWebhookContext
	)

	BeforeEach(func() {
		ctx = newIntgMutatingWebhookContext()
	})

	AfterEach(func() {
		ctx = nil
	})

	It("noop", func() {
		_ = ctx
	})
}
