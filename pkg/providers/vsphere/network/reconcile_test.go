// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var _ = Describe("ReconcileNetworkInterfaces", func() {

	var (
		ctx             context.Context
		results         *network.NetworkInterfaceResults
		currentEthCards object.VirtualDeviceList

		deviceChanges []vimtypes.BaseVirtualDeviceConfigSpec
		err           error
	)

	BeforeEach(func() {
		results = &network.NetworkInterfaceResults{}
	})

	JustBeforeEach(func() {
		deviceChanges, err = network.ReconcileNetworkInterfaces(
			ctx,
			results,
			currentEthCards)
	})

	DescribeTableSubtree("NetworkEnv",
		func(networkEnv builder.NetworkEnv) {
			const (
				interfaceName0 = "eth0"
			)

			var (
				ethCard vimtypes.BaseVirtualEthernetCard
				obj     ctrlclient.Object
			)

			BeforeEach(func() {
				ethCard = &vimtypes.VirtualVmxnet3{}

				switch networkEnv {
				case builder.NetworkEnvVDS:
					ethCard.GetVirtualEthernetCard().AddressType = string(vimtypes.VirtualEthernetCardMacTypeGenerated)
					ethCard.GetVirtualEthernetCard().MacAddress = ""
					ethCard.GetVirtualEthernetCard().ExternalId = ""
					ethCard.GetVirtualEthernetCard().Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: "pg-1",
						},
					}
				case builder.NetworkEnvNSXT:
					ethCard.GetVirtualEthernetCard().MacAddress = "my-mac"
					ethCard.GetVirtualEthernetCard().AddressType = string(vimtypes.VirtualEthernetCardMacTypeAssigned)
					ethCard.GetVirtualEthernetCard().ExternalId = "my-ext-id"
					ethCard.GetVirtualEthernetCard().Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: "pg-1",
						},
					}
				case builder.NetworkEnvVPC:
					ethCard.GetVirtualEthernetCard().MacAddress = "my-mac"
					ethCard.GetVirtualEthernetCard().AddressType = string(vimtypes.VirtualEthernetCardMacTypeAssigned)
					ethCard.GetVirtualEthernetCard().ExternalId = "my-ext-id"
					ethCard.GetVirtualEthernetCard().SubnetId = "/projects/my-project/vpcs/foo/subnets/old-subnet"
					ethCard.GetVirtualEthernetCard().Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: "pg-1",
						},
					}
				default:
					// The named network environment requires no setup.
				}

				switch networkEnv {
				case builder.NetworkEnvVDS:
					obj = &netopv1alpha1.NetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name: "my-vds-interface",
							Labels: map[string]string{
								network.VMInterfaceNameLabel: interfaceName0,
							},
						},
						Status: netopv1alpha1.NetworkInterfaceStatus{
							NetworkID: "pg-1",
						},
					}
				case builder.NetworkEnvNSXT:
					obj = &ncpv1alpha1.VirtualNetworkInterface{
						ObjectMeta: metav1.ObjectMeta{
							Name: "my-ncp-interface",
							Labels: map[string]string{
								network.VMInterfaceNameLabel: interfaceName0,
							},
						},
						Status: ncpv1alpha1.VirtualNetworkInterfaceStatus{
							InterfaceID: ethCard.GetVirtualEthernetCard().ExternalId,
							MacAddress:  ethCard.GetVirtualEthernetCard().MacAddress,
						},
					}
				case builder.NetworkEnvVPC:
					obj = &vpcv1alpha1.SubnetPort{
						ObjectMeta: metav1.ObjectMeta{
							Name: "my-vpc-interface",
							Labels: map[string]string{
								network.VMInterfaceNameLabel: interfaceName0,
							},
						},
						Status: vpcv1alpha1.SubnetPortStatus{
							Attachment: vpcv1alpha1.PortAttachment{
								ID: ethCard.GetVirtualEthernetCard().ExternalId,
							},
							NetworkInterfaceConfig: vpcv1alpha1.NetworkInterfaceConfig{
								MACAddress: ethCard.GetVirtualEthernetCard().MacAddress,
							},
						},
					}
				default:
					// The named network environment requires no setup.
				}
			})

			AfterEach(func() {
				ethCard = nil
				obj = nil
				currentEthCards = nil
			})

			It("Returns success for empty input", func() {
				Expect(err).ToNot(HaveOccurred())
				Expect(deviceChanges).To(BeEmpty())
			})

			When("Add", func() {
				BeforeEach(func() {
					results.Devices = append(results.Devices, network.Device{
						InterfaceName: interfaceName0,
						EthCard:       ethCard.(vimtypes.BaseVirtualDevice),
					})

				})

				It("Returns Add Operation", func() {
					Expect(err).ToNot(HaveOccurred())

					Expect(deviceChanges).To(HaveLen(1))
					dc0 := deviceChanges[0].GetVirtualDeviceConfigSpec()
					Expect(dc0.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationAdd))
					Expect(dc0.Device).To(Equal(ethCard.(vimtypes.BaseVirtualDevice)))
				})
			})

			When("Edit", func() {
				BeforeEach(func() {
					curEthCard := *ethCard.GetVirtualEthernetCard()

					ethCard.GetVirtualEthernetCard().Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
						Port: vimtypes.DistributedVirtualSwitchPortConnection{
							PortgroupKey: "pg-new-42",
						},
					}

					results.Devices = append(results.Devices, network.Device{
						InterfaceName: interfaceName0,
						EthCard:       ethCard.(vimtypes.BaseVirtualDevice),
					})

					results.OrphanedNetworkInterfaces = append(results.OrphanedNetworkInterfaces, obj)

					currentEthCards = append(currentEthCards, &curEthCard)
				})

				It("Returns Edit Operation", func() {
					Expect(err).ToNot(HaveOccurred())

					Expect(deviceChanges).To(HaveLen(1))
					dc0 := deviceChanges[0].GetVirtualDeviceConfigSpec()
					Expect(dc0.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationEdit))
					Expect(dc0.Device.GetVirtualDevice().Backing).To(Equal(ethCard.GetVirtualEthernetCard().Backing))
					Expect(dc0.Device.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().SubnetId).To(BeEmpty())
				})

				When("Network Interface CR is old and does not have interface name label", func() {
					BeforeEach(func() {
						obj.SetName("foo-" + interfaceName0)
						delete(obj.GetLabels(), network.VMInterfaceNameLabel)
					})

					It("Returns Edit Operation", func() {
						Expect(err).ToNot(HaveOccurred())

						Expect(deviceChanges).To(HaveLen(1))
						dc0 := deviceChanges[0].GetVirtualDeviceConfigSpec()
						Expect(dc0.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationEdit))
						Expect(dc0.Device.GetVirtualDevice().Backing).To(Equal(ethCard.GetVirtualEthernetCard().Backing))
						Expect(dc0.Device.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().SubnetId).To(BeEmpty())
					})

					When("An earlier, non-matching orphaned CR without the label also exists", func() {
						BeforeEach(func() {
							// decoyObj also lacks the interface name label but is
							// for a different interface, so it does not match any
							// current ethernet card. It is placed ahead of obj so
							// that finding obj requires looking past index 0 of
							// the without-label candidates.
							decoyObj := obj.DeepCopyObject().(ctrlclient.Object)
							decoyObj.SetName("foo-some-other-interface")
							delete(decoyObj.GetLabels(), network.VMInterfaceNameLabel)

							switch d := decoyObj.(type) {
							case *netopv1alpha1.NetworkInterface:
								d.Status.NetworkID = "decoy-network-id"
							case *ncpv1alpha1.VirtualNetworkInterface:
								d.Status.InterfaceID = "decoy-ext-id"
							case *vpcv1alpha1.SubnetPort:
								d.Status.Attachment.ID = "decoy-ext-id"
							}

							results.OrphanedNetworkInterfaces = append(
								[]ctrlclient.Object{decoyObj}, results.OrphanedNetworkInterfaces...)
						})

						It("still matches obj and returns Edit Operation", func() {
							Expect(err).ToNot(HaveOccurred())

							Expect(deviceChanges).To(HaveLen(1))
							dc0 := deviceChanges[0].GetVirtualDeviceConfigSpec()
							Expect(dc0.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationEdit))
							Expect(dc0.Device.GetVirtualDevice().Backing).To(Equal(ethCard.GetVirtualEthernetCard().Backing))
							Expect(dc0.Device.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().SubnetId).To(BeEmpty())
						})
					})
				})
			})

			When("Remove", func() {
				BeforeEach(func() {
					currentEthCards = append(currentEthCards, ethCard.(vimtypes.BaseVirtualDevice))
				})

				It("Returns Remove Operation", func() {
					Expect(err).ToNot(HaveOccurred())

					Expect(deviceChanges).To(HaveLen(1))
					dc0 := deviceChanges[0].GetVirtualDeviceConfigSpec()
					Expect(dc0.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationRemove))
					Expect(dc0.Device.GetVirtualDevice().Backing).To(Equal(ethCard.GetVirtualEthernetCard().Backing))
				})
			})
		},
		Entry("VDS", builder.NetworkEnvVDS),
		Entry("NSX-T", builder.NetworkEnvNSXT),
		Entry("VPC", builder.NetworkEnvVPC),
	)
})

// charFindMatchingEthCard routes every characterization call through one place so
// a future signature change to network.FindMatchingEthCard (for example, the
// unit-number parameter planned by spec 008) is a one-line edit here.
func charFindMatchingEthCard(
	currentEthCards object.VirtualDeviceList,
	ethCard vimtypes.BaseVirtualEthernetCard) int {

	return network.FindMatchingEthCard(currentEthCards, ethCard)
}

func charDVPGBacking(switchUUID, pgKey string) *vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo {
	return &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
		Port: vimtypes.DistributedVirtualSwitchPortConnection{
			SwitchUuid:   switchUUID,
			PortgroupKey: pgKey,
		},
	}
}

func charStdBacking(deviceName string) *vimtypes.VirtualEthernetCardNetworkBackingInfo {
	return &vimtypes.VirtualEthernetCardNetworkBackingInfo{
		VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
			DeviceName: deviceName,
		},
	}
}

func charOpaqueBacking(id string) *vimtypes.VirtualEthernetCardOpaqueNetworkBackingInfo {
	return &vimtypes.VirtualEthernetCardOpaqueNetworkBackingInfo{
		OpaqueNetworkId:   id,
		OpaqueNetworkType: "nsx.LogicalSwitch",
	}
}

// charCard describes an Ethernet card for the characterization tests.
type charCard struct {
	key         int32
	addressType vimtypes.VirtualEthernetCardMacType
	mac         string
	externalID  string
	subnetID    string
	backing     vimtypes.BaseVirtualDeviceBackingInfo
}

func (c charCard) vmxnet3() *vimtypes.VirtualVmxnet3 {
	card := &vimtypes.VirtualVmxnet3{}
	c.apply(card.GetVirtualEthernetCard())
	return card
}

func (c charCard) e1000e() *vimtypes.VirtualE1000e {
	card := &vimtypes.VirtualE1000e{}
	c.apply(card.GetVirtualEthernetCard())
	return card
}

func (c charCard) apply(card *vimtypes.VirtualEthernetCard) {
	card.Key = c.key
	card.AddressType = string(c.addressType)
	card.MacAddress = c.mac
	card.ExternalId = c.externalID
	card.SubnetId = c.subnetID
	card.Backing = c.backing
}

// Characterization (spec 017 T012): pins pre-restructuring behavior.
var _ = Describe("FindMatchingEthCard characterization", func() {

	const (
		mac1     = "00:50:56:aa:bb:01"
		mac1Up   = "00:50:56:AA:BB:01"
		mac2     = "00:50:56:aa:bb:02"
		extID1   = "ext-id-1"
		extID2   = "ext-id-2"
		switch1  = "switch-uuid-1"
		switch2  = "switch-uuid-2"
		pg1      = "dvportgroup-1"
		pg2      = "dvportgroup-2"
		netName1 = "network-1"
		netName2 = "network-2"
		gen      = vimtypes.VirtualEthernetCardMacTypeGenerated
		manual   = vimtypes.VirtualEthernetCardMacTypeManual
		assigned = vimtypes.VirtualEthernetCardMacTypeAssigned
	)

	DescribeTable("matching rules",
		func(desired charCard, current []charCard, expectedIdx int) {
			var currentEthCards object.VirtualDeviceList
			for _, c := range current {
				currentEthCards = append(currentEthCards, c.vmxnet3())
			}
			Expect(charFindMatchingEthCard(currentEthCards, desired.vmxnet3())).To(Equal(expectedIdx))
		},

		// Backing.
		Entry("DVPG backing with same switch and portgroup matches",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, mac: mac1, backing: charDVPGBacking(switch1, pg1)}},
			0),
		Entry("DVPG backing with different portgroup does not match",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, backing: charDVPGBacking(switch1, pg2)}},
			-1),
		Entry("DVPG backing with different switch does not match",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, backing: charDVPGBacking(switch2, pg1)}},
			-1),
		Entry("standard backing with same device name matches",
			charCard{addressType: gen, backing: charStdBacking(netName1)},
			[]charCard{{key: 4000, backing: charStdBacking(netName1)}},
			0),
		Entry("standard backing with different device name does not match",
			charCard{addressType: gen, backing: charStdBacking(netName1)},
			[]charCard{{key: 4000, backing: charStdBacking(netName2)}},
			-1),
		Entry("opaque backing with same ID and type matches",
			charCard{addressType: gen, backing: charOpaqueBacking("ls-1")},
			[]charCard{{key: 4000, backing: charOpaqueBacking("ls-1")}},
			0),
		Entry("opaque backing with different ID does not match",
			charCard{addressType: gen, backing: charOpaqueBacking("ls-1")},
			[]charCard{{key: 4000, backing: charOpaqueBacking("ls-2")}},
			-1),
		Entry("different backing types do not match",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, backing: charStdBacking(pg1)}},
			-1),
		Entry("current card without a backing never matches",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000}},
			-1),
		Entry("desired card without a backing never matches",
			charCard{addressType: gen},
			[]charCard{{key: 4000, backing: charDVPGBacking(switch1, pg1)}},
			-1),

		// MAC address.
		Entry("Manual MAC is compared case-insensitively",
			charCard{addressType: manual, mac: mac1Up, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, mac: mac1, backing: charDVPGBacking(switch1, pg1)}},
			0),
		Entry("Manual MAC mismatch does not match even with the same backing",
			charCard{addressType: manual, mac: mac1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, mac: mac2, backing: charDVPGBacking(switch1, pg1)}},
			-1),
		Entry("Generated MAC is not compared",
			charCard{addressType: gen, mac: mac1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, mac: mac2, backing: charDVPGBacking(switch1, pg1)}},
			0),
		Entry("Assigned MAC is not compared",
			charCard{addressType: assigned, mac: mac1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, mac: mac2, backing: charDVPGBacking(switch1, pg1)}},
			0),

		// External ID.
		Entry("non-empty external ID must be equal",
			charCard{addressType: gen, externalID: extID1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, externalID: extID2, backing: charDVPGBacking(switch1, pg1)}},
			-1),
		Entry("equal external ID matches",
			charCard{addressType: gen, externalID: extID1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, externalID: extID1, backing: charDVPGBacking(switch1, pg1)}},
			0),
		Entry("empty desired external ID ignores the current card's external ID",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{{key: 4000, externalID: extID1, backing: charDVPGBacking(switch1, pg1)}},
			0),

		// Several candidates and disagreeing evidence. Every check must pass on
		// the same card; there is no precedence between evidence types.
		Entry("first matching card in device order wins when several match",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{
				{key: 4000, mac: mac1, backing: charDVPGBacking(switch1, pg1)},
				{key: 4001, mac: mac2, backing: charDVPGBacking(switch1, pg1)},
			},
			0),
		Entry("non-matching cards before the match are skipped",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{
				{key: 4000, backing: charDVPGBacking(switch1, pg2)},
				{key: 4001, backing: charDVPGBacking(switch1, pg1)},
			},
			1),
		Entry("Manual MAC on one card and backing on another is no match",
			charCard{addressType: manual, mac: mac1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{
				{key: 4000, mac: mac1, backing: charDVPGBacking(switch1, pg2)},
				{key: 4001, mac: mac2, backing: charDVPGBacking(switch1, pg1)},
			},
			-1),
		Entry("external ID on one card and backing on another is no match",
			charCard{addressType: gen, externalID: extID1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{
				{key: 4000, externalID: extID1, backing: charDVPGBacking(switch1, pg2)},
				{key: 4001, externalID: extID2, backing: charDVPGBacking(switch1, pg1)},
			},
			-1),
		Entry("Manual MAC and external ID on different cards is no match",
			charCard{addressType: manual, mac: mac1, externalID: extID1, backing: charDVPGBacking(switch1, pg1)},
			[]charCard{
				{key: 4000, mac: mac1, externalID: extID2, backing: charDVPGBacking(switch1, pg1)},
				{key: 4001, mac: mac2, externalID: extID1, backing: charDVPGBacking(switch1, pg1)},
			},
			-1),
		Entry("no current cards is no match",
			charCard{addressType: gen, backing: charDVPGBacking(switch1, pg1)},
			nil,
			-1),
	)
})

// Characterization (spec 017 T012): pins pre-restructuring behavior.
var _ = Describe("ReconcileNetworkInterfaces characterization", func() {

	const (
		interfaceName0 = "eth0"
		interfaceName1 = "eth1"
		switchUUID     = "switch-uuid"
		oldPG          = "dvportgroup-old"
		newPG          = "dvportgroup-new"
		otherPG        = "dvportgroup-other"
		stalePG        = "dvportgroup-stale"
		oldMAC         = "00:50:56:00:00:01"
		newMAC         = "00:50:56:00:00:02"
		oldExtID       = "ext-id-old"
		newExtID       = "ext-id-new"
		oldSubnetID    = "/projects/p/vpcs/v/subnets/old"
		gen            = vimtypes.VirtualEthernetCardMacTypeGenerated
		manual         = vimtypes.VirtualEthernetCardMacTypeManual
	)

	var (
		ctx             context.Context
		results         *network.NetworkInterfaceResults
		currentEthCards object.VirtualDeviceList
		deviceChanges   []vimtypes.BaseVirtualDeviceConfigSpec
		err             error
	)

	BeforeEach(func() {
		ctx = context.Background()
		results = &network.NetworkInterfaceResults{}
		currentEthCards = nil
	})

	JustBeforeEach(func() {
		deviceChanges, err = network.ReconcileNetworkInterfaces(ctx, results, currentEthCards)
	})

	ops := func() []vimtypes.VirtualDeviceConfigSpecOperation {
		var o []vimtypes.VirtualDeviceConfigSpecOperation
		for _, dc := range deviceChanges {
			o = append(o, dc.GetVirtualDeviceConfigSpec().Operation)
		}
		return o
	}

	When("the desired card matches a current card", func() {
		BeforeEach(func() {
			currentEthCards = append(currentEthCards,
				charCard{key: 4000, addressType: gen, mac: oldMAC, backing: charDVPGBacking(switchUUID, oldPG)}.e1000e())
			results.Devices = append(results.Devices, network.Device{
				InterfaceName: interfaceName0,
				EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, oldPG)}.vmxnet3(),
			})
		})

		It("emits no device change, regardless of the concrete card type", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(deviceChanges).To(BeEmpty())
		})
	})

	When("the attachment changed and there are no orphaned CRs", func() {
		BeforeEach(func() {
			currentEthCards = append(currentEthCards,
				charCard{key: 4000, addressType: gen, mac: oldMAC, backing: charDVPGBacking(switchUUID, oldPG)}.e1000e())
			results.Devices = append(results.Devices, network.Device{
				InterfaceName: interfaceName0,
				EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, newPG)}.vmxnet3(),
			})
		})

		It("removes the current card and adds a new card, removes first", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ops()).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))

			removed := deviceChanges[0].GetVirtualDeviceConfigSpec().Device
			Expect(removed).To(BeAssignableToTypeOf(&vimtypes.VirtualE1000e{}))
			Expect(removed.GetVirtualDevice().Key).To(Equal(int32(4000)))

			added := deviceChanges[1].GetVirtualDeviceConfigSpec().Device
			Expect(added).To(BeAssignableToTypeOf(&vimtypes.VirtualVmxnet3{}))
			Expect(added.GetVirtualDevice().Backing).To(Equal(charDVPGBacking(switchUUID, newPG)))
		})
	})

	DescribeTable("same-name orphaned CR Edit preserves the concrete card type and device key",
		func(desired charCard, current charCard, orphan ctrlclient.Object) {
			currentEthCards = object.VirtualDeviceList{current.e1000e()}
			results.Devices = []network.Device{{InterfaceName: interfaceName0, EthCard: desired.vmxnet3()}}
			results.OrphanedNetworkInterfaces = []ctrlclient.Object{orphan}

			deviceChanges, err = network.ReconcileNetworkInterfaces(ctx, results, currentEthCards)
			Expect(err).ToNot(HaveOccurred())
			Expect(ops()).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationEdit,
			}))

			edited := deviceChanges[0].GetVirtualDeviceConfigSpec().Device
			Expect(edited).To(BeAssignableToTypeOf(&vimtypes.VirtualE1000e{}))
			Expect(edited.GetVirtualDevice().Key).To(Equal(current.key))

			ethDev := edited.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
			Expect(ethDev.Backing).To(Equal(desired.backing))
			Expect(ethDev.AddressType).To(Equal(string(desired.addressType)))
			if desired.addressType == manual {
				// Only a provider-pinned MAC is asserted. For a generated MAC,
				// the Go value left in the struct is not a wire-level intent.
				Expect(ethDev.MacAddress).To(Equal(desired.mac))
			}
			Expect(ethDev.ExternalId).To(Equal(desired.externalID))
			Expect(ethDev.SubnetId).To(BeEmpty())
		},
		Entry("VDS",
			charCard{addressType: gen, externalID: newExtID, backing: charDVPGBacking(switchUUID, newPG)},
			charCard{key: 4000, addressType: gen, mac: oldMAC, externalID: oldExtID, backing: charDVPGBacking(switchUUID, oldPG)},
			&netopv1alpha1.NetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-old-net-eth0", Labels: map[string]string{network.VMInterfaceNameLabel: interfaceName0}},
				Status:     netopv1alpha1.NetworkInterfaceStatus{NetworkID: oldPG, ExternalID: oldExtID},
			}),
		Entry("NSX-T",
			charCard{addressType: manual, mac: newMAC, externalID: newExtID, backing: charDVPGBacking(switchUUID, newPG)},
			charCard{key: 4000, addressType: manual, mac: oldMAC, externalID: oldExtID, backing: charDVPGBacking(switchUUID, oldPG)},
			&ncpv1alpha1.VirtualNetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-old-net-eth0", Labels: map[string]string{network.VMInterfaceNameLabel: interfaceName0}},
				Status:     ncpv1alpha1.VirtualNetworkInterfaceStatus{InterfaceID: oldExtID, MacAddress: oldMAC},
			}),
		Entry("VPC",
			charCard{addressType: manual, mac: newMAC, externalID: newExtID, backing: charDVPGBacking(switchUUID, newPG)},
			charCard{key: 4000, addressType: manual, mac: oldMAC, externalID: oldExtID, subnetID: oldSubnetID, backing: charDVPGBacking(switchUUID, oldPG)},
			&vpcv1alpha1.SubnetPort{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-old-net-eth0", Labels: map[string]string{network.VMInterfaceNameLabel: interfaceName0}},
				Status: vpcv1alpha1.SubnetPortStatus{
					Attachment:             vpcv1alpha1.PortAttachment{ID: oldExtID},
					NetworkInterfaceConfig: vpcv1alpha1.NetworkInterfaceConfig{MACAddress: oldMAC},
				},
			}),
	)

	When("the orphaned CR is labeled with a different interface name", func() {
		BeforeEach(func() {
			currentEthCards = append(currentEthCards,
				charCard{key: 4000, addressType: gen, mac: oldMAC, backing: charDVPGBacking(switchUUID, oldPG)}.e1000e())
			results.Devices = append(results.Devices, network.Device{
				InterfaceName: interfaceName0,
				EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, newPG)}.vmxnet3(),
			})
			results.OrphanedNetworkInterfaces = append(results.OrphanedNetworkInterfaces,
				&netopv1alpha1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: "vm-old-net-eth0", Labels: map[string]string{network.VMInterfaceNameLabel: interfaceName1}},
					Status:     netopv1alpha1.NetworkInterfaceStatus{NetworkID: oldPG},
				})
		})

		It("removes the current card and adds a new card", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ops()).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))
			Expect(deviceChanges[0].GetVirtualDeviceConfigSpec().Device.GetVirtualDevice().Key).To(Equal(int32(4000)))
		})
	})

	When("the same-name orphaned CR does not identify any current card", func() {
		BeforeEach(func() {
			currentEthCards = append(currentEthCards,
				charCard{key: 4000, addressType: gen, mac: oldMAC, backing: charDVPGBacking(switchUUID, oldPG)}.e1000e())
			results.Devices = append(results.Devices, network.Device{
				InterfaceName: interfaceName0,
				EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, newPG)}.vmxnet3(),
			})
			results.OrphanedNetworkInterfaces = append(results.OrphanedNetworkInterfaces,
				&netopv1alpha1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: "vm-other-net-eth0", Labels: map[string]string{network.VMInterfaceNameLabel: interfaceName0}},
					Status:     netopv1alpha1.NetworkInterfaceStatus{NetworkID: otherPG},
				})
		})

		It("removes the current card and adds a new card", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ops()).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))
		})
	})

	When("a card is claimed by an exact match before another interface's orphaned CR names it", func() {
		BeforeEach(func() {
			currentEthCards = append(currentEthCards,
				charCard{key: 4000, addressType: gen, mac: oldMAC, backing: charDVPGBacking(switchUUID, oldPG)}.e1000e())
			results.Devices = append(results.Devices,
				network.Device{
					InterfaceName: interfaceName0,
					EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, oldPG)}.vmxnet3(),
				},
				network.Device{
					InterfaceName: interfaceName1,
					EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, newPG)}.vmxnet3(),
				},
			)
			results.OrphanedNetworkInterfaces = append(results.OrphanedNetworkInterfaces,
				&netopv1alpha1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: "vm-old-net-eth1", Labels: map[string]string{network.VMInterfaceNameLabel: interfaceName1}},
					Status:     netopv1alpha1.NetworkInterfaceStatus{NetworkID: oldPG},
				})
		})

		It("keeps the claimed card and adds a new card for the other interface", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ops()).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))
			added := deviceChanges[0].GetVirtualDeviceConfigSpec().Device
			Expect(added.GetVirtualDevice().Backing).To(Equal(charDVPGBacking(switchUUID, newPG)))
		})
	})

	When("interfaces mix an exact match, an orphaned CR Edit, and an unmatched current card", func() {
		BeforeEach(func() {
			currentEthCards = append(currentEthCards,
				charCard{key: 4000, addressType: gen, mac: oldMAC, backing: charDVPGBacking(switchUUID, otherPG)}.vmxnet3(),
				charCard{key: 4001, addressType: gen, mac: newMAC, backing: charDVPGBacking(switchUUID, oldPG)}.e1000e(),
				charCard{key: 4002, addressType: gen, backing: charDVPGBacking(switchUUID, stalePG)}.vmxnet3(),
			)
			results.Devices = append(results.Devices,
				network.Device{
					InterfaceName: interfaceName0,
					EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, otherPG)}.vmxnet3(),
				},
				network.Device{
					InterfaceName: interfaceName1,
					EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, newPG)}.vmxnet3(),
				},
			)
			results.OrphanedNetworkInterfaces = append(results.OrphanedNetworkInterfaces,
				&netopv1alpha1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{Name: "vm-old-net-eth1", Labels: map[string]string{network.VMInterfaceNameLabel: interfaceName1}},
					Status:     netopv1alpha1.NetworkInterfaceStatus{NetworkID: oldPG},
				})
		})

		It("removes the unmatched card first, then edits the orphan's card", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ops()).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationEdit,
			}))

			removed := deviceChanges[0].GetVirtualDeviceConfigSpec().Device
			Expect(removed.GetVirtualDevice().Key).To(Equal(int32(4002)))

			edited := deviceChanges[1].GetVirtualDeviceConfigSpec().Device
			Expect(edited).To(BeAssignableToTypeOf(&vimtypes.VirtualE1000e{}))
			Expect(edited.GetVirtualDevice().Key).To(Equal(int32(4001)))
			Expect(edited.GetVirtualDevice().Backing).To(Equal(charDVPGBacking(switchUUID, newPG)))
		})
	})

	When("there are more current cards than desired interfaces", func() {
		BeforeEach(func() {
			currentEthCards = append(currentEthCards,
				charCard{key: 4000, addressType: gen, backing: charDVPGBacking(switchUUID, oldPG)}.vmxnet3(),
				charCard{key: 4001, addressType: gen, backing: charDVPGBacking(switchUUID, oldPG)}.vmxnet3(),
			)
			results.Devices = append(results.Devices, network.Device{
				InterfaceName: interfaceName0,
				EthCard:       charCard{addressType: gen, backing: charDVPGBacking(switchUUID, oldPG)}.vmxnet3(),
			})
		})

		It("keeps the first matching card and removes the rest", func() {
			Expect(err).ToNot(HaveOccurred())
			Expect(ops()).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
			}))
			Expect(deviceChanges[0].GetVirtualDeviceConfigSpec().Device.GetVirtualDevice().Key).To(Equal(int32(4001)))
		})
	})
})
