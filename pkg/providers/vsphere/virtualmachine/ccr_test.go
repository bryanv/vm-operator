// Copyright (c) 2022 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"

	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func ccrTests() {

	var (
		ctx  *builder.TestContextForVCSim
		vcVM *object.VirtualMachine
	)

	BeforeEach(func() {
		ctx = suite.NewTestContextForVCSim(builder.VCSimTestConfig{})

		var err error
		vcVM, err = ctx.Finder.VirtualMachine(ctx, "DC0_C0_RP0_VM0")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
	})

	It("Returns VM ResourcePool and ClusterComputeResource", func() {
		var o mo.VirtualMachine
		Expect(vcVM.Properties(ctx, vcVM.Reference(), nil, &o)).To(Succeed())

		rp, ccr, err := virtualmachine.GetVMResourcePoolAndCCR(ctx, vcVM)
		Expect(err).ToNot(HaveOccurred())
		Expect(rp).ToNot(BeNil())
		Expect(rp.Reference()).To(Equal(o.ResourcePool))
		compute, err := rp.Owner(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(ccr).ToNot(BeNil())
		Expect(ccr.Reference()).To(Equal(compute.Reference()))
	})
}
