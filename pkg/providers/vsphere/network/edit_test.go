// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vimtypes "github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/govmomi/vim25/xml"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
)

// populatedCard describes a fully populated card of one concrete Ethernet
// card type for the wire tests.
type populatedCard struct {
	// new returns a card with every common field set by populateEthernetCard
	// and every field specific to its type set.
	new func() vimtypes.BaseVirtualDevice

	// xsiType is the card's SOAP type.
	xsiType string

	// elements are SOAP elements of the fields specific to the type.
	elements []string
}

func populatedVmxnet3(card *vimtypes.VirtualVmxnet3) {
	populateEthernetCard(card.GetVirtualEthernetCard())
	card.Uptv2Enabled = ptr.To(true)
	card.StrictLatencyConfig = &vimtypes.VirtualVmxnet3StrictLatencyConfig{
		Allowed:     ptr.To(true),
		MaxTxQueues: 4,
		MaxRxQueues: 8,
	}
}

var vmxnet3Elements = []string{
	"<uptv2Enabled>true</uptv2Enabled>",
	"<strictLatencyConfig><allowed>true</allowed>",
	"<maxTxQueues>4</maxTxQueues><maxRxQueues>8</maxRxQueues>",
}

var populatedCards = []TableEntry{
	Entry("VirtualVmxnet3", populatedCard{
		new: func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualVmxnet3{}
			populatedVmxnet3(card)
			return card
		},
		xsiType:  "VirtualVmxnet3",
		elements: vmxnet3Elements,
	}),
	Entry("VirtualE1000e", populatedCard{
		new: func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualE1000e{}
			populateEthernetCard(card.GetVirtualEthernetCard())
			return card
		},
		xsiType: "VirtualE1000e",
	}),
	Entry("VirtualE1000", populatedCard{
		new: func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualE1000{}
			populateEthernetCard(card.GetVirtualEthernetCard())
			return card
		},
		xsiType: "VirtualE1000",
	}),
	Entry("VirtualVmxnet2", populatedCard{
		new: func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualVmxnet2{}
			populateEthernetCard(card.GetVirtualEthernetCard())
			return card
		},
		xsiType: "VirtualVmxnet2",
	}),
	Entry("VirtualPCNet32", populatedCard{
		new: func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualPCNet32{}
			populateEthernetCard(card.GetVirtualEthernetCard())
			return card
		},
		xsiType: "VirtualPCNet32",
	}),
	Entry("VirtualSriovEthernetCard", populatedCard{
		new: func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualSriovEthernetCard{
				AllowGuestOSMtuChange: ptr.To(true),
				SriovBacking: &vimtypes.VirtualSriovEthernetCardSriovBackingInfo{
					PhysicalFunctionBacking: &vimtypes.VirtualPCIPassthroughDeviceBackingInfo{
						Id:       "0000:3b:00.0",
						DeviceId: "1572",
						SystemId: "sys-1",
						VendorId: 4318,
					},
					VirtualFunctionIndex: 3,
				},
			}
			populateEthernetCard(card.GetVirtualEthernetCard())
			return card
		},
		xsiType: "VirtualSriovEthernetCard",
		elements: []string{
			"<allowGuestOSMtuChange>true</allowGuestOSMtuChange>",
			"<sriovBacking><physicalFunctionBacking>",
			"<id>0000:3b:00.0</id>",
			"<virtualFunctionIndex>3</virtualFunctionIndex>",
		},
	}),
	Entry("VirtualVmxnet3Vrdma", populatedCard{
		new: func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualVmxnet3Vrdma{DeviceProtocol: "rocev2"}
			populatedVmxnet3(&card.VirtualVmxnet3)
			return card
		},
		xsiType:  "VirtualVmxnet3Vrdma",
		elements: append([]string{"<deviceProtocol>rocev2</deviceProtocol>"}, vmxnet3Elements...),
	}),
}

// commonElements are the SOAP elements of the fields populateEthernetCard
// sets that an attachment edit preserves.
var commonElements = []string{
	"<operation>edit</operation>",
	"<key>4000</key>",
	"<controllerKey>100</controllerKey>",
	"<unitNumber>7</unitNumber>",
	"<pciSlotNumber>192</pciSlotNumber>",
	"<numaNode>1</numaNode>",
	"<groupInstanceKey>3</groupInstanceKey>",
	"<wakeOnLanEnabled>true</wakeOnLanEnabled>",
	"<reservation>100</reservation>",
	"<limit>1000</limit>",
	"<uptCompatibilityEnabled>false</uptCompatibilityEnabled>",
	"<startConnected>true</startConnected>",
	"<allowGuestControl>true</allowGuestControl>",
	"<connected>true</connected>",
}

// onlySpecXML returns the SOAP encoding of the single device change in c.
func onlySpecXML(c *network.DeviceChanges) string {
	specs := c.Specs()
	ExpectWithOffset(1, specs).To(HaveLen(1))
	data, err := xml.Marshal(specs[0])
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return string(data)
}

// deviceTypeElement returns the opening of the SOAP device element of a card
// with key 4000 and the given type. The standalone encoder spells the xsi
// namespace prefix out; the closing quote and the following key element
// keep one type from matching another type whose name it prefixes.
func deviceTypeElement(xsiType string) string {
	return fmt.Sprintf(`_XMLSchema-instance:type="%s"><key>4000</key>`, xsiType)
}

// copyForTest returns an independent typed copy of dev.
func copyForTest(dev vimtypes.BaseVirtualDevice) vimtypes.BaseVirtualDevice {
	out, err := network.NewDeviceChanges().Edit(network.NewObservedAdapterForTest(dev))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return out
}

func ethCardOf(dev vimtypes.BaseVirtualDevice) *vimtypes.VirtualEthernetCard {
	return dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
}

func dvpgAttachment(pg string, extID network.FieldIntent, pinnedMAC string) *network.Attachment {
	return &network.Attachment{
		Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: pg},
		ExternalID: extID,
		MAC:        network.MACPolicy{Pinned: pinnedMAC},
	}
}

func setIntent(value string) network.FieldIntent {
	return network.FieldIntent{Op: network.IntentSet, Value: value}
}

var keepIntent = network.FieldIntent{Op: network.IntentKeep}

var _ = Describe("BuildAdd", Label(testlabels.API), func() {

	DescribeTable("builds the same card as CreateVirtualEthernetCard",
		func(mac, extID string) {
			backing := charDVPGBacking(matchSwitch, matchPGA)

			expected, err := network.CreateVirtualEthernetCard(
				pkgcfg.NewContextWithDefaultConfig(),
				network.Device{
					InterfaceName: "eth0",
					Backing:       fakeNetworkRef{backing: backing},
					MacAddress:    mac,
					ExternalID:    extID,
				},
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"})
			Expect(err).ToNot(HaveOccurred())

			target := dvpgAttachment(matchPGA, keepIntent, mac)
			if extID != "" {
				target.ExternalID = setIntent(extID)
			}
			card, err := network.BuildAdd(network.AddInput{
				Interface: "eth0",
				Target:    target,
				Backing:   backing,
			})
			Expect(err).ToNot(HaveOccurred())

			// Both cards carry a random temporary key.
			expected.GetVirtualDevice().Key = 0
			card.GetVirtualDevice().Key = 0
			Expect(card).To(Equal(expected))
			Expect(xmlDevice(card)).To(Equal(xmlDevice(expected)))
			Expect(ethCardOf(card).Backing).To(BeIdenticalTo(backing))
			Expect(ethCardOf(card).Connectable).To(BeNil())
		},
		Entry("generated MAC address", "", ""),
		Entry("pinned MAC address and external ID", matchMACA, matchExtA),
	)

	It("writes a pinned MAC address as given", func() {
		card, err := network.BuildAdd(network.AddInput{
			Target:  dvpgAttachment(matchPGA, keepIntent, "00:50:56:AA:BB:CC"),
			Backing: charDVPGBacking(matchSwitch, matchPGA),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(ethCardOf(card).MacAddress).To(Equal("00:50:56:AA:BB:CC"))
		Expect(ethCardOf(card).AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeManual)))
	})

	It("places the card at a copy of the unit number", func() {
		unitNumber := ptr.To[int32](9)
		card, err := network.BuildAdd(network.AddInput{
			Target:     dvpgAttachment(matchPGA, keepIntent, ""),
			Backing:    charDVPGBacking(matchSwitch, matchPGA),
			UnitNumber: unitNumber,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(card).To(BeAssignableToTypeOf(&vimtypes.VirtualVmxnet3{}))
		Expect(card.GetVirtualDevice().UnitNumber).To(HaveValue(Equal(int32(9))))
		Expect(card.GetVirtualDevice().UnitNumber).ToNot(BeIdenticalTo(unitNumber))
		Expect(card.GetVirtualDevice().ControllerKey).To(BeZero())
	})

	DescribeTable("writes no external ID unless it is set",
		func(intent network.FieldIntent) {
			card, err := network.BuildAdd(network.AddInput{
				Target:  dvpgAttachment(matchPGA, intent, ""),
				Backing: charDVPGBacking(matchSwitch, matchPGA),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCardOf(card).ExternalId).To(BeEmpty())
			Expect(xmlDevice(card)).ToNot(ContainSubstring("externalId"))
		},
		Entry("Keep", keepIntent),
		Entry("Clear", network.FieldIntent{Op: network.IntentClear}),
	)

	It("fails without a target or a backing", func() {
		_, err := network.BuildAdd(network.AddInput{
			Interface: "eth0",
			Backing:   charDVPGBacking(matchSwitch, matchPGA),
		})
		Expect(err).To(MatchError(ContainSubstring(`"eth0" has no target attachment`)))

		_, err = network.BuildAdd(network.AddInput{
			Interface: "eth0",
			Target:    dvpgAttachment(matchPGA, keepIntent, ""),
		})
		Expect(err).To(MatchError(ContainSubstring(`"eth0" has no Ethernet card backing`)))
	})
})

var _ = Describe("BuildAttachmentEdit", Label(testlabels.API), func() {

	var c *network.DeviceChanges

	BeforeEach(func() {
		c = network.NewDeviceChanges()
	})

	// expectUnchanged asserts that a failed build left c without a change for
	// the adapter.
	expectUnchanged := func(key int32) {
		ExpectWithOffset(1, c.Len()).To(BeZero())
		_, ok := c.Lookup(key)
		ExpectWithOffset(1, ok).To(BeFalse())
	}

	DescribeTable("sets a new attachment on the wire",
		func(pc populatedCard) {
			observed := pc.new()
			// A subnet ID cannot be cleared on the wire yet.
			ethCardOf(observed).SubnetId = ""
			before := xmlDevice(observed)

			freshBacking := charDVPGBacking(matchSwitch, matchPGB)
			dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{
					Target:     dvpgAttachment(matchPGB, setIntent(matchExtB), matchMACB),
					Backing:    freshBacking,
					PowerState: vimtypes.VirtualMachinePowerStatePoweredOff,
				})
			Expect(err).ToNot(HaveOccurred())

			By("keeping the concrete type and never mutating the observed device")
			Expect(fmt.Sprintf("%T", dev)).To(Equal(fmt.Sprintf("%T", observed)))
			Expect(dev).ToNot(BeIdenticalTo(observed))
			Expect(xmlDevice(observed)).To(Equal(before))

			By("changing only the attachment fields")
			expected := copyForTest(observed)
			ethCardOf(expected).Backing = freshBacking
			ethCardOf(expected).ExternalId = matchExtB
			ethCardOf(expected).MacAddress = matchMACB
			ethCardOf(expected).AddressType = string(vimtypes.VirtualEthernetCardMacTypeManual)
			Expect(dev).To(Equal(expected))
			Expect(ethCardOf(dev).Backing).To(BeIdenticalTo(freshBacking))

			By("serializing the edit with every preserved and set field")
			got := onlySpecXML(c)
			Expect(got).To(ContainSubstring(deviceTypeElement(pc.xsiType)))
			for _, elem := range append(append([]string{}, commonElements...), pc.elements...) {
				Expect(got).To(ContainSubstring(elem))
			}
			Expect(got).To(ContainSubstring("<externalId>" + matchExtB + "</externalId>"))
			Expect(got).To(ContainSubstring("<macAddress>" + matchMACB + "</macAddress>"))
			Expect(got).To(ContainSubstring("<addressType>manual</addressType>"))
			Expect(got).To(ContainSubstring("<portgroupKey>" + matchPGB + "</portgroupKey>"))
			Expect(got).ToNot(ContainSubstring("fileOperation"))
			Expect(got).ToNot(ContainSubstring(matchPGA))
			Expect(got).ToNot(ContainSubstring(matchExtA))
			Expect(got).ToNot(ContainSubstring(matchMACA))
			Expect(got).ToNot(ContainSubstring("portKey"))
			Expect(got).ToNot(ContainSubstring("connectionCookie"))
			Expect(got).ToNot(ContainSubstring("subnetId"))
		},
		populatedCards,
	)

	DescribeTable("keeps an unchanged attachment on the wire",
		func(pc populatedCard) {
			observed := pc.new()
			ethCardOf(observed).AddressType = string(vimtypes.VirtualEthernetCardMacTypeAssigned)
			before := xmlDevice(observed)

			dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{
					Target: dvpgAttachment(matchPGA, keepIntent, ""),
					// The backing is unchanged, so a given backing is not
					// used.
					Backing:    charDVPGBacking(matchSwitch, matchPGB),
					PowerState: vimtypes.VirtualMachinePowerStatePoweredOn,
				})
			Expect(err).ToNot(HaveOccurred())

			Expect(fmt.Sprintf("%T", dev)).To(Equal(fmt.Sprintf("%T", observed)))
			Expect(dev).To(Equal(observed))
			Expect(xmlDevice(observed)).To(Equal(before))

			got := onlySpecXML(c)
			Expect(got).To(ContainSubstring(deviceTypeElement(pc.xsiType)))
			for _, elem := range append(append([]string{}, commonElements...), pc.elements...) {
				Expect(got).To(ContainSubstring(elem))
			}
			Expect(got).To(ContainSubstring("<externalId>" + matchExtA + "</externalId>"))
			Expect(got).To(ContainSubstring("<macAddress>" + matchMACA + "</macAddress>"))
			Expect(got).To(ContainSubstring("<addressType>assigned</addressType>"))
			Expect(got).To(ContainSubstring("<portgroupKey>" + matchPGA + "</portgroupKey>"))
			Expect(got).To(ContainSubstring("<portKey>42</portKey>"))
			Expect(got).To(ContainSubstring("<connectionCookie>7</connectionCookie>"))
			Expect(got).To(ContainSubstring("<subnetId>subnet-a</subnetId>"))
			Expect(got).ToNot(ContainSubstring(matchPGB))
		},
		populatedCards,
	)

	Context("MAC transitions", func() {
		type macCase struct {
			targetPinned string
			addressType  vimtypes.VirtualEthernetCardMacType
			expectedType vimtypes.VirtualEthernetCardMacType
			expectedMAC  string
			clearErr     bool
		}

		DescribeTable("sets the address type and MAC address as a pair",
			func(tc macCase) {
				observed := dvpgCard(4000, 7, matchPGA)
				observed.AddressType = string(tc.addressType)
				observed.MacAddress = matchMACA
				before := xmlDevice(observed)

				dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
					network.AttachmentEditInput{
						Target:  dvpgAttachment(matchPGB, keepIntent, tc.targetPinned),
						Backing: charDVPGBacking(matchSwitch, matchPGB),
					})
				Expect(xmlDevice(observed)).To(Equal(before))

				if tc.clearErr {
					Expect(err).To(MatchError(network.ErrClearUnsupported))
					Expect(err).To(MatchError(ContainSubstring("manual MAC address")))
					expectUnchanged(4000)
					return
				}

				Expect(err).ToNot(HaveOccurred())
				Expect(ethCardOf(dev).AddressType).To(Equal(string(tc.expectedType)))
				Expect(ethCardOf(dev).MacAddress).To(Equal(tc.expectedMAC))
				got := onlySpecXML(c)
				Expect(got).To(ContainSubstring("<addressType>" + string(tc.expectedType) + "</addressType>"))
				Expect(got).To(ContainSubstring("<macAddress>" + tc.expectedMAC + "</macAddress>"))
			},
			Entry("pinned over Manual writes the pinned MAC address", macCase{
				targetPinned: matchMACB,
				addressType:  vimtypes.VirtualEthernetCardMacTypeManual,
				expectedType: vimtypes.VirtualEthernetCardMacTypeManual,
				expectedMAC:  matchMACB,
			}),
			Entry("pinned over Generated writes the pinned MAC address", macCase{
				targetPinned: matchMACB,
				addressType:  vimtypes.VirtualEthernetCardMacTypeGenerated,
				expectedType: vimtypes.VirtualEthernetCardMacTypeManual,
				expectedMAC:  matchMACB,
			}),
			Entry("pinned over Assigned writes the pinned MAC address", macCase{
				targetPinned: matchMACB,
				addressType:  vimtypes.VirtualEthernetCardMacTypeAssigned,
				expectedType: vimtypes.VirtualEthernetCardMacTypeManual,
				expectedMAC:  matchMACB,
			}),
			Entry("pinned over the same pinned MAC address keeps it", macCase{
				targetPinned: matchMACA,
				addressType:  vimtypes.VirtualEthernetCardMacTypeManual,
				expectedType: vimtypes.VirtualEthernetCardMacTypeManual,
				expectedMAC:  matchMACA,
			}),
			Entry("managed over Generated keeps the address and its type", macCase{
				addressType:  vimtypes.VirtualEthernetCardMacTypeGenerated,
				expectedType: vimtypes.VirtualEthernetCardMacTypeGenerated,
				expectedMAC:  matchMACA,
			}),
			Entry("managed over Assigned keeps the address and its type", macCase{
				addressType:  vimtypes.VirtualEthernetCardMacTypeAssigned,
				expectedType: vimtypes.VirtualEthernetCardMacTypeAssigned,
				expectedMAC:  matchMACA,
			}),
			Entry("managed over Manual is unsupported", macCase{
				addressType: vimtypes.VirtualEthernetCardMacTypeManual,
				clearErr:    true,
			}),
		)
	})

	Context("external ID", func() {
		var observed *vimtypes.VirtualVmxnet3

		BeforeEach(func() {
			observed = dvpgCard(4000, 7, matchPGA)
			observed.ExternalId = matchExtA
		})

		build := func(intent network.FieldIntent) (vimtypes.BaseVirtualDevice, error) {
			return network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{
					Target:  dvpgAttachment(matchPGB, intent, ""),
					Backing: charDVPGBacking(matchSwitch, matchPGB),
				})
		}

		It("keeps the external ID with Keep", func() {
			dev, err := build(keepIntent)
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCardOf(dev).ExternalId).To(Equal(matchExtA))
			Expect(onlySpecXML(c)).To(ContainSubstring("<externalId>" + matchExtA + "</externalId>"))
		})

		It("sets the external ID with Set", func() {
			dev, err := build(setIntent(matchExtB))
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCardOf(dev).ExternalId).To(Equal(matchExtB))
			Expect(onlySpecXML(c)).To(ContainSubstring("<externalId>" + matchExtB + "</externalId>"))
			Expect(observed.ExternalId).To(Equal(matchExtA))
		})

		DescribeTable("refuses to clear the external ID",
			func(hasExternalID bool, intent network.FieldIntent) {
				if !hasExternalID {
					observed.ExternalId = ""
				}
				_, err := build(intent)
				Expect(err).To(MatchError(network.ErrClearUnsupported))
				Expect(err).To(MatchError(ContainSubstring("external ID must be cleared")))
				expectUnchanged(4000)
			},
			Entry("Clear", true, network.FieldIntent{Op: network.IntentClear}),
			Entry("Set to an empty value", true, setIntent("")),
			Entry("Clear of an absent external ID", false, network.FieldIntent{Op: network.IntentClear}),
		)

		It("fails with an unknown external ID", func() {
			_, err := build(network.FieldIntent{Op: network.IntentUnknown})
			Expect(err).To(MatchError(ContainSubstring("external ID is not known")))
			Expect(err).ToNot(MatchError(network.ErrClearUnsupported))
			expectUnchanged(4000)
		})
	})

	Context("backing", func() {
		It("keeps a DV portgroup backing that identifies the target", func() {
			observed := dvpgCard(4000, 7, matchPGA)
			observed.Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo).Port.PortKey = "42"

			dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{Target: dvpgAttachment(matchPGA, setIntent(matchExtB), "")})
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCardOf(dev).Backing).To(Equal(observed.Backing))
			Expect(ethCardOf(dev).Backing).ToNot(BeIdenticalTo(observed.Backing))
			Expect(ethCardOf(dev).ExternalId).To(Equal(matchExtB))
		})

		It("keeps a standard network backing that identifies the target by name", func() {
			observed := matchCard(4000, 7, charCard{
				addressType: vimtypes.VirtualEthernetCardMacTypeGenerated,
				mac:         matchMACA,
				backing:     charStdBacking("VM Network"),
			})
			target := &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNetwork, ID: "VM Network"},
				ExternalID: keepIntent,
			}

			dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{Target: target})
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCardOf(dev).Backing).To(Equal(observed.Backing))
		})

		It("requires a fresh backing when the backing changes", func() {
			observed := dvpgCard(4000, 7, matchPGA)
			_, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{Target: dvpgAttachment(matchPGB, keepIntent, "")})
			Expect(err).To(MatchError(ContainSubstring("no Ethernet card backing is given")))
			expectUnchanged(4000)
		})

		Context("NSX segment", func() {
			nsxTarget := func(extID string) *network.Attachment {
				return &network.Attachment{
					Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: matchLSUUID},
					ExternalID: setIntent(extID),
					MAC:        network.MACPolicy{Pinned: matchMACA},
				}
			}

			It("keeps the backing while the external ID is unchanged", func() {
				observed := nsxCard(4000, matchExtA, matchMACA)
				dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
					network.AttachmentEditInput{
						Target:  nsxTarget(matchExtA),
						Backing: charDVPGBacking(matchSwitch, matchPGB),
					})
				Expect(err).ToNot(HaveOccurred())
				Expect(ethCardOf(dev).Backing).To(Equal(observed.Backing))
			})

			It("replaces the backing when the external ID changes", func() {
				observed := nsxCard(4000, matchExtA, matchMACA)
				fresh := charDVPGBacking(matchSwitch, matchPGB)
				dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
					network.AttachmentEditInput{Target: nsxTarget(matchExtB), Backing: fresh})
				Expect(err).ToNot(HaveOccurred())
				Expect(ethCardOf(dev).Backing).To(BeIdenticalTo(fresh))
				Expect(ethCardOf(dev).ExternalId).To(Equal(matchExtB))
				Expect(ethCardOf(dev).MacAddress).To(Equal(matchMACA))
			})
		})
	})

	Context("subnet ID", func() {
		var observed *vimtypes.VirtualVmxnet3

		BeforeEach(func() {
			observed = dvpgCard(4000, 7, matchPGA)
			observed.SubnetId = "subnet-a"
		})

		It("refuses to change the backing of an adapter with a subnet ID", func() {
			_, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{
					Target:  dvpgAttachment(matchPGB, keepIntent, ""),
					Backing: charDVPGBacking(matchSwitch, matchPGB),
				})
			Expect(err).To(MatchError(network.ErrClearUnsupported))
			Expect(err).To(MatchError(ContainSubstring(`subnet ID "subnet-a" must be cleared`)))
			expectUnchanged(4000)
		})

		It("keeps the subnet ID when the backing is unchanged", func() {
			dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{Target: dvpgAttachment(matchPGA, setIntent(matchExtB), "")})
			Expect(err).ToNot(HaveOccurred())
			Expect(ethCardOf(dev).SubnetId).To(Equal("subnet-a"))
			Expect(onlySpecXML(c)).To(ContainSubstring("<subnetId>subnet-a</subnetId>"))
		})
	})

	Context("connection", func() {
		var observed *vimtypes.VirtualVmxnet3

		BeforeEach(func() {
			observed = dvpgCard(4000, 7, matchPGA)
			observed.Connectable = &vimtypes.VirtualDeviceConnectInfo{
				Connected:         false,
				StartConnected:    false,
				AllowGuestControl: false,
				Status:            "ok",
			}
		})

		build := func(powerState vimtypes.VirtualMachinePowerState, reconnect bool) vimtypes.BaseVirtualDevice {
			dev, err := network.BuildAttachmentEdit(c, network.NewObservedAdapterForTest(observed),
				network.AttachmentEditInput{
					Target:     dvpgAttachment(matchPGB, setIntent(matchExtB), ""),
					Backing:    charDVPGBacking(matchSwitch, matchPGB),
					PowerState: powerState,
					Reconnect:  reconnect,
				})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return dev
		}

		It("reconnects a running adapter in the same edit that applies the attachment", func() {
			dev := build(vimtypes.VirtualMachinePowerStatePoweredOn, true)
			Expect(*ethCardOf(dev).Connectable).To(Equal(vimtypes.VirtualDeviceConnectInfo{
				Connected:         true,
				StartConnected:    true,
				AllowGuestControl: true,
				Status:            "ok",
			}))
			Expect(observed.Connectable.Connected).To(BeFalse())

			got := onlySpecXML(c)
			Expect(got).To(ContainSubstring("<portgroupKey>" + matchPGB + "</portgroupKey>"))
			Expect(got).To(ContainSubstring("<externalId>" + matchExtB + "</externalId>"))
			Expect(got).To(ContainSubstring(
				"<startConnected>true</startConnected><allowGuestControl>true</allowGuestControl><connected>true</connected>"))
		})

		DescribeTable("enables startup connection and guest control without a runtime connection when not running",
			func(powerState vimtypes.VirtualMachinePowerState) {
				dev := build(powerState, true)
				Expect(ethCardOf(dev).Connectable.Connected).To(BeFalse())
				Expect(ethCardOf(dev).Connectable.StartConnected).To(BeTrue())
				Expect(ethCardOf(dev).Connectable.AllowGuestControl).To(BeTrue())
			},
			Entry("powered off", vimtypes.VirtualMachinePowerStatePoweredOff),
			Entry("suspended", vimtypes.VirtualMachinePowerStateSuspended),
		)

		It("allocates missing connection information to reconnect", func() {
			observed.Connectable = nil
			dev := build(vimtypes.VirtualMachinePowerStatePoweredOn, true)
			Expect(ethCardOf(dev).Connectable).To(Equal(&vimtypes.VirtualDeviceConnectInfo{
				Connected:         true,
				StartConnected:    true,
				AllowGuestControl: true,
			}))
			Expect(observed.Connectable).To(BeNil())
		})

		It("keeps the connection settings without reconnecting", func() {
			dev := build(vimtypes.VirtualMachinePowerStatePoweredOn, false)
			Expect(ethCardOf(dev).Connectable).To(Equal(observed.Connectable))
			Expect(ethCardOf(dev).Connectable).ToNot(BeIdenticalTo(observed.Connectable))
		})

		It("keeps missing connection information without reconnecting", func() {
			observed.Connectable = nil
			dev := build(vimtypes.VirtualMachinePowerStatePoweredOn, false)
			Expect(ethCardOf(dev).Connectable).To(BeNil())
		})
	})

	Context("device changes", func() {
		It("edits the copy another contributor already edits", func() {
			observed := dvpgCard(4000, 7, matchPGA)
			adapter := network.NewObservedAdapterForTest(observed)
			tuned, err := c.Edit(adapter)
			Expect(err).ToNot(HaveOccurred())
			ethCardOf(tuned).WakeOnLanEnabled = ptr.To(true)

			dev, err := network.BuildAttachmentEdit(c, adapter, network.AttachmentEditInput{
				Target:  dvpgAttachment(matchPGB, keepIntent, ""),
				Backing: charDVPGBacking(matchSwitch, matchPGB),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(dev).To(BeIdenticalTo(tuned))
			Expect(ethCardOf(dev).WakeOnLanEnabled).To(HaveValue(BeTrue()))
			Expect(c.Specs()).To(HaveLen(1))
		})

		It("fails for an adapter being removed", func() {
			adapter := network.NewObservedAdapterForTest(dvpgCard(4000, 7, matchPGA))
			Expect(c.Remove(adapter)).To(Succeed())
			_, err := network.BuildAttachmentEdit(c, adapter, network.AttachmentEditInput{
				Target:  dvpgAttachment(matchPGB, keepIntent, ""),
				Backing: charDVPGBacking(matchSwitch, matchPGB),
			})
			Expect(err).To(MatchError(ContainSubstring("being removed")))
		})

		It("fails without an observed device or a target", func() {
			_, err := network.BuildAttachmentEdit(c, network.ObservedAdapter{Key: 4000},
				network.AttachmentEditInput{Target: dvpgAttachment(matchPGB, keepIntent, "")})
			Expect(err).To(MatchError(ContainSubstring("observed device is not available")))

			_, err = network.BuildAttachmentEdit(c,
				network.NewObservedAdapterForTest(dvpgCard(4000, 7, matchPGA)),
				network.AttachmentEditInput{})
			Expect(err).To(MatchError(ContainSubstring("no target attachment")))
			expectUnchanged(4000)
		})
	})
})

var _ = Describe("BuildDisconnect", Label(testlabels.API), func() {

	var c *network.DeviceChanges

	BeforeEach(func() {
		c = network.NewDeviceChanges()
	})

	DescribeTable("disconnects the adapter and keeps everything else on the wire",
		func(pc populatedCard) {
			observed := pc.new()
			before := xmlDevice(observed)

			dev, err := network.BuildDisconnect(c, network.NewObservedAdapterForTest(observed))
			Expect(err).ToNot(HaveOccurred())
			Expect(xmlDevice(observed)).To(Equal(before))

			expected := copyForTest(observed)
			connect := ethCardOf(expected).Connectable
			connect.Connected = false
			connect.StartConnected = false
			connect.AllowGuestControl = false
			Expect(dev).To(Equal(expected))

			got := onlySpecXML(c)
			Expect(got).To(ContainSubstring(deviceTypeElement(pc.xsiType)))
			Expect(got).To(ContainSubstring(
				"<startConnected>false</startConnected><allowGuestControl>false</allowGuestControl><connected>false</connected>"))
			Expect(got).To(ContainSubstring("<externalId>" + matchExtA + "</externalId>"))
			Expect(got).To(ContainSubstring("<portKey>42</portKey>"))
			Expect(got).To(ContainSubstring("<subnetId>subnet-a</subnetId>"))
			for _, elem := range pc.elements {
				Expect(got).To(ContainSubstring(elem))
			}
		},
		populatedCards,
	)

	It("allocates missing connection information", func() {
		observed := dvpgCard(4000, 7, matchPGA)
		dev, err := network.BuildDisconnect(c, network.NewObservedAdapterForTest(observed))
		Expect(err).ToNot(HaveOccurred())
		Expect(ethCardOf(dev).Connectable).To(Equal(&vimtypes.VirtualDeviceConnectInfo{}))
		Expect(observed.Connectable).To(BeNil())
		Expect(onlySpecXML(c)).To(ContainSubstring("<connected>false</connected>"))
	})

	It("fails without an observed device or for an adapter being removed", func() {
		_, err := network.BuildDisconnect(c, network.ObservedAdapter{Key: 4000})
		Expect(err).To(MatchError(ContainSubstring("observed device is not available")))
		Expect(c.Len()).To(BeZero())

		adapter := network.NewObservedAdapterForTest(dvpgCard(4000, 7, matchPGA))
		Expect(c.Remove(adapter)).To(Succeed())
		_, err = network.BuildDisconnect(c, adapter)
		Expect(err).To(MatchError(ContainSubstring("being removed")))
	})
})
