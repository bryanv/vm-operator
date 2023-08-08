// Copyright (c) 2021-2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle_test

import (
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"

	"github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/api/v1alpha2/common"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/vmlifecycle"
)

var _ = Describe("VAppConfig Bootstrap", func() {
	const key, value = "fooKey", "fooValue"

	var (
		configInfo       *types.VirtualMachineConfigInfo
		vAppConfigSpec   *vmopv1.VirtualMachineBootstrapVAppConfigSpec
		bsArgs           vmlifecycle.BootstrapArgs
		baseVMConfigSpec types.BaseVmConfigSpec
	)

	BeforeEach(func() {
		configInfo = &types.VirtualMachineConfigInfo{}
		configInfo.VAppConfig = &types.VmConfigInfo{
			Property: []types.VAppPropertyInfo{
				{
					Id:               key,
					Value:            "should-change",
					UserConfigurable: pointer.Bool(true),
				},
			},
		}

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
				bsArgs.TemplateRenderFn)
		})

		Context("Empty input", func() {
			It("No changes", func() {
				Expect(baseVMConfigSpec).To(BeNil())
			})
		})

		Context("vAppData Map", func() {
			BeforeEach(func() {
				bsArgs.VAppData[key] = value
			})

			It("Expected VAppConfig", func() {
				Expect(baseVMConfigSpec).ToNot(BeNil())

				vmCs := baseVMConfigSpec.GetVmConfigSpec()
				Expect(vmCs).ToNot(BeNil())
				Expect(vmCs.Property).To(HaveLen(1))
				Expect(vmCs.Property[0].Info).ToNot(BeNil())
				Expect(vmCs.Property[0].Info.Id).To(Equal(key))
				Expect(vmCs.Property[0].Info.Value).To(Equal(value))
			})

			Context("Applies TemplateRenderFn when specified", func() {
				BeforeEach(func() {
					bsArgs.TemplateRenderFn = func(_, v string) string {
						return strings.ToUpper(v)
					}
				})

				It("Expected VAppConfig", func() {
					Expect(baseVMConfigSpec).ToNot(BeNil())

					vmCs := baseVMConfigSpec.GetVmConfigSpec()
					Expect(vmCs).ToNot(BeNil())
					Expect(vmCs.Property).To(HaveLen(1))
					Expect(vmCs.Property[0].Info).ToNot(BeNil())
					Expect(vmCs.Property[0].Info.Id).To(Equal(key))
					Expect(vmCs.Property[0].Info.Value).To(Equal(strings.ToUpper(value)))
				})
			})
		})

		Context("vAppDataConfig Inlined Properties", func() {
			BeforeEach(func() {
				vAppConfigSpec = &vmopv1.VirtualMachineBootstrapVAppConfigSpec{
					Properties: []common.KeyValueOrSecretKeySelectorPair{
						{
							Key:   key,
							Value: common.ValueOrSecretKeySelector{Value: pointer.String(value)},
						},
					},
				}
			})

			It("Expected VAppConfig", func() {
				Expect(baseVMConfigSpec).ToNot(BeNil())

				vmCs := baseVMConfigSpec.GetVmConfigSpec()
				Expect(vmCs).ToNot(BeNil())
				Expect(vmCs.Property).To(HaveLen(1))
				Expect(vmCs.Property[0].Info).ToNot(BeNil())
				Expect(vmCs.Property[0].Info.Id).To(Equal(key))
				Expect(vmCs.Property[0].Info.Value).To(Equal(value))
			})

			Context("Applies TemplateRenderFn when specified", func() {
				BeforeEach(func() {
					bsArgs.TemplateRenderFn = func(_, v string) string {
						return strings.ToUpper(v)
					}
				})

				It("Expected VAppConfig", func() {
					Expect(baseVMConfigSpec).ToNot(BeNil())

					vmCs := baseVMConfigSpec.GetVmConfigSpec()
					Expect(vmCs).ToNot(BeNil())
					Expect(vmCs.Property).To(HaveLen(1))
					Expect(vmCs.Property[0].Info).ToNot(BeNil())
					Expect(vmCs.Property[0].Info.Id).To(Equal(key))
					Expect(vmCs.Property[0].Info.Value).To(Equal(strings.ToUpper(value)))
				})
			})
		})

		Context("vAppDataConfig From Properties", func() {
			const secretName = "my-other-secret"

			BeforeEach(func() {
				vAppConfigSpec = &vmopv1.VirtualMachineBootstrapVAppConfigSpec{
					Properties: []common.KeyValueOrSecretKeySelectorPair{
						{
							Key: key,
							Value: common.ValueOrSecretKeySelector{
								From: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
									Key:                  key,
									Optional:             nil, // TODO: Rethink if we really need this complexity
								},
							},
						},
					},
				}

				bsArgs.VAppExData[secretName] = map[string]string{key: value}
			})

			It("Expected VAppConfig", func() {
				Expect(baseVMConfigSpec).ToNot(BeNil())

				vmCs := baseVMConfigSpec.GetVmConfigSpec()
				Expect(vmCs).ToNot(BeNil())
				Expect(vmCs.Property).To(HaveLen(1))
				Expect(vmCs.Property[0].Info).ToNot(BeNil())
				Expect(vmCs.Property[0].Info.Id).To(Equal(key))
				Expect(vmCs.Property[0].Info.Value).To(Equal(value))
			})

			Context("Applies TemplateRenderFn when specified", func() {
				BeforeEach(func() {
					bsArgs.TemplateRenderFn = func(_, v string) string {
						return strings.ToUpper(v)
					}
				})

				It("Expected VAppConfig", func() {
					Expect(baseVMConfigSpec).ToNot(BeNil())

					vmCs := baseVMConfigSpec.GetVmConfigSpec()
					Expect(vmCs).ToNot(BeNil())
					Expect(vmCs.Property).To(HaveLen(1))
					Expect(vmCs.Property[0].Info).ToNot(BeNil())
					Expect(vmCs.Property[0].Info.Id).To(Equal(key))
					Expect(vmCs.Property[0].Info.Value).To(Equal(strings.ToUpper(value)))
				})
			})
		})
	})
})
