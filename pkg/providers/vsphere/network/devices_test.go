// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"context"
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

					It("Spec 017 T018: claims the card by position", func() {
						// Spec 017 T018: MapEthernetDevicesToSpecIdx now derives
						// from the shared match. The pinned MAC address rules out
						// an identity match, and the interface then claims the
						// remaining card through the shared positional fallback
						// (spec 017 Q5). Before, there was no mapping.
						Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0}))
					})
				})
			})

			Context("PerNamespaceNetworkProvider is enabled", func() {
				BeforeEach(func() {
					pkgcfg.SetContext(vmCtx, func(config *pkgcfg.Config) {
						config.Features.PerNamespaceNetworkProvider = true
					})
				})

				It("Spec 017 T018: does not use the Named matcher and claims the card by position", func() {
					// With PerNamespaceNetworkProvider enabled, dispatch is by
					// the interface's API group. Since a Named network has no
					// APIVersion set, the interface's provider resolution fails.
					// Spec 017 T018: MapEthernetDevicesToSpecIdx now derives
					// from the shared match, whose positional fallback (spec 017
					// Q5) includes interfaces whose resolution failed. Before,
					// there was no mapping.
					Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0}))
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

			It("Spec 017 T018: claims the card by position", func() {
				// Spec 017 T018: MapEthernetDevicesToSpecIdx now derives from
				// the shared match, whose positional fallback (spec 017 Q5)
				// includes interfaces whose resolution failed. Before, there
				// was no mapping.
				Expect(devKeyToIdx).To(Equal(map[int32]int{4000: 0}))
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

		// Spec 017 T018: this case was "TODAY: ignores the unit numbers and
		// zips by position". MapEthernetDevicesToSpecIdx now derives from the
		// shared match, which matches a numbered interface by its exact unit
		// number only (spec 008 G11). eth0 (unit 8) claims the card at unit 8;
		// eth1 (unit 9) has no card at its unit number, so it gets no entry and
		// the card at unit 7 is not handed to it.
		When("Spec 017 T018: VMNetworkUnitNumbers is enabled and interfaces declare unit numbers", func() {
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

			It("Spec 017 T018: matches numbered interfaces by exact unit number only", func() {
				Expect(devKeyToIdx).To(Equal(map[int32]int{4001: 0}))
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

			// Spec 017 T018: MapEthernetDevicesToSpecIdx now derives from the
			// shared match. Where the CR's reported attachment does not match
			// the card, there is no identity claim, but the one unnumbered
			// interface still claims the one card through the shared
			// positional fallback (spec 017 Q5), which applies with
			// MutableNetworks too. The entries prefixed "Spec 017 T018:" were
			// "false" (no mapping) before; the identity predicate itself is
			// covered by the FindMatchingEthCard and MatchInterfaces tests.

			// VDS: external ID when the CR has one, never a card external ID
			// when the CR has none, interface MACAddr when set, and the
			// portgroup key or standard network MoRef.
			Entry("VDS: portgroup matches the CR NetworkID",
				netOPNetworkTypeMeta, "",
				charCard{backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				true),
			Entry("Spec 017 T018: VDS: portgroup differs from the CR NetworkID",
				netOPNetworkTypeMeta, "",
				charCard{backing: charDVPGBacking("", pgB)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				true),
			Entry("VDS: CR external ID equals the card external ID",
				netOPNetworkTypeMeta, "",
				charCard{externalID: extIDA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, extIDA) },
				true),
			Entry("Spec 017 T018: VDS: CR external ID differs from the card external ID",
				netOPNetworkTypeMeta, "",
				charCard{externalID: extIDB, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, extIDA) },
				true),
			Entry("Spec 017 T018: VDS: card has an external ID but the CR does not",
				netOPNetworkTypeMeta, "",
				charCard{externalID: extIDA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				true),
			Entry("VDS: interface MACAddr matches the card case-insensitively",
				netOPNetworkTypeMeta, macAUp,
				charCard{mac: macA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				true),
			Entry("Spec 017 T018: VDS: interface MACAddr differs from the card",
				netOPNetworkTypeMeta, macB,
				charCard{mac: macA, backing: charDVPGBacking("", pgA)},
				func() ctrlclient.Object { return charVDSCR(pgA, "") },
				true),
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
			Entry("Spec 017 T018: NSX-T: MAC differs",
				nsxtNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macB},
				func() ctrlclient.Object { return charNSXTCR(extIDA, macA) },
				true),
			Entry("Spec 017 T018: NSX-T: interface ID differs",
				nsxtNetworkTypeMeta, "",
				charCard{externalID: extIDB, mac: macA},
				func() ctrlclient.Object { return charNSXTCR(extIDA, macA) },
				true),

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
			Entry("Spec 017 T018: VPC: MAC differs",
				vpcNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macB},
				func() ctrlclient.Object { return charVPCCR(extIDA, macA) },
				true),
			Entry("Spec 017 T018: VPC: attachment ID differs",
				vpcNetworkTypeMeta, "",
				charCard{externalID: extIDA, mac: macA},
				func() ctrlclient.Object { return charVPCCR(extIDB, macA) },
				true),
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

var _ = Describe("DeviceKeyToSpecIdx", Label(testlabels.API), func() {
	It("returns nil for a nil Info", func() {
		Expect(network.DeviceKeyToSpecIdx(nil)).To(BeNil())
	})

	It("maps only the claiming match kinds", func() {
		info := &network.Info{
			Interfaces: []network.InterfaceState{
				{Match: network.Match{Kind: network.MatchUnitNumber, AdapterKey: 4000}},
				{Match: network.Match{Kind: network.MatchRecord, AdapterKey: 4001}},
				{Match: network.Match{Kind: network.MatchIdentity, AdapterKey: 4002}},
				{Match: network.Match{Kind: network.MatchOrphan, AdapterKey: 4003}},
				{Match: network.Match{Kind: network.MatchPositional, AdapterKey: 4004}},
				{Match: network.Match{Kind: network.MatchNone}},
				{Match: network.Match{Kind: network.MatchAmbiguous, Candidates: []int32{4005, 4006}}},
			},
		}
		Expect(network.DeviceKeyToSpecIdx(info)).To(Equal(map[int32]int{
			4000: 0, 4001: 1, 4002: 2, 4003: 3, 4004: 4,
		}))
	})
})

var _ = Describe("IsClaim", Label(testlabels.API), func() {
	DescribeTable("reports whether a match kind claims an adapter",
		func(kind network.MatchKind, expected bool) {
			Expect(network.IsClaim(kind)).To(Equal(expected))
		},
		Entry("UnitNumber", network.MatchUnitNumber, true),
		Entry("Record", network.MatchRecord, true),
		Entry("Identity", network.MatchIdentity, true),
		Entry("Orphan", network.MatchOrphan, true),
		Entry("Positional", network.MatchPositional, true),
		Entry("None", network.MatchNone, false),
		Entry("Ambiguous", network.MatchAmbiguous, false),
	)
})

var _ = Describe("ObservedInfo", Label(testlabels.API), func() {
	var (
		ctx    context.Context
		client ctrlclient.Client
		vm     *vmopv1.VirtualMachine
		moVM   mo.VirtualMachine
	)

	BeforeEach(func() {
		ctx = providerTestContext(pkgcfg.NetworkProviderTypeVDS)
		client = builder.NewFakeClient()
		vm = providerTestVM()
		vm.Spec.Network.Interfaces = parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS)
		moVM = providerTestMoVM(indistinguishableCards(2)...)
	})

	observe := func() *network.Info {
		return network.Observe(ctx, network.ObserveInput{
			Client: client,
			VM:     vm,
			MoVM:   moVM,
		})
	}

	It("returns nil when the VM has no config", func() {
		Expect(network.ObservedInfo(ctx, client, vm, mo.VirtualMachine{})).To(BeNil())
	})

	It("observes and matches when there is no reconcile state", func() {
		info := network.ObservedInfo(ctx, client, vm, moVM)
		Expect(info).ToNot(BeNil())
		Expect(network.DeviceKeyToSpecIdx(info)).To(Equal(map[int32]int{4000: 0, 4001: 1}))
	})

	It("observes and matches when the reconcile state has no observation", func() {
		ctx = network.WithContext(ctx, &network.ReconcileState{})
		info := network.ObservedInfo(ctx, client, vm, moVM)
		Expect(info).ToNot(BeNil())
		Expect(network.DeviceKeyToSpecIdx(info)).To(Equal(map[int32]int{4000: 0, 4001: 1}))
	})

	It("returns the pass's observation when it is current", func() {
		observed := observe()
		ctx = network.WithContext(ctx, &network.ReconcileState{Observed: observed})
		Expect(network.ObservedInfo(ctx, client, vm, moVM)).To(BeIdenticalTo(observed))
	})

	It("observes anew when the interfaces changed after the pass's observation", func() {
		observed := observe()
		ctx = network.WithContext(ctx, &network.ReconcileState{Observed: observed})

		// A later step, such as the schema upgrade's unit number backfill,
		// changes the interfaces.
		vm.Spec.Network.Interfaces[0], vm.Spec.Network.Interfaces[1] =
			vm.Spec.Network.Interfaces[1], vm.Spec.Network.Interfaces[0]

		info := network.ObservedInfo(ctx, client, vm, moVM)
		Expect(info).ToNot(BeIdenticalTo(observed))
		Expect(info.Interfaces[0].Spec.Name).To(Equal("eth1"))
	})

	It("observes anew when the vSphere VM changed after the pass's observation", func() {
		observed := observe()
		ctx = network.WithContext(ctx, &network.ReconcileState{Observed: observed})

		moVM.Config.ChangeVersion = "change-2"

		Expect(network.ObservedInfo(ctx, client, vm, moVM)).ToNot(BeIdenticalTo(observed))
	})
})

// Spec 017 T018: status and boot options read MapEthernetDevicesToSpecIdx or
// the same shared match, so these cases pin what both consumers see.
var _ = Describe("MapEthernetDevicesToSpecIdx with unit numbers", Label(testlabels.API), func() {
	var (
		vmCtx   pkgctx.VirtualMachineContext
		devices []vimtypes.BaseVirtualDevice
	)

	numbered := func(name string, unitNumber int32) vmopv1.VirtualMachineNetworkInterfaceSpec {
		return vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name:       name,
			Network:    providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
			UnitNumber: ptr.To(unitNumber),
		}
	}

	unnumbered := func(name string) vmopv1.VirtualMachineNetworkInterfaceSpec {
		return vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name:    name,
			Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a"),
		}
	}

	BeforeEach(func() {
		vmCtx = pkgctx.VirtualMachineContext{
			Context: pkgcfg.UpdateContext(
				providerTestContext(pkgcfg.NetworkProviderTypeVDS),
				func(config *pkgcfg.Config) {
					config.Features.VMNetworkUnitNumbers = true
				}),
			Logger: suite.GetLogger().WithName("map_eth_devices_unit_numbers"),
			VM:     providerTestVM(),
		}

		// Three indistinguishable cards at unit numbers 7, 8, and 9.
		devices = indistinguishableCards(3)
	})

	mapping := func() map[int32]int {
		return network.MapEthernetDevicesToSpecIdx(vmCtx, builder.NewFakeClient(), providerTestMoVM(devices...))
	}

	It("maps numbered interfaces by unit number regardless of spec order", func() {
		vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			numbered("eth0", 9),
			numbered("eth1", 7),
		}
		Expect(mapping()).To(Equal(map[int32]int{4002: 0, 4000: 1}))

		By("reordering the spec entries keeps each interface's card")
		vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			numbered("eth1", 7),
			numbered("eth0", 9),
		}
		Expect(mapping()).To(Equal(map[int32]int{4000: 0, 4002: 1}))
	})

	It("gives a numbered interface whose unit number has no card no mapping", func() {
		vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			numbered("eth0", 10),
			numbered("eth1", 8),
		}
		// The cards at unit numbers 7 and 9 are not given to eth0.
		Expect(mapping()).To(Equal(map[int32]int{4001: 1}))
	})

	It("does not fill a numbered interface's missing slot with an unnumbered interface's card", func() {
		vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			numbered("eth0", 10),
			unnumbered("eth1"),
			numbered("eth2", 8),
		}
		// eth2 claims unit 8 exactly; eth1 takes the first remaining card by
		// position; eth0 gets nothing and the card at unit 9 stays unclaimed.
		Expect(mapping()).To(Equal(map[int32]int{4001: 2, 4000: 1}))
	})

	It("zips unnumbered interfaces with the cards the numbered interfaces leave", func() {
		vmCtx.VM.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			unnumbered("eth0"),
			numbered("eth1", 7),
			unnumbered("eth2"),
		}
		Expect(mapping()).To(Equal(map[int32]int{4001: 0, 4000: 1, 4002: 2}))
	})
})
