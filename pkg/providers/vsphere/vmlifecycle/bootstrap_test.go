// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle_test

import (
	"context"
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1sysprep "github.com/vmware-tanzu/vm-operator/api/v1alpha6/sysprep"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/internal"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/vmlifecycle"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var _ = Describe("Customization utils", func() {
	Context("IsPending", func() {
		var extraConfig []vimtypes.BaseOptionValue
		var pending bool

		BeforeEach(func() {
			extraConfig = nil
		})

		JustBeforeEach(func() {
			pending = vmlifecycle.IsCustomizationPendingExtraConfig(extraConfig)
		})

		Context("Empty ExtraConfig", func() {
			It("not pending", func() {
				Expect(pending).To(BeFalse())
			})
		})

		Context("ExtraConfig with pending key", func() {
			BeforeEach(func() {
				extraConfig = append(extraConfig, &vimtypes.OptionValue{
					Key:   constants.GOSCPendingExtraConfigKey,
					Value: "/foo/bar",
				})
			})

			It("is pending", func() {
				Expect(pending).To(BeTrue())
			})
		})
	})
})

var _ = Describe("SanitizeConfigSpec", func() {
	var (
		inConfigSpec, outConfigSpec vimtypes.VirtualMachineConfigSpec
	)

	BeforeEach(func() {
		inConfigSpec = vimtypes.VirtualMachineConfigSpec{}
	})

	JustBeforeEach(func() {
		outConfigSpec = vmlifecycle.SanitizeConfigSpec(inConfigSpec)
	})

	When("EC CloudInitGuestInfoUserdata", func() {
		BeforeEach(func() {
			inConfigSpec.ExtraConfig = append(inConfigSpec.ExtraConfig, &vimtypes.OptionValue{
				Key:   constants.CloudInitGuestInfoUserdata,
				Value: "value",
			})
		})

		It("redacts value", func() {
			Expect(inConfigSpec.ExtraConfig).To(HaveLen(1))
			Expect(inConfigSpec.ExtraConfig[0].GetOptionValue().Key).To(Equal(constants.CloudInitGuestInfoUserdata))
			Expect(inConfigSpec.ExtraConfig[0].GetOptionValue().Value).To(Equal("value"))

			Expect(outConfigSpec.ExtraConfig).To(HaveLen(1))
			Expect(outConfigSpec.ExtraConfig[0].GetOptionValue().Key).To(Equal(constants.CloudInitGuestInfoUserdata))
			Expect(outConfigSpec.ExtraConfig[0].GetOptionValue().Value).To(Equal("***"))
		})
	})

	When("vAppConfig user property", func() {
		BeforeEach(func() {
			inConfigSpec.VAppConfig = &vimtypes.VmConfigSpec{
				Property: []vimtypes.VAppPropertySpec{
					{
						Info: &vimtypes.VAppPropertyInfo{
							UserConfigurable: vimtypes.NewBool(true),
							Value:            "value",
						},
					},
				},
			}
		})

		It("redacts value", func() {
			vmConfigSpec := inConfigSpec.VAppConfig.GetVmConfigSpec()
			Expect(vmConfigSpec).ToNot(BeNil())
			Expect(vmConfigSpec.Property).To(HaveLen(1))
			Expect(vmConfigSpec.Property[0].Info.Value).To(Equal("value"))

			vmConfigSpec = outConfigSpec.VAppConfig.GetVmConfigSpec()
			Expect(vmConfigSpec).ToNot(BeNil())
			Expect(vmConfigSpec.Property).To(HaveLen(1))
			Expect(vmConfigSpec.Property[0].Info.Value).To(Equal("***"))
		})
	})
})

var _ = Describe("SanitizeCustomizationSpec", func() {
	var (
		inCustSpec, outCustSpec vimtypes.CustomizationSpec
	)

	BeforeEach(func() {
		inCustSpec = vimtypes.CustomizationSpec{}
	})

	JustBeforeEach(func() {
		outCustSpec = vmlifecycle.SanitizeCustomizationSpec(inCustSpec)
	})

	When("CustomizationCloudinitPrep", func() {
		BeforeEach(func() {
			inCustSpec.Identity = &internal.CustomizationCloudinitPrep{
				Metadata: "metadata",
				Userdata: "userdata",
			}
		})

		It("redacts userdata", func() {
			Expect(inCustSpec.Identity).ToNot(BeNil())
			c := inCustSpec.Identity.(*internal.CustomizationCloudinitPrep)
			Expect(c.Metadata).To(Equal("metadata"))
			Expect(c.Userdata).To(Equal("userdata"))

			Expect(outCustSpec.Identity).ToNot(BeNil())
			c = outCustSpec.Identity.(*internal.CustomizationCloudinitPrep)
			Expect(c.Metadata).To(Equal("metadata"))
			Expect(c.Userdata).To(Equal("***"))
		})
	})

	When("CustomizationLinuxPrep", func() {
		BeforeEach(func() {
			inCustSpec.Identity = &vimtypes.CustomizationLinuxPrep{
				Password: &vimtypes.CustomizationPassword{
					Value: "value",
				},
				ScriptText: "value",
			}
		})

		It("redacts fields", func() {
			Expect(inCustSpec.Identity).ToNot(BeNil())
			s := inCustSpec.Identity.(*vimtypes.CustomizationLinuxPrep)
			Expect(s.Password).ToNot(BeNil())
			Expect(s.Password.Value).To(Equal("value"))
			Expect(s.ScriptText).To(Equal("value"))

			Expect(outCustSpec.Identity).ToNot(BeNil())
			s = outCustSpec.Identity.(*vimtypes.CustomizationLinuxPrep)
			Expect(s.Password).ToNot(BeNil())
			Expect(s.Password.Value).To(Equal("***"))
			Expect(s.ScriptText).To(Equal("***"))
		})
	})

	When("CustomizationSysprepText", func() {
		BeforeEach(func() {
			inCustSpec.Identity = &vimtypes.CustomizationSysprepText{
				Value: "value",
			}
		})

		It("redacts value", func() {
			Expect(inCustSpec.Identity).ToNot(BeNil())
			s := inCustSpec.Identity.(*vimtypes.CustomizationSysprepText)
			Expect(s.Value).To(Equal("value"))

			Expect(outCustSpec.Identity).ToNot(BeNil())
			s = outCustSpec.Identity.(*vimtypes.CustomizationSysprepText)
			Expect(s.Value).To(Equal("***"))
		})
	})

	When("CustomizationSysprep", func() {
		BeforeEach(func() {
			inCustSpec.Identity = &vimtypes.CustomizationSysprep{
				GuiUnattended: vimtypes.CustomizationGuiUnattended{
					Password: &vimtypes.CustomizationPassword{
						Value: "value",
					},
					TimeZone: 42,
				},
				UserData: vimtypes.CustomizationUserData{},
				Identification: vimtypes.CustomizationIdentification{
					DomainAdmin: "admin",
					DomainAdminPassword: &vimtypes.CustomizationPassword{
						Value: "value",
					},
				},
				ScriptText: "value",
			}
		})

		It("redacts fields", func() {
			Expect(inCustSpec.Identity).ToNot(BeNil())
			s := inCustSpec.Identity.(*vimtypes.CustomizationSysprep)
			Expect(s.GuiUnattended.TimeZone).To(BeEquivalentTo(42))
			Expect(s.GuiUnattended.Password).ToNot(BeNil())
			Expect(s.GuiUnattended.Password.Value).To(Equal("value"))
			Expect(s.Identification.DomainAdmin).To(Equal("admin"))
			Expect(s.Identification.DomainAdminPassword).ToNot(BeNil())
			Expect(s.Identification.DomainAdminPassword.Value).To(Equal("value"))
			Expect(s.ScriptText).To(Equal("value"))

			Expect(outCustSpec.Identity).ToNot(BeNil())
			s = outCustSpec.Identity.(*vimtypes.CustomizationSysprep)
			Expect(s.GuiUnattended.TimeZone).To(BeEquivalentTo(42))
			Expect(s.GuiUnattended.Password).ToNot(BeNil())
			Expect(s.GuiUnattended.Password.Value).To(Equal("***"))
			Expect(s.Identification.DomainAdmin).To(Equal("admin"))
			Expect(s.Identification.DomainAdminPassword).ToNot(BeNil())
			Expect(s.Identification.DomainAdminPassword.Value).To(Equal("***"))
			Expect(s.ScriptText).To(Equal("***"))
		})
	})
})

var _ = Describe("DoBootstrap", func() {
	// Use a VM that vcsim creates for us.
	const vcVMName = "DC0_C0_RP0_VM0"

	var (
		ctx        *builder.TestContextForVCSim
		nsInfo     builder.WorkloadNamespaceInfo
		testConfig builder.VCSimTestConfig

		bsArgs     vmlifecycle.BootstrapArgs
		bsErr      error
		vcVM       *object.VirtualMachine
		vmCtx      pkgctx.VirtualMachineContext
		configInfo *vimtypes.VirtualMachineConfigInfo
	)

	BeforeEach(func() {
		var err error

		testConfig = builder.VCSimTestConfig{}
		ctx = suite.NewTestContextForVCSim(testConfig)
		nsInfo = ctx.CreateWorkloadNamespace()

		vm := builder.DummyVirtualMachine()
		vm.Name = "bootstrap-test"
		vm.Namespace = nsInfo.Namespace

		vmCtx = pkgctx.VirtualMachineContext{
			Context: ctx,
			Logger:  suite.GetLogger().WithValues("vmName", vm.Name),
			VM:      vm,
		}

		vcVM, err = ctx.Finder.VirtualMachine(ctx, vcVMName)
		Expect(err).ToNot(HaveOccurred())
		vmCtx.VM.Status.UniqueID = vcVM.Reference().Value
		task, err := vcVM.PowerOff(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(task.Wait(ctx)).To(Succeed())

		{
			// Just remove all EthernetCards to make GOSC happy, instead of having
			// to fake more data.
			devices, err := vcVM.Device(ctx)
			Expect(err).ToNot(HaveOccurred())

			var cs vimtypes.VirtualMachineConfigSpec
			cs.DeviceChange, err = devices.SelectByType(&vimtypes.VirtualEthernetCard{}).
				ConfigSpec(vimtypes.VirtualDeviceConfigSpecOperationRemove)
			Expect(err).ToNot(HaveOccurred())
			task, err := vcVM.Reconfigure(vmCtx, cs)
			Expect(err).ToNot(HaveOccurred())
			Expect(task.Wait(ctx)).To(Succeed())
		}

		moVM := &mo.VirtualMachine{}
		Expect(vcVM.Properties(ctx, vcVM.Reference(), nil, moVM)).To(Succeed())
		configInfo = moVM.Config
	})

	JustBeforeEach(func() {
		bsErr = vmlifecycle.DoBootstrap(vmCtx, vcVM, configInfo, bsArgs)
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
		vcVM = nil
		configInfo = nil
		bsArgs = vmlifecycle.BootstrapArgs{}
	})

	Context("CloudInit", func() {
		BeforeEach(func() {
			vmCtx.VM.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{},
			}
		})

		When("Disabled is true", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.Disabled = true
			})

			It("Noop", func() {
				Expect(bsErr).ToNot(HaveOccurred())
			})
		})
	})

	Context("LinuxPrep", func() {
		BeforeEach(func() {
			vmCtx.VM.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				LinuxPrep: &vmopv1.VirtualMachineBootstrapLinuxPrepSpec{},
			}
		})

		When("Disabled is true", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.Disabled = true
			})

			It("Noop", func() {
				Expect(bsErr).ToNot(HaveOccurred())
			})
		})

		It("Customizes", func() {
			Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
		})

		When("CustomizedAtNextPowerOn is false", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.LinuxPrep.CustomizeAtNextPowerOn = ptr.To(false)
			})

			It("Does not customize", func() {
				Expect(bsErr).ToNot(HaveOccurred())
				Expect(vmCtx.VM.Spec.Bootstrap.LinuxPrep.CustomizeAtNextPowerOn).To(HaveValue(BeFalse()))
			})
		})

		When("CustomizedAtNextPowerOn is true", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.LinuxPrep.CustomizeAtNextPowerOn = ptr.To(true)
			})

			It("Customizes and toggles", func() {
				Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
				Expect(vmCtx.VM.Spec.Bootstrap.LinuxPrep.CustomizeAtNextPowerOn).To(HaveValue(BeFalse()))
			})
		})
	})

	Context("Sysprep", func() {
		BeforeEach(func() {
			vmCtx.VM.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				Sysprep: &vmopv1.VirtualMachineBootstrapSysprepSpec{
					Sysprep: &vmopv1sysprep.Sysprep{},
				},
			}
		})

		When("Disabled is true", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.Disabled = true
			})

			It("Noop", func() {
				Expect(bsErr).ToNot(HaveOccurred())
			})
		})

		It("Customizes", func() {
			Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
		})

		When("CustomizedAtNextPowerOn is false", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.Sysprep.CustomizeAtNextPowerOn = ptr.To(false)
			})

			It("Does not customize", func() {
				Expect(bsErr).ToNot(HaveOccurred())
				Expect(vmCtx.VM.Spec.Bootstrap.Sysprep.CustomizeAtNextPowerOn).To(HaveValue(BeFalse()))
			})
		})

		When("CustomizedAtNextPowerOn is true", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.Sysprep.CustomizeAtNextPowerOn = ptr.To(true)
			})

			It("Customizes and toggles", func() {
				Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
				Expect(vmCtx.VM.Spec.Bootstrap.Sysprep.CustomizeAtNextPowerOn).To(HaveValue(BeFalse()))
			})
		})
	})

	Context("vAppConfig", func() {
		BeforeEach(func() {
			vmCtx.VM.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				VAppConfig: &vmopv1.VirtualMachineBootstrapVAppConfigSpec{},
			}
		})

		When("Disabled is true", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap.Disabled = true
			})

			It("Noop", func() {
				Expect(bsErr).ToNot(HaveOccurred())
			})
		})
	})

	Context("NICsOutOfGOSCHash", func() {
		const (
			fullHashKey    = pkgconst.BootstrapHashCustomSpecAnnotationKey
			partialHashKey = pkgconst.BootstrapHashCustomSpecWithoutNICsAnnotationKey
		)

		var (
			baseCtx context.Context
			st      *network.ReconcileState
		)

		nic := func(ipCIDR string) network.Bootstrap {
			return network.Bootstrap{
				IPConfigs: []network.NetworkInterfaceIPConfig{
					{
						Gateway: "192.168.1.1",
						IPCIDR:  ipCIDR,
						IsIPv4:  true,
					},
				},
			}
		}

		// bootstrapAgain runs DoBootstrap again with the current inputs, as
		// at the next power-on after the first pass.
		bootstrapAgain := func() error {
			return vmlifecycle.DoBootstrap(vmCtx, vcVM, configInfo, bsArgs)
		}

		annotations := func() map[string]string {
			return maps.Clone(vmCtx.VM.Annotations)
		}

		// setSimGuestNICs sets the number of guest NICs that vcsim compares
		// with the length of the NicSettingMap of a customization spec, and
		// clears any pending customization so Customize can be called again.
		setSimGuestNICs := func(n int) {
			simCtx := ctx.SimulatorContext()
			simVM := simCtx.Map.Get(vcVM.Reference()).(*simulator.VirtualMachine)
			simCtx.WithLock(simVM, func() {
				simVM.Guest.Net = make([]vimtypes.GuestNicInfo, n)
				if simVM.Config.Tools != nil {
					simVM.Config.Tools.PendingCustomization = ""
				}
			})
		}

		BeforeEach(func() {
			baseCtx = vmCtx.Context
			st = &network.ReconcileState{
				Policy: network.Policy{NICsOutOfGOSCHash: true},
			}
			vmCtx.Context = network.WithContext(baseCtx, st)

			// Skip the vSphere Customize call so that repeated passes do not
			// trip vcsim's pending-customization and NIC-count checks.
			// DoBootstrap still decides whether to customize, reports it, and
			// records the hashes.
			if vmCtx.VM.Annotations == nil {
				vmCtx.VM.Annotations = map[string]string{}
			}
			vmCtx.VM.Annotations[constants.VSphereCustomizationBypassKey] =
				constants.VSphereCustomizationBypassDisable

			bsArgs.HostName = "host-1"
			bsArgs.NetBootstraps = []network.Bootstrap{nic("192.168.1.10/24")}
		})

		goscTests := func(setCustomizeAtNextPowerOn func(*bool)) {
			When("the policy is enabled", func() {
				It("customizes at first power-on and writes both hashes", func() {
					Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(fullHashKey, Not(BeEmpty())))
					Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(partialHashKey, Not(BeEmpty())))
					Expect(vmCtx.VM.Annotations[fullHashKey]).ToNot(Equal(vmCtx.VM.Annotations[partialHashKey]))
				})

				It("does not customize when nothing has changed", func() {
					Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					before := annotations()

					Expect(bootstrapAgain()).To(Succeed())
					Expect(vmCtx.VM.Annotations).To(Equal(before))
				})

				DescribeTable("does not customize for a NIC-only change and refreshes the legacy hash",
					func(change func()) {
						Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
						before := annotations()

						change()
						Expect(bootstrapAgain()).To(Succeed())
						Expect(vmCtx.VM.Annotations[partialHashKey]).To(Equal(before[partialHashKey]))
						Expect(vmCtx.VM.Annotations[fullHashKey]).ToNot(Equal(before[fullHashKey]))

						// The refreshed legacy hash matches the current inputs.
						Expect(bootstrapAgain()).To(Succeed())
					},
					Entry("an interface is added", func() {
						bsArgs.NetBootstraps = append(bsArgs.NetBootstraps, nic("192.168.1.11/24"))
					}),
					Entry("an interface is removed", func() {
						bsArgs.NetBootstraps = nil
					}),
					Entry("an interface's addressing changes", func() {
						bsArgs.NetBootstraps = []network.Bootstrap{nic("192.168.1.20/24")}
					}),
					Entry("an interface changes to DHCP", func() {
						bsArgs.NetBootstraps = []network.Bootstrap{{DHCP4: true}}
					}),
				)

				It("customizes and writes both hashes when the host name changes", func() {
					Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					before := annotations()

					bsArgs.HostName = "host-2"
					bsArgs.NetBootstraps = append(bsArgs.NetBootstraps, nic("192.168.1.11/24"))
					Expect(bootstrapAgain()).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					Expect(vmCtx.VM.Annotations[partialHashKey]).ToNot(Equal(before[partialHashKey]))
					Expect(vmCtx.VM.Annotations[fullHashKey]).ToNot(Equal(before[fullHashKey]))

					Expect(bootstrapAgain()).To(Succeed())
				})

				It("customizes when the latch is set even though nothing has changed", func() {
					Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					before := annotations()

					latch := ptr.To(true)
					setCustomizeAtNextPowerOn(latch)
					Expect(bootstrapAgain()).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					Expect(latch).To(HaveValue(BeFalse()))
					Expect(vmCtx.VM.Annotations).To(Equal(before))
				})

				When("customizeAtNextPowerOn is false", func() {
					BeforeEach(func() {
						setCustomizeAtNextPowerOn(ptr.To(false))
					})

					It("does not customize or write either hash", func() {
						Expect(bsErr).ToNot(HaveOccurred())
						Expect(vmCtx.VM.Annotations).ToNot(HaveKey(fullHashKey))
						Expect(vmCtx.VM.Annotations).ToNot(HaveKey(partialHashKey))
					})
				})

				When("the policy comes from the capability", func() {
					BeforeEach(func() {
						vmCtx.Context = pkgcfg.UpdateContext(baseCtx, func(config *pkgcfg.Config) {
							config.Features.VMNetworkHotPlug = true
						})
					})

					It("writes both hashes", func() {
						_, ok := network.FromContext(vmCtx)
						Expect(ok).To(BeFalse())
						Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
						Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(partialHashKey, Not(BeEmpty())))
					})
				})

				When("the VM is customized by vSphere", func() {
					BeforeEach(func() {
						delete(vmCtx.VM.Annotations, constants.VSphereCustomizationBypassKey)
						setSimGuestNICs(1)
					})

					It("sends the full current NicSettingMap", func() {
						Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))

						// vcsim rejects a spec whose NicSettingMap length
						// differs from the guest NIC count, so this proves
						// the cleared map used for the hash is not sent.
						bsArgs.HostName = "host-2"
						bsArgs.NetBootstraps = append(bsArgs.NetBootstraps, nic("192.168.1.11/24"))
						setSimGuestNICs(2)
						Expect(bootstrapAgain()).To(MatchError(vmlifecycle.ErrBootstrapCustomize))

						// Check that vcsim enforces the NIC count.
						bsArgs.HostName = "host-3"
						setSimGuestNICs(3)
						err := bootstrapAgain()
						Expect(err).To(HaveOccurred())
						Expect(err.Error()).To(ContainSubstring("bootstrap customize failed"))
					})
				})
			})

			When("the policy is enabled on a VM with only the legacy hash", func() {
				BeforeEach(func() {
					st.Policy.NICsOutOfGOSCHash = false
				})

				It("does not customize when nothing has changed and writes both hashes", func() {
					Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					Expect(vmCtx.VM.Annotations).ToNot(HaveKey(partialHashKey))
					before := annotations()

					st.Policy.NICsOutOfGOSCHash = true
					Expect(bootstrapAgain()).To(Succeed())
					Expect(vmCtx.VM.Annotations[fullHashKey]).To(Equal(before[fullHashKey]))
					Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(partialHashKey, Not(BeEmpty())))

					// A later NIC-only change does not customize.
					bsArgs.NetBootstraps = append(bsArgs.NetBootstraps, nic("192.168.1.11/24"))
					Expect(bootstrapAgain()).To(Succeed())
				})

				It("customizes when the inputs changed and writes both hashes", func() {
					Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					before := annotations()

					st.Policy.NICsOutOfGOSCHash = true
					bsArgs.NetBootstraps = append(bsArgs.NetBootstraps, nic("192.168.1.11/24"))
					Expect(bootstrapAgain()).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					Expect(vmCtx.VM.Annotations[fullHashKey]).ToNot(Equal(before[fullHashKey]))
					Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(partialHashKey, Not(BeEmpty())))

					Expect(bootstrapAgain()).To(Succeed())
				})
			})

			When("the policy is disabled", func() {
				BeforeEach(func() {
					st.Policy.NICsOutOfGOSCHash = false
				})

				It("customizes for a NIC-only change and does not write the without-NICs hash", func() {
					Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(fullHashKey, Not(BeEmpty())))
					Expect(vmCtx.VM.Annotations).ToNot(HaveKey(partialHashKey))
					before := annotations()

					Expect(bootstrapAgain()).To(Succeed())

					bsArgs.NetBootstraps = append(bsArgs.NetBootstraps, nic("192.168.1.11/24"))
					Expect(bootstrapAgain()).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
					Expect(vmCtx.VM.Annotations[fullHashKey]).ToNot(Equal(before[fullHashKey]))
					Expect(vmCtx.VM.Annotations).ToNot(HaveKey(partialHashKey))
				})

				When("the VM has a without-NICs hash", func() {
					BeforeEach(func() {
						vmCtx.VM.Annotations[partialHashKey] = "stale"
					})

					It("ignores it and leaves it unchanged", func() {
						Expect(bsErr).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
						Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(partialHashKey, "stale"))

						bsArgs.NetBootstraps = append(bsArgs.NetBootstraps, nic("192.168.1.11/24"))
						Expect(bootstrapAgain()).To(MatchError(vmlifecycle.ErrBootstrapCustomize))
						Expect(vmCtx.VM.Annotations).To(HaveKeyWithValue(partialHashKey, "stale"))
					})
				})
			})
		}

		Context("LinuxPrep", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
					LinuxPrep: &vmopv1.VirtualMachineBootstrapLinuxPrepSpec{},
				}
			})

			goscTests(func(v *bool) {
				vmCtx.VM.Spec.Bootstrap.LinuxPrep.CustomizeAtNextPowerOn = v
			})
		})

		Context("Sysprep", func() {
			BeforeEach(func() {
				vmCtx.VM.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
					Sysprep: &vmopv1.VirtualMachineBootstrapSysprepSpec{
						Sysprep: &vmopv1sysprep.Sysprep{},
					},
				}
			})

			goscTests(func(v *bool) {
				vmCtx.VM.Spec.Bootstrap.Sysprep.CustomizeAtNextPowerOn = v
			})
		})
	})
})
