// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var _ = Describe("CreateVirtualEthernetCard", Label(testlabels.VCSim), func() {

	const (
		macAddress = "01:02:03:04:05:06"
		externalID = "my-external-id"
	)

	var (
		ctx        *builder.TestContextForVCSim
		testConfig builder.VCSimTestConfig

		dev             network.Device
		interfaceSpec   vmopv1.VirtualMachineNetworkInterfaceSpec
		useBogusBacking bool

		ethCardDev vimtypes.BaseVirtualDevice
		err        error
	)

	BeforeEach(func() {
		testConfig = builder.VCSimTestConfig{
			NumNetworks:    1,
			WithNetworkEnv: builder.NetworkEnvVDS,
		}

		dev = network.Device{
			NetworkID:  "dummy-network-id",
			MacAddress: macAddress,
			ExternalID: externalID,
		}
		interfaceSpec = vmopv1.VirtualMachineNetworkInterfaceSpec{}
		useBogusBacking = false
	})

	JustBeforeEach(func() {
		ctx = suite.NewTestContextForVCSim(testConfig)

		if useBogusBacking {
			dev.Backing = object.NewNetwork(ctx.VCClient.Client, vimtypes.ManagedObjectReference{
				Type:  "Network",
				Value: "does-not-exist",
			})
		} else {
			dev.Backing = ctx.GetNetwork(0).Backing
		}

		ethCardDev, err = network.CreateVirtualEthernetCard(ctx, dev, interfaceSpec)
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
	})

	ethCard := func() *vimtypes.VirtualEthernetCard {
		return ethCardDev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	}

	It("sets the backing from the Device", func() {
		Expect(err).ToNot(HaveOccurred())

		backingInfo, ok := ethCard().Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo)
		Expect(ok).To(BeTrue())
		Expect(backingInfo.Port.PortgroupKey).To(Equal(ctx.GetNetwork(0).Backing.Reference().Value))
	})

	It("sets the ExternalId from the Device", func() {
		Expect(err).ToNot(HaveOccurred())
		Expect(ethCard().ExternalId).To(Equal(externalID))
	})

	When("Device.MacAddress is set", func() {
		It("sets a manual MacAddress", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCard().MacAddress).To(Equal(macAddress))
			Expect(ethCard().AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeManual)))
		})
	})

	When("Device.MacAddress is empty", func() {
		BeforeEach(func() {
			dev.MacAddress = ""
		})

		It("leaves the MacAddress unset with a generated AddressType", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCard().MacAddress).To(BeEmpty())
			Expect(ethCard().AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeGenerated)))
		})
	})

	Context("Device.Backing cannot provide EthernetCardBackingInfo", func() {
		BeforeEach(func() {
			useBogusBacking = true
		})

		It("returns an error", func() {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unable to get Ethernet card backing info"))
			Expect(ethCardDev).To(BeNil())
		})
	})
})

var _ = Describe("UpdateVMClassEthCardFromDevice", Label(testlabels.VCSim), func() {

	const (
		macAddress = "01:02:03:04:05:06"
		externalID = "my-external-id"
	)

	var (
		ctx        *builder.TestContextForVCSim
		testConfig builder.VCSimTestConfig

		dev             network.Device
		ethCard         *vimtypes.VirtualEthernetCard
		useBogusBacking bool

		err error
	)

	BeforeEach(func() {
		testConfig = builder.VCSimTestConfig{
			NumNetworks:    1,
			WithNetworkEnv: builder.NetworkEnvVDS,
		}

		dev = network.Device{
			NetworkID:  "dummy-network-id",
			MacAddress: macAddress,
			ExternalID: externalID,
		}
		useBogusBacking = false

		ethCard = &vimtypes.VirtualEthernetCard{}
	})

	JustBeforeEach(func() {
		ctx = suite.NewTestContextForVCSim(testConfig)

		if useBogusBacking {
			dev.Backing = object.NewNetwork(ctx.VCClient.Client, vimtypes.ManagedObjectReference{
				Type:  "Network",
				Value: "does-not-exist",
			})
		} else {
			dev.Backing = ctx.GetNetwork(0).Backing
		}

		err = network.UpdateVMClassEthCardFromDevice(ctx, dev, ethCard)
	})

	AfterEach(func() {
		ctx.AfterEach()
		ctx = nil
	})

	It("sets the backing from the Device", func() {
		Expect(err).ToNot(HaveOccurred())

		backingInfo, ok := ethCard.Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo)
		Expect(ok).To(BeTrue())
		Expect(backingInfo.Port.PortgroupKey).To(Equal(ctx.GetNetwork(0).Backing.Reference().Value))
	})

	It("sets the ExternalId from the Device", func() {
		Expect(err).ToNot(HaveOccurred())
		Expect(ethCard.ExternalId).To(Equal(externalID))
	})

	When("Device.MacAddress is set", func() {
		It("sets a manual MacAddress", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCard.MacAddress).To(Equal(macAddress))
			Expect(ethCard.AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeManual)))
		})
	})

	When("Device.MacAddress is empty", func() {
		BeforeEach(func() {
			dev.MacAddress = ""
		})

		Context("and the existing card has no MacAddress/AddressType set", func() {
			It("leaves MacAddress and AddressType unset", func() {
				Expect(err).ToNot(HaveOccurred())
				Expect(ethCard.MacAddress).To(BeEmpty())
				Expect(ethCard.AddressType).To(BeEmpty())
			})
		})

		Context("and the existing card already has a MacAddress/AddressType", func() {
			BeforeEach(func() {
				ethCard.MacAddress = "aa:bb:cc:dd:ee:ff"
				ethCard.AddressType = string(vimtypes.VirtualEthernetCardMacTypeAssigned)
			})

			It("leaves the existing MacAddress and AddressType untouched", func() {
				// Per the BMV comment on UpdateVMClassEthCardFromDevice, an
				// empty Device.MacAddress intentionally does not clear or
				// overwrite whatever the class-provided card already had.
				Expect(err).ToNot(HaveOccurred())
				Expect(ethCard.MacAddress).To(Equal("aa:bb:cc:dd:ee:ff"))
				Expect(ethCard.AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeAssigned)))
			})
		})
	})

	Context("Device.Backing cannot provide EthernetCardBackingInfo", func() {
		BeforeEach(func() {
			useBogusBacking = true
		})

		It("returns an error and does not modify the ethCard", func() {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unable to get Ethernet card backing info"))
			Expect(ethCard.Backing).To(BeNil())
			Expect(ethCard.ExternalId).To(BeEmpty())
		})
	})
})

var _ = Describe("MapEthernetDevicesToSpecIdx", func() {

	var (
		client      ctrlclient.Client
		initObjs    []ctrlclient.Object
		vmCtx       pkgctx.VirtualMachineContext
		devices     object.VirtualDeviceList
		devKeyToIdx map[int32]int
	)

	BeforeEach(func() {
		vm := &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "map-eth-dev-test",
				Namespace: "map-eth-dev-test",
			},
			Spec: vmopv1.VirtualMachineSpec{
				Network: &vmopv1.VirtualMachineNetworkSpec{},
			},
		}

		vmCtx = pkgctx.VirtualMachineContext{
			Context: pkgcfg.NewContextWithDefaultConfig(),
			Logger:  suite.GetLogger().WithName("map_eth_devices"),
			VM:      vm,
		}
	})

	JustBeforeEach(func() {
		client = builder.NewFakeClient(initObjs...)

		vmMo := mo.VirtualMachine{
			Config: &vimtypes.VirtualMachineConfigInfo{
				Hardware: vimtypes.VirtualHardware{
					Device: devices,
				},
			},
		}
		devKeyToIdx = network.MapEthernetDevicesToSpecIdx(vmCtx, client, vmMo)
	})

	AfterEach(func() {
		initObjs = nil
		devices = nil
	})

	Context("Mutable networks is not enable", func() {
		BeforeEach(func() {
			pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
				config.Features.MutableNetworks = false
			})
		})

		Context("Zips devices and interfaces together", func() {
			BeforeEach(func() {
				dev1 := &vimtypes.VirtualVmxnet3{}
				dev1.Key = 4000
				dev2 := &vimtypes.VirtualE1000e{}
				dev2.Key = 4001
				devices = append(devices, dev1, dev2)

				vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name: "eth0",
					},
					{
						Name: "eth1",
					},
				}
			})

			It("returns expected mapping", func() {
				Expect(devKeyToIdx).To(HaveLen(2))
				Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4000), 0))
				Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4001), 1))
			})
		})
	})

	Context("Mutable networks is enabled", func() {
		BeforeEach(func() {
			pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
				config.Features.MutableNetworks = true
			})
		})

		Context("VDS", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.NetworkProviderType = pkgcfg.NetworkProviderTypeVDS
				})
			})

			Context("Matches", func() {
				const networkName1, networkName2, networkName3 = "network-1", "network-2", "network-3"
				const backing1, backing2, backing3 = "dvpg-1", "dvpg-2", "dvpg-3"
				const externalID = "extid-1"
				const macAddress = "f8:e4:3b:7e:88:ca"

				BeforeEach(func() {
					dev1 := &vimtypes.VirtualVmxnet3{}
					dev1.Key = 4000
					dev1.Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: backing1,
						},
					}
					dev2 := &vimtypes.VirtualE1000e{}
					dev2.Key = 4001
					dev2.ExternalId = externalID
					dev2.Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: backing2,
						},
					}
					dev3 := &vimtypes.VirtualVmxnet2{}
					dev3.Key = 4002
					dev3.Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: backing2,
						},
					}
					dev4 := &vimtypes.VirtualVmxnet3{}
					dev4.Key = 4003
					dev4.MacAddress = macAddress
					dev4.Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: backing3,
						},
					}
					devices = append(devices, dev1, dev2, dev3, dev4)

					netIf1 := &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NetOPCRName(vmCtx.VM.Name, networkName1, "eth0", false),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: netopv1alpha1.NetworkInterfaceStatus{
							NetworkID: backing1,
						},
					}
					netIf2 := &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NetOPCRName(vmCtx.VM.Name, networkName2, "eth1", false),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: netopv1alpha1.NetworkInterfaceStatus{
							NetworkID: backing2,
						},
					}
					netIf3 := &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NetOPCRName(vmCtx.VM.Name, networkName2, "eth2", false),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: netopv1alpha1.NetworkInterfaceStatus{
							ExternalID: externalID,
							NetworkID:  backing2,
						},
					}
					netIf4 := &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NetOPCRName(vmCtx.VM.Name, networkName3, "eth3", false),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: netopv1alpha1.NetworkInterfaceStatus{
							NetworkID:  backing3,
							MacAddress: macAddress,
						},
					}
					initObjs = append(initObjs, netIf1, netIf2, netIf3, netIf4)

					vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
						{
							Name: "eth1",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName2,
								TypeMeta: netOPNetworkTypeMeta,
							},
						},
						{
							Name: "eth0",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName1,
								TypeMeta: netOPNetworkTypeMeta,
							},
						},
						{
							Name: "eth2",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName2,
								TypeMeta: netOPNetworkTypeMeta,
							},
						},
						{
							Name: "eth3",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName3,
								TypeMeta: netOPNetworkTypeMeta,
							},
							MACAddr: macAddress,
						},
					}
				})

				It("returns expected mapping", func() {
					Expect(devKeyToIdx).To(HaveLen(4))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4000), 1))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4001), 2))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4002), 0))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4003), 3))
				})
			})
		})

		Context("NSXT", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.NetworkProviderType = pkgcfg.NetworkProviderTypeNSXT
				})
			})

			Context("Matches", func() {
				const networkName1, networkName2 = "network-1", "network-2"
				const externalID1, macAddress2 = "extid-1", "macaddr-2"

				BeforeEach(func() {
					dev1 := &vimtypes.VirtualVmxnet3{}
					dev1.Key = 4000
					dev1.ExternalId = externalID1
					dev2 := &vimtypes.VirtualE1000e{}
					dev2.Key = 4001
					dev2.MacAddress = macAddress2
					devices = append(devices, dev1, dev2)

					netIf1 := &ncpv1alpha1.VirtualNetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NCPCRName(vmCtx.VM.Name, networkName1, "eth0", false),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: ncpv1alpha1.VirtualNetworkInterfaceStatus{
							InterfaceID: externalID1,
						},
					}
					netIf2 := &ncpv1alpha1.VirtualNetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NCPCRName(vmCtx.VM.Name, networkName2, "eth1", false),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: ncpv1alpha1.VirtualNetworkInterfaceStatus{
							MacAddress: macAddress2,
						},
					}
					initObjs = append(initObjs, netIf1, netIf2)

					vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
						{
							Name: "eth1",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName2,
								TypeMeta: nsxtNetworkTypeMeta,
							},
						},
						{
							Name: "eth0",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName1,
								TypeMeta: nsxtNetworkTypeMeta,
							},
						},
					}
				})

				It("returns expected mapping", func() {
					Expect(devKeyToIdx).To(HaveLen(2))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4000), 1))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4001), 0))
				})
			})
		})

		Context("VPC", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.NetworkProviderType = pkgcfg.NetworkProviderTypeVPC
				})
			})

			Context("Matches", func() {
				const networkName1, networkName2, networkName3 = "network-1", "network-2", "network-3"
				const externalID1, macAddress2 = "extid-1", "macaddr-2"
				const externalID3, macAddress3 = "extid-3", "macaddr-3"

				BeforeEach(func() {
					dev1 := &vimtypes.VirtualVmxnet3{}
					dev1.Key = 4000
					dev1.ExternalId = externalID1
					dev2 := &vimtypes.VirtualE1000e{}
					dev2.Key = 4001
					dev2.MacAddress = macAddress2
					dev3 := &vimtypes.VirtualE1000e{}
					dev3.Key = 4002
					dev3.ExternalId = externalID3
					dev3.MacAddress = macAddress3
					devices = append(devices, dev1, dev2, dev3)

					netIf1 := &vpcv1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.VPCCRName(vmCtx.VM.Name, networkName1, "eth0"),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: vpcv1alpha1.SubnetPortStatus{
							Attachment: vpcv1alpha1.PortAttachment{
								ID: externalID1,
							},
						},
					}
					netIf2 := &vpcv1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.VPCCRName(vmCtx.VM.Name, networkName2, "eth1"),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: vpcv1alpha1.SubnetPortStatus{
							NetworkInterfaceConfig: vpcv1alpha1.NetworkInterfaceConfig{
								MACAddress: macAddress2,
							},
						},
					}
					netIf3 := &vpcv1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.VPCCRName(vmCtx.VM.Name, networkName3, "eth2"),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: vpcv1alpha1.SubnetPortStatus{
							Attachment: vpcv1alpha1.PortAttachment{
								ID: externalID3,
							},
						},
					}
					initObjs = append(initObjs, netIf1, netIf2, netIf3)

					vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
						{
							Name: "eth1",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName2,
								TypeMeta: vpcNetworkTypeMeta,
							},
						},
						{
							Name: "eth2",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName3,
								TypeMeta: vpcNetworkTypeMeta,
							},
						},
						{
							Name: "eth0",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName1,
								TypeMeta: vpcNetworkTypeMeta,
							},
						},
					}
				})

				It("returns expected mapping", func() {
					Expect(devKeyToIdx).To(HaveLen(3))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4000), 2))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4001), 0))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4002), 1))
				})
			})

			Context("With VPC ignore MAC address", func() {
				const networkName = "network-1"
				const externalID = "extid-1"
				const devMacAddress = "00:50:56:00:00:01"

				BeforeEach(func() {
					dev1 := &vimtypes.VirtualVmxnet3{}
					dev1.Key = 4000
					dev1.ExternalId = externalID
					dev1.MacAddress = devMacAddress
					devices = append(devices, dev1)

					netIf1 := &vpcv1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.VPCCRName(vmCtx.VM.Name, networkName, "eth0"),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: vpcv1alpha1.SubnetPortStatus{
							Attachment: vpcv1alpha1.PortAttachment{
								ID: externalID,
							},
							NetworkInterfaceConfig: vpcv1alpha1.NetworkInterfaceConfig{
								MACAddress: "00:00:00:00:00:00",
							},
						},
					}
					initObjs = append(initObjs, netIf1)

					vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
						{
							Name: "eth0",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName,
								TypeMeta: vpcNetworkTypeMeta,
							},
						},
					}
				})

				It("returns expected mapping", func() {
					Expect(devKeyToIdx).To(HaveLen(1))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4000), 0))
				})
			})
		})

		Context("Named", func() {
			const networkName = "my-network"

			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.NetworkProviderType = pkgcfg.NetworkProviderTypeNamed
				})

				dev1 := &vimtypes.VirtualVmxnet3{}
				dev1.Key = 4000
				dev1.Backing = &vimtypes.VirtualEthernetCardNetworkBackingInfo{
					VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
						DeviceName: networkName,
					},
				}
				devices = append(devices, dev1)

				// Note the interface's Network has no APIVersion/TypeMeta set,
				// as is the case for a Named network.
				vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name: "eth0",
						Network: &vmopv1common.PartialObjectRef{
							Name: networkName,
						},
					},
				}
			})

			Context("PerNamespaceNetworkProvider is disabled", func() {
				BeforeEach(func() {
					pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
						config.Features.PerNamespaceNetworkProvider = false
					})
				})

				It("matches by device name", func() {
					Expect(devKeyToIdx).To(HaveLen(1))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4000), 0))
				})

				Context("and the interface spec MACAddr does not match the device", func() {
					BeforeEach(func() {
						vmCtx.VM.Spec.Network.Interfaces[0].MACAddr = "aa:bb:cc:dd:ee:ff"
					})

					It("returns no mapping", func() {
						Expect(devKeyToIdx).To(BeEmpty())
					})
				})
			})

			Context("PerNamespaceNetworkProvider is enabled", func() {
				BeforeEach(func() {
					pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
						config.Features.PerNamespaceNetworkProvider = true
					})
				})

				It("does not use the Named matcher and returns no mapping", func() {
					// With PerNamespaceNetworkProvider enabled, dispatch is by
					// the interface's API group. Since a Named network has no
					// APIVersion set, no matcher applies.
					Expect(devKeyToIdx).To(BeEmpty())
				})
			})
		})

		Context("Interface's Network has no recognized API group", func() {
			BeforeEach(func() {
				dev1 := &vimtypes.VirtualVmxnet3{}
				dev1.Key = 4000
				devices = append(devices, dev1)

				vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name: "eth0",
						Network: &vmopv1common.PartialObjectRef{
							Name: "some-network",
							TypeMeta: metav1.TypeMeta{
								APIVersion: "unknown.example.com/v1",
							},
						},
					},
				}
			})

			It("returns no mapping", func() {
				Expect(devKeyToIdx).To(BeEmpty())
			})
		})

		Context("Mixed VDS and VPC", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.Features.PerNamespaceNetworkProvider = true
				})
			})

			Context("Matches", func() {
				const networkName1, networkName2 = "network-1", "network-2"
				const backing1 = "dvpg-1"
				const externalID2 = "extid-2"

				BeforeEach(func() {
					dev1 := &vimtypes.VirtualVmxnet3{}
					dev1.Key = 4000
					dev1.Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: backing1,
						},
					}
					dev2 := &vimtypes.VirtualE1000e{}
					dev2.Key = 4001
					dev2.ExternalId = externalID2
					devices = append(devices, dev1, dev2)

					netIf1 := &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.NetOPCRName(vmCtx.VM.Name, networkName1, "eth0", false),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: netopv1alpha1.NetworkInterfaceStatus{
							NetworkID: backing1,
						},
					}
					subnetPort2 := &vpcv1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Name:      network.VPCCRName(vmCtx.VM.Name, networkName2, "eth1"),
							Namespace: vmCtx.VM.Namespace,
						},
						Status: vpcv1alpha1.SubnetPortStatus{
							Attachment: vpcv1alpha1.PortAttachment{
								ID: externalID2,
							},
						},
					}
					initObjs = append(initObjs, netIf1, subnetPort2)

					vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
						{
							Name: "eth1",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName2,
								TypeMeta: vpcNetworkTypeMeta,
							},
						},
						{
							Name: "eth0",
							Network: &vmopv1common.PartialObjectRef{
								Name:     networkName1,
								TypeMeta: netOPNetworkTypeMeta,
							},
						},
					}
				})

				It("returns expected mapping", func() {
					Expect(devKeyToIdx).To(HaveLen(2))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4000), 1))
					Expect(devKeyToIdx).To(HaveKeyWithValue(int32(4001), 0))
				})
			})
		})
	})
})

const (
	charVMName      = "map-eth-char"
	charNamespace   = "map-eth-char-ns"
	charNetworkName = "network-a"
)

// Characterization (spec 017 T012): pins pre-restructuring behavior.
var _ = Describe("MapEthernetDevicesToSpecIdx characterization", func() {

	const (
		vmName    = charVMName
		namespace = charNamespace
		pgA       = "dvportgroup-a"
		pgB       = "dvportgroup-b"
		netA      = charNetworkName
		extIDA    = "ext-id-a"
		extIDB    = "ext-id-b"
		macA      = "00:50:56:00:00:0a"
		macAUp    = "00:50:56:00:00:0A"
		macB      = "00:50:56:00:00:0b"
	)

	var (
		initObjs    []ctrlclient.Object
		vmCtx       pkgctx.VirtualMachineContext
		devices     object.VirtualDeviceList
		devKeyToIdx map[int32]int
	)

	newCard := func(key int32, unitNumber int32, backing vimtypes.BaseVirtualDeviceBackingInfo) *vimtypes.VirtualVmxnet3 {
		card := &vimtypes.VirtualVmxnet3{}
		card.Key = key
		card.UnitNumber = ptr.To(unitNumber)
		card.Backing = backing
		return card
	}

	interfaces := func(n int) []vmopv1.VirtualMachineNetworkInterfaceSpec {
		var ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec
		for i := range n {
			ifaces = append(ifaces, vmopv1.VirtualMachineNetworkInterfaceSpec{
				Name: fmt.Sprintf("eth%d", i),
			})
		}
		return ifaces
	}

	BeforeEach(func() {
		initObjs = nil
		devices = nil

		vmCtx = pkgctx.VirtualMachineContext{
			Context: pkgcfg.NewContextWithDefaultConfig(),
			Logger:  suite.GetLogger().WithName("map_eth_devices_char"),
			VM: &vmopv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vmName,
					Namespace: namespace,
				},
				Spec: vmopv1.VirtualMachineSpec{
					Network: &vmopv1.VirtualMachineNetworkSpec{},
				},
			},
		}
	})

	JustBeforeEach(func() {
		devKeyToIdx = network.MapEthernetDevicesToSpecIdx(
			vmCtx,
			builder.NewFakeClient(initObjs...),
			mo.VirtualMachine{
				Config: &vimtypes.VirtualMachineConfigInfo{
					Hardware: vimtypes.VirtualHardware{Device: devices},
				},
			})
	})

	It("returns nil when the VM has no network spec", func() {
		vmCtx.VM.Spec.Network = nil
		Expect(network.MapEthernetDevicesToSpecIdx(vmCtx, builder.NewFakeClient(), mo.VirtualMachine{
			Config: &vimtypes.VirtualMachineConfigInfo{},
		})).To(BeNil())
	})

	It("returns nil when the VM has no config", func() {
		Expect(network.MapEthernetDevicesToSpecIdx(vmCtx, builder.NewFakeClient(), mo.VirtualMachine{})).To(BeNil())
	})

	Context("MutableNetworks is disabled (positional zip)", func() {
		BeforeEach(func() {
			pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
				config.Features.MutableNetworks = false
			})
		})

		DescribeTable("zips spec interfaces with cards in device order, truncated to the shorter list",
			func(numCards, numInterfaces int, expected map[int32]int) {
				for i := range numCards {
					// All cards are indistinguishable: same backing, no CRs.
					devices = append(devices, newCard(int32(4000+i), int32(7+i), charDVPGBacking("", pgA)))
				}
				vmCtx.VM.Spec.Network.Interfaces = interfaces(numInterfaces)

				devKeyToIdx = network.MapEthernetDevicesToSpecIdx(
					vmCtx,
					builder.NewFakeClient(),
					mo.VirtualMachine{
						Config: &vimtypes.VirtualMachineConfigInfo{
							Hardware: vimtypes.VirtualHardware{Device: devices},
						},
					})
				if len(expected) == 0 {
					Expect(devKeyToIdx).To(BeEmpty())
				} else {
					Expect(devKeyToIdx).To(Equal(expected))
				}
			},
			Entry("equal counts", 2, 2, map[int32]int{4000: 0, 4001: 1}),
			Entry("more cards than interfaces", 3, 2, map[int32]int{4000: 0, 4001: 1}),
			Entry("more interfaces than cards", 1, 3, map[int32]int{4000: 0}),
			Entry("no cards", 0, 2, map[int32]int{}),
		)

		When("non-Ethernet devices are interleaved with the cards", func() {
			BeforeEach(func() {
				disk := &vimtypes.VirtualDisk{}
				disk.Key = 2000
				devices = append(devices,
					newCard(4000, 7, charDVPGBacking("", pgA)),
					disk,
					newCard(4001, 8, charDVPGBacking("", pgA)),
				)
				vmCtx.VM.Spec.Network.Interfaces = interfaces(2)
			})

			It("zips only the Ethernet cards", func() {
				Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0, 4001: 1}))
			})
		})

		When("VMNetworkUnitNumbers is disabled and interfaces declare unit numbers", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.Features.VMNetworkUnitNumbers = false
				})
				devices = append(devices,
					newCard(4000, 7, charDVPGBacking("", pgA)),
					newCard(4001, 8, charDVPGBacking("", pgA)),
				)
				vmCtx.VM.Spec.Network.Interfaces = interfaces(2)
				vmCtx.VM.Spec.Network.Interfaces[0].UnitNumber = ptr.To(int32(8))
				vmCtx.VM.Spec.Network.Interfaces[1].UnitNumber = ptr.To(int32(7))
			})

			It("ignores the unit numbers and zips by position", func() {
				Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0, 4001: 1}))
			})
		})

		When("VMNetworkUnitNumbers is enabled and interfaces are unnumbered", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.Features.VMNetworkUnitNumbers = true
				})
				devices = append(devices,
					newCard(4000, 7, charDVPGBacking("", pgA)),
					newCard(4001, 8, charDVPGBacking("", pgA)),
				)
				vmCtx.VM.Spec.Network.Interfaces = interfaces(2)
			})

			It("zips by position", func() {
				Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0, 4001: 1}))
			})
		})

		// TODAY: unit numbers are not consulted. Spec 008 T020 (and spec 017
		// T014's Match) are expected to change this to exact-number matching
		// where a numbered miss gets no entry. Unlike the rest of this file,
		// this case is intentionally expected to be updated by that work.
		When("TODAY: VMNetworkUnitNumbers is enabled and interfaces declare unit numbers", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
					config.Features.VMNetworkUnitNumbers = true
				})
				devices = append(devices,
					newCard(4000, 7, charDVPGBacking("", pgA)),
					newCard(4001, 8, charDVPGBacking("", pgA)),
				)
				vmCtx.VM.Spec.Network.Interfaces = interfaces(2)
				vmCtx.VM.Spec.Network.Interfaces[0].UnitNumber = ptr.To(int32(8))
				vmCtx.VM.Spec.Network.Interfaces[1].UnitNumber = ptr.To(int32(9))
			})

			It("TODAY: ignores the unit numbers and zips by position", func() {
				Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0, 4001: 1}))
			})
		})
	})

	Context("MutableNetworks is enabled (per-interface CR matching)", func() {
		BeforeEach(func() {
			pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
				config.Features.MutableNetworks = true
				config.Features.PerNamespaceNetworkProvider = true
			})
		})

		When("a VDS NetworkInterface uses the legacy v1alpha1 name", func() {
			BeforeEach(func() {
				devices = append(devices, newCard(4000, 7, charDVPGBacking("", pgA)))
				initObjs = append(initObjs, &netopv1alpha1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{
						Name:      network.NetOPCRName(vmName, netA, "eth0", true),
						Namespace: namespace,
					},
					Status: netopv1alpha1.NetworkInterfaceStatus{NetworkID: pgA},
				})
				vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{Name: "eth0", Network: &vmopv1common.PartialObjectRef{Name: netA, TypeMeta: netOPNetworkTypeMeta}},
				}
			})

			It("matches through the legacy CR", func() {
				Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0}))
			})
		})

		DescribeTable("per-provider matching of one interface against one card",
			func(
				typeMeta metav1.TypeMeta,
				interfaceMAC string,
				card charCard,
				newCR func() ctrlclient.Object,
				expectMapped bool) {

				card.key = 4000
				devices = object.VirtualDeviceList{card.vmxnet3()}
				vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    "eth0",
						Network: &vmopv1common.PartialObjectRef{Name: netA, TypeMeta: typeMeta},
						MACAddr: interfaceMAC,
					},
				}

				devKeyToIdx = network.MapEthernetDevicesToSpecIdx(
					vmCtx,
					builder.NewFakeClient(newCR()),
					mo.VirtualMachine{
						Config: &vimtypes.VirtualMachineConfigInfo{
							Hardware: vimtypes.VirtualHardware{Device: devices},
						},
					})

				if expectMapped {
					Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0}))
				} else {
					Expect(devKeyToIdx).To(BeEmpty())
				}
			},

			// VDS: external ID when the CR has one, never a card external ID
			// when the CR has none, interface MACAddr when set, and the
			// portgroup key or standard network MoRef.
			Entry("VDS: portgroup matches the CR NetworkID",
				netOPNetworkTypeMeta, "",
				charCard{backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				true),
			Entry("VDS: portgroup differs from the CR NetworkID",
				netOPNetworkTypeMeta, "",
				charCard{backing: charDVPGBacking("", pgB)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				false),
			Entry("VDS: CR external ID equals the card external ID",
				netOPNetworkTypeMeta, "",
				charCard{externalID: extIDA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, extIDA) },
				true),
			Entry("VDS: CR external ID differs from the card external ID",
				netOPNetworkTypeMeta, "",
				charCard{externalID: extIDB, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, extIDA) },
				false),
			Entry("VDS: card has an external ID but the CR does not",
				netOPNetworkTypeMeta, "",
				charCard{externalID: extIDA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				false),
			Entry("VDS: interface MACAddr matches the card case-insensitively",
				netOPNetworkTypeMeta, macAUp,
				charCard{mac: macA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				true),
			Entry("VDS: interface MACAddr differs from the card",
				netOPNetworkTypeMeta, macB,
				charCard{mac: macA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				false),
			Entry("VDS: standard portgroup Network MoRef matches the CR NetworkID",
				netOPNetworkTypeMeta, "",
				charCard{backing: &vimtypes.VirtualEthernetCardNetworkBackingInfo{
					Network: &vimtypes.ManagedObjectReference{Type: "Network", Value: "network-1"},
				}},
				func() ctrlclient.Object { return charVDSCR("network-1", "") },
				true),

			// NSX-T: both the interface ID and the MAC must match.
			Entry("NSX-T: interface ID and MAC match",
				nsxtNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macA},
				func() ctrlclient.Object { return charNSXTCR(extIDA, macAUp) },
				true),
			Entry("NSX-T: MAC differs",
				nsxtNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macB},
				func() ctrlclient.Object { return charNSXTCR(extIDA, macA) },
				false),
			Entry("NSX-T: interface ID differs",
				nsxtNetworkTypeMeta, "",
				charCard{externalID: extIDB, mac: macA},
				func() ctrlclient.Object { return charNSXTCR(extIDA, macA) },
				false),

			// VPC: the attachment ID must match; the MAC must match when the
			// SubnetPort reports one other than the all-zero placeholder.
			Entry("VPC: attachment ID and MAC match",
				vpcNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macA},
				func() ctrlclient.Object { return charVPCCR(extIDA, macAUp) },
				true),
			Entry("VPC: attachment ID matches and the SubnetPort has no MAC",
				vpcNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macA},
				func() ctrlclient.Object { return charVPCCR(extIDA, "") },
				true),
			Entry("VPC: MAC differs",
				vpcNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macB},
				func() ctrlclient.Object { return charVPCCR(extIDA, macA) },
				false),
			Entry("VPC: attachment ID differs",
				vpcNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macA},
				func() ctrlclient.Object { return charVPCCR(extIDB, macA) },
				false),
		)
	})
})

func charVDSCR(networkID, externalID string) *netopv1alpha1.NetworkInterface {
	return &netopv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name:      network.NetOPCRName(charVMName, charNetworkName, "eth0", false),
			Namespace: charNamespace,
		},
		Status: netopv1alpha1.NetworkInterfaceStatus{
			NetworkID:  networkID,
			ExternalID: externalID,
		},
	}
}

func charNSXTCR(interfaceID, mac string) *ncpv1alpha1.VirtualNetworkInterface {
	return &ncpv1alpha1.VirtualNetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name:      network.NCPCRName(charVMName, charNetworkName, "eth0", false),
			Namespace: charNamespace,
		},
		Status: ncpv1alpha1.VirtualNetworkInterfaceStatus{
			InterfaceID: interfaceID,
			MacAddress:  mac,
		},
	}
}

func charVPCCR(attachmentID, mac string) *vpcv1alpha1.SubnetPort {
	return &vpcv1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      network.VPCCRName(charVMName, charNetworkName, "eth0"),
			Namespace: charNamespace,
		},
		Status: vpcv1alpha1.SubnetPortStatus{
			Attachment: vpcv1alpha1.PortAttachment{ID: attachmentID},
			NetworkInterfaceConfig: vpcv1alpha1.NetworkInterfaceConfig{
				MACAddress: mac,
			},
		},
	}
}
