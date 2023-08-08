// Copyright (c) 2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/network"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var _ = Describe("ResolveNCPBackingPostPlacement", func() {

	var (
		testConfig builder.VCSimTestConfig
		ctx        *builder.TestContextForVCSimA2

		results *network.NetworkInterfaceResults
		err     error
	)

	BeforeEach(func() {
		testConfig = builder.VCSimTestConfig{}
	})

	JustBeforeEach(func() {
		ctx = suite.NewTestContextForVCSimA2(testConfig)

		err = network.ResolveNCPBackingPostPlacement(
			ctx,
			ctx.VCClient.Client,
			ctx.GetSingleClusterCompute().Reference(),
			results)
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
	})

	FContext("returns success", func() {

		BeforeEach(func() {
			testConfig.WithNetworkEnv = builder.NetworkEnvNSXT

			results = &network.NetworkInterfaceResults{
				Results: []network.NetworkInterfaceResult{
					{
						NetworkID: builder.NsxTLogicalSwitchUUID,
						Backing:   nil,
					},
				},
			}
		})

		It("returns success", func() {
			Expect(err).ToNot(HaveOccurred())

			Expect(results.Results).To(HaveLen(1))
			By("should populate the backing", func() {
				backing := results.Results[0].Backing
				Expect(backing).ToNot(BeNil())
				Expect(backing.Reference()).To(Equal(ctx.NetworkRef.Reference()))
			})
		})
	})
})
