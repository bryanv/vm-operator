// Copyright (c) 2021-2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	vimTypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/utils/pointer"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/vmlifecycle"
)

var _ = Describe("VAppConfig Bootstrap", func() {
	var (
		configInfo       *vimTypes.VirtualMachineConfigInfo
		vAppConfigSpec   *vmopv1.VirtualMachineBootstrapVAppConfigSpec
		bsArgs           vmlifecycle.BootstrapArgs
		baseVMConfigSpec vimTypes.BaseVmConfigSpec
	)

	BeforeEach(func() {
		configInfo = &vimTypes.VirtualMachineConfigInfo{}
		vAppConfigSpec = &vmopv1.VirtualMachineBootstrapVAppConfigSpec{}

		bsArgs.VAppData = make(map[string]string)
		bsArgs.VAppExData = make(map[string]map[string]string)
	})

	AfterEach(func() {
		vAppConfigSpec = nil
		baseVMConfigSpec = nil
		bsArgs = vmlifecycle.BootstrapArgs{}
	})

	Context("GetOVFVAppConfigForConfigSpec", func() {

		JustBeforeEach(func() {
			baseVMConfigSpec = vmlifecycle.GetOVFVAppConfigForConfigSpec(
				configInfo,
				vAppConfigSpec,
				bsArgs.VAppData,
				bsArgs.VAppExData,
				nil)
		})

		Context("Empty input", func() {
			It("No changes", func() {
				Expect(baseVMConfigSpec).To(BeNil())
			})
		})

		Context("Update to user configurable field", func() {
			BeforeEach(func() {
				bsArgs.VAppData["foo"] = "fooval"
				configInfo.VAppConfig = &vimTypes.VmConfigInfo{
					Property: []vimTypes.VAppPropertyInfo{
						{
							Id:               "foo",
							Value:            "should-change",
							UserConfigurable: pointer.Bool(true),
						},
					},
				}
			})

			It("Updates configSpec.VAppConfig", func() {
				Expect(baseVMConfigSpec).ToNot(BeNil())
				vmCs := baseVMConfigSpec.GetVmConfigSpec()
				Expect(vmCs).ToNot(BeNil())
				Expect(vmCs.Property).To(HaveLen(1))
				Expect(vmCs.Property[0].Info).ToNot(BeNil())
				Expect(vmCs.Property[0].Info.Value).To(Equal("fooval"))
			})
		})
	})
})
