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
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
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
		ctx = pkgcfg.NewContextWithDefaultConfig()
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
					Expect(dc0.Device).To(Equal(ethCard))
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

var _ = Describe("ReconcileNetworkInterfaces unit numbers", func() {

	const (
		netA = "net-a"
		netB = "net-b"
	)

	// ethCard builds an ethernet card with the given properties. A generated
	// MAC (the common case) is an empty MAC with a non-Manual address type.
	ethCard := func(
		key int32,
		unit *int32,
		addressType, mac, externalID string,
		backing vimtypes.BaseVirtualDeviceBackingInfo,
	) vimtypes.BaseVirtualDevice {
		return &vimtypes.VirtualEthernetCard{
			VirtualDevice: vimtypes.VirtualDevice{
				Key:        key,
				UnitNumber: unit,
				Backing:    backing,
			},
			AddressType: addressType,
			MacAddress:  mac,
			ExternalId:  externalID,
		}
	}

	// ethCardV is ethCard for call sites that need the ethernet-card
	// interface (e.g. FindMatchingEthCard's desired-card argument).
	ethCardV := func(
		key int32,
		unit *int32,
		addressType, mac, externalID string,
		backing vimtypes.BaseVirtualDeviceBackingInfo,
	) vimtypes.BaseVirtualEthernetCard {
		return ethCard(key, unit, addressType, mac, externalID, backing).(vimtypes.BaseVirtualEthernetCard)
	}

	netBacking := func(name string) vimtypes.BaseVirtualDeviceBackingInfo {
		return &vimtypes.VirtualEthernetCardNetworkBackingInfo{
			VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{
				DeviceName: name,
			},
		}
	}

	result := func(name string, dev vimtypes.BaseVirtualDevice, unit *int32) network.Device {
		return network.Device{
			InterfaceName: name,
			EthCard:       dev,
			UnitNumber:    unit,
		}
	}

	generated := string(vimtypes.VirtualEthernetCardMacTypeGenerated)
	manual := string(vimtypes.VirtualEthernetCardMacTypeManual)

	ops := func(dcs []vimtypes.BaseVirtualDeviceConfigSpec) []vimtypes.VirtualDeviceConfigSpecOperation {
		out := make([]vimtypes.VirtualDeviceConfigSpecOperation, 0, len(dcs))
		for _, dc := range dcs {
			out = append(out, dc.GetVirtualDeviceConfigSpec().Operation)
		}
		return out
	}

	unitOf := func(dc vimtypes.BaseVirtualDeviceConfigSpec) *int32 {
		return dc.GetVirtualDeviceConfigSpec().Device.GetVirtualDevice().UnitNumber
	}

	Describe("FindMatchingEthCard exact-only semantics", func() {

		It("matches only the card at the declared unit number", func() {
			cards := object.VirtualDeviceList{
				ethCard(4000, ptr.To(int32(8)), generated, "", "", netBacking(netA)),
				ethCard(4001, ptr.To(int32(9)), generated, "", "", netBacking(netB)),
			}
			Expect(network.FindMatchingEthCard(
				cards,
				ethCardV(0, ptr.To(int32(9)), generated, "", "", netBacking(netA)),
				ptr.To(int32(9)))).To(Equal(1))
		})

		It("does not fall back to backing matching on a miss", func() {
			cards := object.VirtualDeviceList{
				ethCard(4000, ptr.To(int32(8)), generated, "", "", netBacking(netA)),
			}
			// Desired card backing-matches the card above, but declares a
			// different, unoccupied unit: no match, ever.
			Expect(network.FindMatchingEthCard(
				cards,
				ethCardV(0, ptr.To(int32(9)), generated, "", "", netBacking(netA)),
				ptr.To(int32(9)))).To(Equal(-1))
		})

		It("falls back to backing matching when no unit number is declared", func() {
			cards := object.VirtualDeviceList{
				ethCard(4000, ptr.To(int32(8)), generated, "", "", netBacking(netA)),
			}
			Expect(network.FindMatchingEthCard(
				cards,
				ethCardV(0, nil, generated, "", "", netBacking(netA)),
				nil)).To(Equal(0))
		})
	})

	Describe("two-pass claim and compare-then-replace", func() {

		var (
			results *network.NetworkInterfaceResults
			cards   object.VirtualDeviceList
			err     error
		)

		BeforeEach(func() {
			results = &network.NetworkInterfaceResults{}
		})

		JustBeforeEach(func() {
			err = nil
		})

		run := func() []vimtypes.BaseVirtualDeviceConfigSpec {
			ctx := pkgcfg.WithConfig(pkgcfg.Config{
				Features: pkgcfg.FeatureStates{VMNetworkUnitNumbers: true},
			})
			dcs, e := network.ReconcileNetworkInterfaces(ctx, results, cards)
			err = e
			Expect(err).ToNot(HaveOccurred())
			return dcs
		}

		It("no device change when the located device fully matches; adopts Key/MAC", func() {
			dev := ethCard(0, ptr.To(int32(9)), generated, "", "", netBacking(netA))
			curMAC := "aa:bb:cc:dd:ee:09"
			cards = object.VirtualDeviceList{
				ethCard(4008, ptr.To(int32(9)), generated, curMAC, "", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(9))),
			}

			dcs := run()
			Expect(dcs).To(BeEmpty())
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(4008)))
			Expect(results.Devices[0].MacAddress).To(Equal(curMAC))
			// The learned MAC is written onto the desired device too.
			Expect(dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().MacAddress).To(Equal(curMAC))
		})

		It("replaces when the backing differs; Remove precedes the Add at the same unit", func() {
			dev := ethCard(0, ptr.To(int32(9)), generated, "", "", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4008, ptr.To(int32(9)), generated, "aa:bb:cc:dd:ee:09", "", netBacking(netB)),
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(9))),
			}

			dcs := run()
			Expect(ops(dcs)).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))
			rmDC := dcs[0].GetVirtualDeviceConfigSpec()
			addDC := dcs[1].GetVirtualDeviceConfigSpec()
			Expect(rmDC.Device.GetVirtualDevice().Key).To(Equal(int32(4008)))
			Expect(unitOf(dcs[1])).To(Equal(ptr.To(int32(9))))
			Expect(addDC.Device.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().Backing).
				To(Equal(dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().Backing))
			// I4: do not adopt the removed device's Key or MAC.
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(0)))
			Expect(results.Devices[0].MacAddress).To(BeEmpty())
			Expect(results.UpdatedEthCards).To(BeTrue())
		})

		It("replaces when the desired ExternalID differs", func() {
			dev := ethCard(0, ptr.To(int32(9)), manual, "aa:bb:cc:dd:ee:09", "ext-new", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4008, ptr.To(int32(9)), manual, "aa:bb:cc:dd:ee:09", "ext-old", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(9))),
			}

			dcs := run()
			Expect(ops(dcs)).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))
		})

		It("replaces when the desired MAC is Manual and differs", func() {
			dev := ethCard(0, ptr.To(int32(9)), manual, "aa:bb:cc:dd:ee:01", "", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4008, ptr.To(int32(9)), manual, "aa:bb:cc:dd:ee:09", "", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(9))),
			}

			dcs := run()
			Expect(ops(dcs)).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))
		})

		It("does not compare a Generated desired MAC: adopts the located device's", func() {
			// The main false-churn regression: only what the desired state
			// specifies is compared. A Generated MAC is unspecified, so a
			// differing observed MAC must not trigger a replace.
			dev := ethCard(0, ptr.To(int32(9)), generated, "", "", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4008, ptr.To(int32(9)), manual, "aa:bb:cc:dd:ee:09", "", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(9))),
			}

			dcs := run()
			Expect(dcs).To(BeEmpty())
			Expect(results.Devices[0].MacAddress).To(Equal("aa:bb:cc:dd:ee:09"))
		})

		It("leaves a located device of a differing type alone (type excluded, I2)", func() {
			// Desired devices are always built as the default vmxnet3 type; a
			// class-ConfigSpec E1000e at the declared slot must not churn.
			dev := ethCard(0, ptr.To(int32(9)), generated, "", "", netBacking(netA))
			vmx := &vimtypes.VirtualVmxnet3{}
			vmx.VirtualEthernetCard = *dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
			dev = vmx
			e1000e := &vimtypes.VirtualE1000e{}
			e1000e.VirtualEthernetCard = *ethCardV(
				4008, ptr.To(int32(9)), generated, "aa:bb:cc:dd:ee:09", "", netBacking(netA)).GetVirtualEthernetCard()
			cards = object.VirtualDeviceList{e1000e}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(9))),
			}

			dcs := run()
			Expect(dcs).To(BeEmpty())
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(4008)))
		})

		It("flag off: a unit number on the result is ignored (fallback matching)", func() {
			// With the feature off, r.UnitNumber is nil in production (the
			// population is flag-gated); a directly-constructed result with a
			// unit number must likewise not trigger the exact-only path. The
			// desired card backing-matches a device at a DIFFERENT slot, so
			// exact-only matching would miss and Add — fallback matching, as
			// before this feature, claims the backing-matching device instead.
			ctx := pkgcfg.NewContextWithDefaultConfig() // default config: flag off
			dev := ethCard(0, ptr.To(int32(12)), generated, "", "", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4000, ptr.To(int32(8)), generated, "aa:bb:cc:dd:ee:08", "", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(12))),
			}

			dcs, err := network.ReconcileNetworkInterfaces(ctx, results, cards)
			Expect(err).ToNot(HaveOccurred())
			Expect(dcs).To(BeEmpty())
			// Fallback match claimed the device: Key and MAC adopted.
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(4000)))
			Expect(results.Devices[0].MacAddress).To(Equal("aa:bb:cc:dd:ee:08"))
			Expect(results.UpdatedEthCards).To(BeFalse())
		})

		It("numbered miss does NOT fall through to a backing match or orphaned-CR edit", func() {
			dev := ethCard(0, ptr.To(int32(12)), generated, "", "", netBacking(netA))
			// A DVP-backed card would match the orphaned NetworkInterface CR
			// below via findMatchingEthCardNetOpNetIf (NetworkID == portgroup
			// key) if the miss were ever routed to the orphaned-CR path — it
			// must not be.
			cards = object.VirtualDeviceList{
				// Backing-matches the desired card but sits at a different slot.
				ethCard(4000, ptr.To(int32(8)), generated, "", "", netBacking(netA)),
				&vimtypes.VirtualEthernetCard{
					VirtualDevice: vimtypes.VirtualDevice{
						Key: 4001,
						Backing: &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
							Port: vimtypes.DistributedVirtualSwitchPortConnection{
								PortgroupKey: "pg-1",
							},
						},
					},
				},
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(12))),
			}
			results.OrphanedNetworkInterfaces = []ctrlclient.Object{
				&netopv1alpha1.NetworkInterface{
					ObjectMeta: metav1.ObjectMeta{
						Name: "orphaned-if",
						Labels: map[string]string{
							network.VMInterfaceNameLabel: "eth0",
						},
					},
					Status: netopv1alpha1.NetworkInterfaceStatus{
						NetworkID: "pg-1",
					},
				},
			}

			dcs := run()
			Expect(ops(dcs)).To(ConsistOf(
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			))
			// No Edit: the miss must not reach the orphaned-CR path.
			Expect(ops(dcs)).ToNot(ContainElement(vimtypes.VirtualDeviceConfigSpecOperationEdit))
			Expect(results.UpdatedEthCards).To(BeTrue())
		})

		It("un-numbered result before a numbered one cannot steal its device", func() {
			// The two-pass claim order (plan Design point 1): pass 1 claims
			// devices for numbered results FIRST, so a preceding un-numbered
			// result must not claim — by backing — the device the numbered
			// result declares as its identity.
			unnumbered := ethCard(0, nil, generated, "", "", netBacking(netA))
			numbered := ethCard(0, ptr.To(int32(9)), generated, "", "", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4008, ptr.To(int32(9)), generated, "aa:bb:cc:dd:ee:09", "", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", unnumbered, nil),
				result("eth1", numbered, ptr.To(int32(9))),
			}

			dcs := run()
			// The numbered result claims the device (full match: no change);
			// the un-numbered result finds nothing left and Adds.
			Expect(ops(dcs)).To(ConsistOf(vimtypes.VirtualDeviceConfigSpecOperationAdd))
			Expect(results.Devices[1].EthCardKey).To(Equal(int32(4008)))
			Expect(results.Devices[1].MacAddress).To(Equal("aa:bb:cc:dd:ee:09"))
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(0)))
			Expect(results.UpdatedEthCards).To(BeTrue())
		})

		It("renumber to an empty slot: old device removed, new device added", func() {
			dev := ethCard(0, ptr.To(int32(9)), generated, "", "", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4007, ptr.To(int32(8)), generated, "aa:bb:cc:dd:ee:08", "", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", dev, ptr.To(int32(9))),
			}

			dcs := run()
			Expect(ops(dcs)).To(Equal([]vimtypes.VirtualDeviceConfigSpecOperation{
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			}))
			// The old device is gone; the Add is built fresh: no copied MAC.
			Expect(dcs[0].GetVirtualDeviceConfigSpec().Device.GetVirtualDevice().Key).
				To(Equal(int32(4007)))
			Expect(dcs[1].GetVirtualDeviceConfigSpec().Device.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().MacAddress).
				To(BeEmpty())
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(0)))
			Expect(results.Devices[0].MacAddress).To(BeEmpty())
		})

		It("swap of two differing interfaces: each slot ends up with its own identity", func() {
			// eth0 moves 8 -> 9 and eth1 moves 9 -> 8; the devices at those
			// slots belong to the other interface. Each side replaces the
			// device it finds: no cross-inheritance of backing/MAC.
			dev0 := ethCard(0, ptr.To(int32(9)), manual, "aa:bb:cc:dd:ee:01", "ext-0", netBacking(netA))
			dev1 := ethCard(0, ptr.To(int32(8)), manual, "aa:bb:cc:dd:ee:02", "ext-1", netBacking(netB))
			cards = object.VirtualDeviceList{
				ethCard(4007, ptr.To(int32(8)), manual, "aa:bb:cc:dd:ee:01", "ext-0", netBacking(netA)),
				ethCard(4008, ptr.To(int32(9)), manual, "aa:bb:cc:dd:ee:02", "ext-1", netBacking(netB)),
			}
			results.Devices = []network.Device{
				result("eth0", dev0, ptr.To(int32(9))),
				result("eth1", dev1, ptr.To(int32(8))),
			}

			dcs := run()
			Expect(ops(dcs)).To(ConsistOf(
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
				vimtypes.VirtualDeviceConfigSpecOperationAdd,
			))
			// All removes precede all adds.
			Expect(ops(dcs)[:2]).To(ConsistOf(
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
				vimtypes.VirtualDeviceConfigSpecOperationRemove,
			))
			// Each Add carries its own interface's identity (and unit).
			var add0, add1 vimtypes.BaseVirtualDevice
			for _, dc := range dcs {
				vd := dc.GetVirtualDeviceConfigSpec()
				if vd.Operation == vimtypes.VirtualDeviceConfigSpecOperationAdd {
					ethDev := vd.Device.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
					if ethDev.MacAddress == "aa:bb:cc:dd:ee:01" {
						add0 = vd.Device
					} else if ethDev.MacAddress == "aa:bb:cc:dd:ee:02" {
						add1 = vd.Device
					}
				}
			}
			Expect(add0).ToNot(BeNil())
			Expect(add1).ToNot(BeNil())
			Expect(add0.GetVirtualDevice().UnitNumber).To(Equal(ptr.To(int32(9))))
			Expect(add1.GetVirtualDevice().UnitNumber).To(Equal(ptr.To(int32(8))))
			// Each Add carries its own interface's backing and ExternalID —
			// no cross-inheritance of the other interface's identity.
			add0Eth := add0.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
			add1Eth := add1.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
			Expect(add0Eth.Backing).To(Equal(dev0.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().Backing))
			Expect(add0Eth.ExternalId).To(Equal("ext-0"))
			Expect(add1Eth.Backing).To(Equal(dev1.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard().Backing))
			Expect(add1Eth.ExternalId).To(Equal("ext-1"))
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(0)))
			Expect(results.Devices[1].EthCardKey).To(Equal(int32(0)))
		})

		It("swap of two agreeing interfaces: zero device changes", func() {
			// Same network, Generated MACs: nothing distinguishing is pinned,
			// so the interfaces simply trade slots with no device changes.
			dev0 := ethCard(0, ptr.To(int32(9)), generated, "", "", netBacking(netA))
			dev1 := ethCard(0, ptr.To(int32(8)), generated, "", "", netBacking(netA))
			cards = object.VirtualDeviceList{
				ethCard(4007, ptr.To(int32(8)), generated, "aa:bb:cc:dd:ee:08", "", netBacking(netA)),
				ethCard(4008, ptr.To(int32(9)), generated, "aa:bb:cc:dd:ee:09", "", netBacking(netA)),
			}
			results.Devices = []network.Device{
				result("eth0", dev0, ptr.To(int32(9))),
				result("eth1", dev1, ptr.To(int32(8))),
			}

			dcs := run()
			Expect(dcs).To(BeEmpty())
			Expect(results.Devices[0].EthCardKey).To(Equal(int32(4008)))
			Expect(results.Devices[1].EthCardKey).To(Equal(int32(4007)))
		})
	})
})
