// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/govmomi/vim25/xml"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
)

// fakeNetworkRef is a network reference whose Ethernet card backing is known
// without vCenter I/O.
type fakeNetworkRef struct {
	backing vimtypes.BaseVirtualDeviceBackingInfo
	err     error
}

var _ object.NetworkReference = fakeNetworkRef{}

func (n fakeNetworkRef) Reference() vimtypes.ManagedObjectReference {
	return vimtypes.ManagedObjectReference{Type: "Network", Value: "fake-network"}
}

func (n fakeNetworkRef) GetInventoryPath() string {
	return ""
}

func (n fakeNetworkRef) EthernetCardBackingInfo(context.Context) (vimtypes.BaseVirtualDeviceBackingInfo, error) {
	return n.backing, n.err
}

func poweredOffInput(names ...string) network.PlanInput {
	return network.PlanInput{
		PowerState:   vimtypes.VirtualMachinePowerStatePoweredOff,
		ApplyChanges: true,
		CardBackings: pgBackings(names...),
	}
}

// pgBackings returns the card backing of each named interface. An interface
// eth<N> is backed by a portgroup only through its Target; any DVPG backing
// works here because the backing itself is opaque to the planner.
func pgBackings(names ...string) map[string]vimtypes.BaseVirtualDeviceBackingInfo {
	out := map[string]vimtypes.BaseVirtualDeviceBackingInfo{}
	for _, name := range names {
		out[name] = charDVPGBacking(matchSwitch, "pg-for-"+name)
	}
	return out
}

func matchAndPlan(ctx context.Context, info *network.Info, in network.PlanInput) *network.Plan {
	network.MatchInterfaces(ctx, info)
	return network.BuildPlan(info, in)
}

// specOps renders the device changes as "Operation:key" strings.
func specOps(p *network.Plan) []string {
	var out []string
	for _, s := range p.DeviceChanges().Specs() {
		spec := s.GetVirtualDeviceConfigSpec()
		out = append(out, fmt.Sprintf("%s:%d", spec.Operation, spec.Device.GetVirtualDevice().Key))
	}
	return out
}

func actionKinds(p *network.Plan) []string {
	var out []string
	for _, a := range p.Actions {
		name := a.Interface
		if name == "" {
			name = fmt.Sprintf("adapter-%d", a.Adapter)
		}
		out = append(out, fmt.Sprintf("%s=%s", name, a.Kind))
	}
	return out
}

func encodeDevices(devs ...vimtypes.BaseVirtualDevice) string {
	var specs []vimtypes.BaseVirtualDeviceConfigSpec
	for _, dev := range devs {
		specs = append(specs, &vimtypes.VirtualDeviceConfigSpec{Device: dev})
	}
	return encodeSpecs(specs)
}

func encodeSpecs(specs []vimtypes.BaseVirtualDeviceConfigSpec) string {
	var w bytes.Buffer
	ExpectWithOffset(1, vimtypes.NewJSONEncoder(&w).Encode(
		vimtypes.VirtualMachineConfigSpec{DeviceChange: specs})).To(Succeed())
	return w.String()
}

func adapterDevices(info *network.Info) []vimtypes.BaseVirtualDevice {
	var out []vimtypes.BaseVirtualDevice
	for _, a := range info.Adapters {
		out = append(out, network.ObservedAdapterDeviceForTest(a))
	}
	return out
}

// xmlDevice returns the SOAP encoding of a device.
func xmlDevice(dev vimtypes.BaseVirtualDevice) string {
	data, err := xml.Marshal(vimtypes.VirtualDeviceConfigSpec{Device: dev})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return string(data)
}

// populateEthernetCard sets every common ethernet card field, including the
// ones that other reconcilers own.
func populateEthernetCard(card *vimtypes.VirtualEthernetCard) {
	card.Key = 4000
	card.DeviceInfo = &vimtypes.Description{Label: "Network adapter 1", Summary: "pg-a"}
	card.Backing = &vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo{
		Port: vimtypes.DistributedVirtualSwitchPortConnection{
			SwitchUuid:       matchSwitch,
			PortgroupKey:     matchPGA,
			PortKey:          "42",
			ConnectionCookie: 7,
		},
	}
	card.Connectable = &vimtypes.VirtualDeviceConnectInfo{
		MigrateConnect:    "unset",
		StartConnected:    true,
		AllowGuestControl: true,
		Connected:         true,
		Status:            "ok",
	}
	card.SlotInfo = &vimtypes.VirtualDevicePciBusSlotInfo{PciSlotNumber: 192}
	card.ControllerKey = 100
	card.UnitNumber = ptr.To[int32](7)
	card.NumaNode = ptr.To[int32](1)
	card.DeviceGroupInfo = &vimtypes.VirtualDeviceDeviceGroupInfo{GroupInstanceKey: 3, SequenceId: 1}
	card.AddressType = string(vimtypes.VirtualEthernetCardMacTypeManual)
	card.MacAddress = matchMACA
	card.WakeOnLanEnabled = ptr.To(true)
	card.ResourceAllocation = &vimtypes.VirtualEthernetCardResourceAllocation{
		Reservation: ptr.To[int64](100),
		Share:       vimtypes.SharesInfo{Shares: 50, Level: vimtypes.SharesLevelCustom},
		Limit:       ptr.To[int64](1000),
	}
	card.ExternalId = matchExtA
	card.UptCompatibilityEnabled = ptr.To(false)
	card.SubnetId = "subnet-a"
}

func e1000eCard(key int32, pg, extID, mac, subnetID string) *vimtypes.VirtualE1000e {
	addressType := vimtypes.VirtualEthernetCardMacTypeGenerated
	if mac != "" {
		addressType = vimtypes.VirtualEthernetCardMacTypeAssigned
	}
	return charCard{
		key:         key,
		addressType: addressType,
		mac:         mac,
		externalID:  extID,
		subnetID:    subnetID,
		backing:     charDVPGBacking(matchSwitch, pg),
	}.e1000e()
}

var _ = Describe("BuildPlan", Label(testlabels.API), func() {

	var ctx context.Context

	BeforeEach(func() {
		ctx = matchContext(false)
	})

	It("returns an empty plan for a nil observation", func() {
		p := network.BuildPlan(nil, poweredOffInput())
		Expect(p.Actions).To(BeEmpty())
		Expect(p.DeviceChanges().Specs()).To(BeEmpty())
	})

	It("plans NoOp for a matched adapter with an unchanged attachment", func() {
		info := matchInfo(
			[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))},
			dvpgCard(4000, 7, matchPGA))

		p := matchAndPlan(ctx, info, poweredOffInput("eth0"))

		Expect(p.Actions).To(Equal([]network.Action{{
			Kind:      network.ActionNoOp,
			Interface: "eth0",
			Adapter:   4000,
			Reason:    network.PlanReasonAttachmentUnchanged,
		}}))
		Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		Expect(p.Identity).To(Equal(info.Identity))
	})

	Context("Add", func() {
		It("adds a VMXNET3 card with a generated MAC address", func() {
			info := matchInfo([]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))})
			in := poweredOffInput("eth0")

			p := matchAndPlan(ctx, info, in)

			Expect(p.Actions).To(HaveLen(1))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionAdd))
			Expect(p.Actions[0].Reason).To(Equal(network.PlanReasonDeviceNotPresent))
			Expect(p.Actions[0].DeviceKey).To(BeNumerically("<", 0))

			specs := p.DeviceChanges().Specs()
			Expect(specs).To(HaveLen(1))
			spec := specs[0].GetVirtualDeviceConfigSpec()
			Expect(spec.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationAdd))
			card, ok := spec.Device.(*vimtypes.VirtualVmxnet3)
			Expect(ok).To(BeTrue())
			Expect(card.Key).To(Equal(p.Actions[0].DeviceKey))
			Expect(card.Backing).To(BeIdenticalTo(in.CardBackings["eth0"]))
			Expect(card.AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeGenerated)))
			Expect(card.MacAddress).To(BeEmpty())
			Expect(card.ExternalId).To(BeEmpty())
			Expect(card.UnitNumber).To(BeNil())

			dev, ok := p.DeviceChanges().Lookup(p.Actions[0].DeviceKey)
			Expect(ok).To(BeTrue())
			Expect(dev).To(BeIdenticalTo(spec.Device))
		})

		It("adds a card with the provider's pinned MAC address and external ID", func() {
			target := &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: matchLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: matchExtA},
				MAC:        network.MACPolicy{Pinned: matchMACA},
			}
			info := matchInfo([]network.InterfaceState{readyIface("eth0", target)})

			p := matchAndPlan(ctx, info, poweredOffInput("eth0"))

			specs := p.DeviceChanges().Specs()
			Expect(specs).To(HaveLen(1))
			card := specs[0].GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualVmxnet3)
			Expect(card.AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeManual)))
			Expect(card.MacAddress).To(Equal(matchMACA))
			Expect(card.ExternalId).To(Equal(matchExtA))
		})

		It("assigns each added card a distinct temporary key", func() {
			info := matchInfo([]network.InterfaceState{
				readyIface("eth0", dvpgTarget(matchPGA)),
				readyIface("eth1", dvpgTarget(matchPGB)),
				readyIface("eth2", dvpgTarget(matchPGC)),
			})

			p := matchAndPlan(ctx, info, poweredOffInput("eth0", "eth1", "eth2"))

			keys := map[int32]struct{}{}
			for _, a := range p.Actions {
				Expect(a.Kind).To(Equal(network.ActionAdd))
				Expect(a.DeviceKey).To(BeNumerically("<", 0))
				keys[a.DeviceKey] = struct{}{}
			}
			Expect(keys).To(HaveLen(3))
			Expect(specOps(p)).To(Equal([]string{
				fmt.Sprintf("add:%d", p.Actions[0].DeviceKey),
				fmt.Sprintf("add:%d", p.Actions[1].DeviceKey),
				fmt.Sprintf("add:%d", p.Actions[2].DeviceKey),
			}))
		})

		It("defers an Add whose card backing is not resolved", func() {
			info := matchInfo([]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))})

			p := matchAndPlan(ctx, info, poweredOffInput())

			Expect(p.Actions).To(HaveLen(1))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionDeferred))
			Expect(p.Actions[0].Proposed).To(Equal(network.ActionAdd))
			Expect(p.Actions[0].Reason).To(Equal(network.PlanReasonBackingUnavailable))
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		})
	})

	Context("Remove", func() {
		It("removes unclaimed adapters in device order", func() {
			info := matchInfo(
				[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGB))},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGB),
				dvpgCard(4002, 9, matchPGC))

			p := matchAndPlan(ctx, info, poweredOffInput("eth0"))

			// eth0 claims 4001 by identity. Positional claims are not taken
			// because eth0 is already claimed.
			Expect(actionKinds(p)).To(Equal([]string{
				"eth0=NoOp", "adapter-4000=Remove", "adapter-4002=Remove",
			}))
			Expect(p.Actions[1].Reason).To(Equal(network.PlanReasonRemovalPending))
			Expect(specOps(p)).To(Equal([]string{"remove:4000", "remove:4002"}))
			Expect(p.DeviceChanges().Removed(4000)).To(BeTrue())
			Expect(p.DeviceChanges().Removed(4001)).To(BeFalse())
		})

		It("removes a retiring adapter", func() {
			info := matchInfo(nil, dvpgCard(4000, 7, matchPGA))
			network.SetInfoOwnedObjectsForTest(info, []ctrlclient.Object{
				orphanNetOP("vm-net-a-eth9", "eth9", matchPGA),
			})

			p := matchAndPlan(ctx, info, poweredOffInput())

			Expect(info.Disposition[4000]).To(Equal(network.AdapterDispositionRetiring))
			Expect(actionKinds(p)).To(Equal([]string{"adapter-4000=Remove"}))
			Expect(specOps(p)).To(Equal([]string{"remove:4000"}))
		})

		It("removes every adapter when the network is disabled", func() {
			info := matchInfo(nil, dvpgCard(4000, 7, matchPGA), dvpgCard(4001, 8, matchPGB))
			info.Enabled = false

			p := matchAndPlan(ctx, info, poweredOffInput())

			Expect(specOps(p)).To(Equal([]string{"remove:4000", "remove:4001"}))
		})

		It("defers a removal whose device is not available", func() {
			info := matchInfo(nil)
			info.Adapters = []network.ObservedAdapter{{Key: 4000}}
			info.Disposition = map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionUnclaimed,
			}

			p := network.BuildPlan(info, poweredOffInput())

			Expect(p.Actions).To(HaveLen(1))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionDeferred))
			Expect(p.Actions[0].Proposed).To(Equal(network.ActionRemove))
			Expect(p.Actions[0].Reason).To(Equal(network.PlanReasonDeviceChangeFailed))
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		})

		It("never removes a protected adapter", func() {
			By("an incomplete observation")
			info := matchInfo(nil, dvpgCard(4000, 7, matchPGA))
			info.Complete = false
			p := matchAndPlan(ctx, info, poweredOffInput())
			Expect(info.Disposition[4000]).To(Equal(network.AdapterDispositionProtected))
			Expect(p.Actions).To(BeEmpty())
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())

			By("an adapter without a disposition")
			info = matchInfo(nil, dvpgCard(4000, 7, matchPGA))
			p = network.BuildPlan(info, poweredOffInput())
			Expect(p.Actions).To(BeEmpty())
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		})
	})

	Context("provider not ready", func() {
		It("retains the adapter claimed by a pending interface", func() {
			info := matchInfo(
				[]network.InterfaceState{phaseIface("eth0", network.ResolutionPending)},
				dvpgCard(4000, 7, matchPGA))

			p := matchAndPlan(ctx, info, poweredOffInput())

			Expect(p.Actions).To(Equal([]network.Action{{
				Kind:      network.ActionNoOp,
				Interface: "eth0",
				Adapter:   4000,
				Reason:    network.PlanReasonProviderPending,
			}}))
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		})

		It("keeps a pending replacement connected until T027", func() {
			iface := phaseIface("eth0", network.ResolutionPending)
			info := matchInfo([]network.InterfaceState{iface}, dvpgCard(4000, 7, matchPGA))
			network.SetInfoOwnedObjectsForTest(info, []ctrlclient.Object{
				orphanNetOP("vm-net-a-eth0", "eth0", matchPGA),
			})

			in := poweredOffInput()
			in.Policy.EditAttachments = true
			p := matchAndPlan(ctx, info, in)

			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
			Expect(p.Actions).To(HaveLen(1))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionNoOp))
			Expect(p.Actions[0].Reason).To(Equal(network.PlanReasonReplacementPending))
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		})

		It("retains the entries of unclaimed pending interfaces", func() {
			info := matchInfo([]network.InterfaceState{
				readyIface("eth0", dvpgTarget(matchPGA)),
				phaseIface("eth1", network.ResolutionPending),
				phaseIface("eth2", network.ResolutionPending),
			}, dvpgCard(4000, 7, matchPGA))
			// eth0 claims the only adapter, so neither pending interface has
			// a claim.
			p := matchAndPlan(ctx, info, poweredOffInput("eth0"))

			Expect(actionKinds(p)).To(Equal([]string{
				"eth0=NoOp", "eth1=Deferred", "eth2=Deferred",
			}))
			for _, a := range p.Actions[1:] {
				Expect(a.Proposed).To(BeEmpty())
				Expect(a.Reason).To(Equal(network.PlanReasonProviderPending))
			}
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		})

		DescribeTable("failed and unknown providers",
			func(phase network.ResolutionPhase, reason string) {
				By("a claimed interface keeps its adapter")
				info := matchInfo(
					[]network.InterfaceState{phaseIface("eth0", phase)},
					dvpgCard(4000, 7, matchPGA))
				p := matchAndPlan(ctx, info, poweredOffInput())
				Expect(p.Actions).To(HaveLen(1))
				Expect(p.Actions[0].Kind).To(Equal(network.ActionNoOp))
				Expect(p.Actions[0].Adapter).To(Equal(int32(4000)))
				Expect(p.Actions[0].Reason).To(Equal(reason))
				Expect(p.DeviceChanges().Specs()).To(BeEmpty())

				By("an unclaimed interface is deferred")
				info = matchInfo([]network.InterfaceState{phaseIface("eth0", phase)})
				p = matchAndPlan(ctx, info, poweredOffInput())
				Expect(p.Actions).To(HaveLen(1))
				Expect(p.Actions[0].Kind).To(Equal(network.ActionDeferred))
				Expect(p.Actions[0].Proposed).To(BeEmpty())
				Expect(p.Actions[0].Reason).To(Equal(reason))
			},
			Entry("Failed", network.ResolutionFailed, network.PlanReasonProviderFailed),
			Entry("Unknown", network.ResolutionUnknown, network.PlanReasonProviderUnknown),
		)
	})

	It("defers an ambiguous interface and protects its candidates", func() {
		restore := network.SetAdapterRecordInterfaceForTest(
			func(network.ObservedAdapter, types.UID) (string, bool) { return "eth0", true })
		DeferCleanup(restore)

		info := matchInfo(
			[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))},
			dvpgCard(4000, 7, matchPGA),
			dvpgCard(4001, 8, matchPGA))

		p := matchAndPlan(ctx, info, poweredOffInput("eth0"))

		Expect(p.Actions).To(HaveLen(1))
		Expect(p.Actions[0].Kind).To(Equal(network.ActionDeferred))
		Expect(p.Actions[0].Reason).To(Equal(network.PlanReasonInterfaceMappingAmbiguous))
		Expect(p.DeviceChanges().Specs()).To(BeEmpty())
	})

	Context("attachment replacement", func() {
		It("edits the same-name orphan's adapter with MutableNetworks, preserving its type and key", func() {
			observed := e1000eCard(4000, matchPGA, "", "00:50:56:00:00:99", "subnet-old")
			observed.UnitNumber = ptr.To[int32](7)
			observed.Connectable = &vimtypes.VirtualDeviceConnectInfo{
				StartConnected: true, Connected: true, AllowGuestControl: true,
			}

			target := &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: matchPGB},
				ExternalID: network.FieldIntent{Op: network.IntentSet, Value: matchExtB},
				MAC:        network.MACPolicy{Pinned: matchMACB},
			}
			info := matchInfo([]network.InterfaceState{readyIface("eth0", target)}, observed)
			network.SetInfoOwnedObjectsForTest(info, []ctrlclient.Object{
				orphanNetOP("vm-net-a-eth0", "eth0", matchPGA),
			})
			before := encodeDevices(adapterDevices(info)...)

			in := poweredOffInput("eth0")
			in.MutableNetworks = true
			p := matchAndPlan(ctx, info, in)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchOrphan))
			Expect(p.Actions).To(HaveLen(1))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionEdit))
			Expect(p.Actions[0].Adapter).To(Equal(int32(4000)))
			Expect(p.Actions[0].Reason).To(Equal(network.PlanReasonAttachmentPending))

			specs := p.DeviceChanges().Specs()
			Expect(specs).To(HaveLen(1))
			spec := specs[0].GetVirtualDeviceConfigSpec()
			Expect(spec.Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationEdit))
			card, ok := spec.Device.(*vimtypes.VirtualE1000e)
			Expect(ok).To(BeTrue())
			Expect(card).ToNot(BeIdenticalTo(observed))
			Expect(card.Key).To(Equal(int32(4000)))
			Expect(card.UnitNumber).To(HaveValue(Equal(int32(7))))
			Expect(card.Connectable).To(Equal(observed.Connectable))
			Expect(card.Backing).To(BeIdenticalTo(in.CardBackings["eth0"]))
			Expect(card.AddressType).To(Equal(string(vimtypes.VirtualEthernetCardMacTypeManual)))
			Expect(card.MacAddress).To(Equal(matchMACB))
			Expect(card.ExternalId).To(Equal(matchExtB))
			Expect(card.SubnetId).To(BeEmpty())

			By("leaving the observed device unchanged")
			Expect(encodeDevices(adapterDevices(info)...)).To(Equal(before))
			Expect(observed.SubnetId).To(Equal("subnet-old"))

			dev, ok := p.DeviceChanges().Lookup(4000)
			Expect(ok).To(BeTrue())
			Expect(dev).To(BeIdenticalTo(spec.Device))
		})

		It("replaces the same-name orphan's adapter without MutableNetworks", func() {
			info := matchInfo(
				[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGB))},
				dvpgCard(4000, 7, matchPGA))
			network.SetInfoOwnedObjectsForTest(info, []ctrlclient.Object{
				orphanNetOP("vm-net-a-eth0", "eth0", matchPGA),
			})

			p := matchAndPlan(ctx, info, poweredOffInput("eth0"))

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchOrphan))
			Expect(p.Actions).To(HaveLen(1))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionReplace))
			Expect(p.Actions[0].Adapter).To(Equal(int32(4000)))
			Expect(specOps(p)).To(Equal([]string{
				"remove:4000", fmt.Sprintf("add:%d", p.Actions[0].DeviceKey),
			}))
		})

		// TODO(T026): When EditAttachments is wired, UnitNumber, Record,
		// Orphan, and Identity replacements may become Edits. A positional
		// replacement must remain Remove+Add, because replacement.md forbids
		// inferring an in-place change from position.
		It("never edits a positionally matched adapter, even with EditAttachments", func() {
			for _, editAttachments := range []bool{false, true} {
				info := matchInfo(
					[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGB))},
					dvpgCard(4000, 7, matchPGA))

				in := poweredOffInput("eth0")
				in.Policy.EditAttachments = editAttachments
				in.MutableNetworks = true
				p := matchAndPlan(ctx, info, in)

				Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchPositional))
				Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
				Expect(p.Actions).To(HaveLen(1))
				Expect(p.Actions[0].Kind).To(Equal(network.ActionReplace))
				Expect(specOps(p)).To(Equal([]string{
					"remove:4000", fmt.Sprintf("add:%d", p.Actions[0].DeviceKey),
				}))
			}
		})

		It("replaces the adapter at the declared unit number and converges", func() {
			ctx = matchContext(true)
			iface := numbered(readyIface("eth0", dvpgTarget(matchPGB)), 7)
			info := matchInfo([]network.InterfaceState{iface}, dvpgCard(4000, 7, matchPGA))

			in := poweredOffInput()
			in.UnitNumbers = true
			in.CardBackings = map[string]vimtypes.BaseVirtualDeviceBackingInfo{
				"eth0": charDVPGBacking(matchSwitch, matchPGB),
			}
			p := matchAndPlan(ctx, info, in)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchUnitNumber))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionReplace))
			Expect(specOps(p)).To(Equal([]string{
				"remove:4000", fmt.Sprintf("add:%d", p.Actions[0].DeviceKey),
			}))

			added, ok := p.DeviceChanges().Lookup(p.Actions[0].DeviceKey)
			Expect(ok).To(BeTrue())
			card := added.(*vimtypes.VirtualVmxnet3)
			Expect(card.UnitNumber).To(HaveValue(Equal(int32(7))))
			Expect(card.ControllerKey).To(BeZero())

			By("matching the resulting adapter on the next pass")
			result := *card
			result.Key = 4100
			result.MacAddress = "00:50:56:00:01:00"
			next := matchAndPlan(ctx, matchInfo([]network.InterfaceState{iface}, &result), in)
			Expect(next.Actions).To(Equal([]network.Action{{
				Kind:      network.ActionNoOp,
				Interface: "eth0",
				Adapter:   4100,
				Reason:    network.PlanReasonAttachmentUnchanged,
			}}))
			Expect(next.DeviceChanges().Specs()).To(BeEmpty())
		})

		It("places an added card at its declared unit number only with VMNetworkUnitNumbers", func() {
			ctx = matchContext(true)
			for _, unitNumbers := range []bool{false, true} {
				info := matchInfo([]network.InterfaceState{
					numbered(readyIface("eth0", dvpgTarget(matchPGA)), 9),
				})
				in := poweredOffInput("eth0")
				in.UnitNumbers = unitNumbers
				p := matchAndPlan(ctx, info, in)

				Expect(p.Actions[0].Kind).To(Equal(network.ActionAdd))
				added, ok := p.DeviceChanges().Lookup(p.Actions[0].DeviceKey)
				Expect(ok).To(BeTrue())
				if unitNumbers {
					Expect(added.GetVirtualDevice().UnitNumber).To(HaveValue(Equal(int32(9))))
				} else {
					Expect(added.GetVirtualDevice().UnitNumber).To(BeNil())
				}
			}
		})

		It("retains a matched adapter whose attachment cannot be compared", func() {
			// An NSX target with neither an external ID nor a pinned MAC
			// address carries no comparable identity.
			target := &network.Attachment{
				Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: matchLSUUID},
				ExternalID: network.FieldIntent{Op: network.IntentKeep},
			}
			info := matchInfo(
				[]network.InterfaceState{readyIface("eth0", target)},
				nsxCard(4000, matchExtA, matchMACA))

			p := matchAndPlan(ctx, info, poweredOffInput("eth0"))

			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
			Expect(p.Actions[0].Kind).To(Equal(network.ActionNoOp))
			Expect(p.Actions[0].Reason).To(Equal(network.PlanReasonAttachmentUnknown))
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		})
	})

	It("emits every removal before any addition when a slot is reused", func() {
		info := matchInfo([]network.InterfaceState{
			readyIface("eth0", dvpgTarget(matchPGA)),
			readyIface("eth1", dvpgTarget(matchPGB)),
			readyIface("eth2", dvpgTarget(matchPGC)),
		},
			dvpgCard(4000, 7, matchPGA),
			dvpgCard(4001, 8, "dvportgroup-old"),
			dvpgCard(4002, 9, "dvportgroup-extra"))
		// eth0 claims 4000 by identity; eth1 and eth2 are zipped with 4001
		// and 4002 and replace them.

		p := matchAndPlan(ctx, info, poweredOffInput("eth0", "eth1", "eth2"))

		Expect(actionKinds(p)).To(Equal([]string{"eth0=NoOp", "eth1=Replace", "eth2=Replace"}))
		Expect(specOps(p)).To(Equal([]string{
			"remove:4001",
			"remove:4002",
			fmt.Sprintf("add:%d", p.Actions[1].DeviceKey),
			fmt.Sprintf("add:%d", p.Actions[2].DeviceKey),
		}))
		Expect(p.Actions[1].DeviceKey).ToNot(Equal(p.Actions[2].DeviceKey))
	})

	Context("power state and dispatch path", func() {
		BeforeEach(func() {
			// eth2 is numbered so that it is not zipped with the extra
			// adapter, which is therefore removed while eth2 is added.
			ctx = matchContext(true)
		})

		newInfo := func() *network.Info {
			info := matchInfo([]network.InterfaceState{
				readyIface("eth0", dvpgTarget(matchPGA)),
				readyIface("eth1", dvpgTarget(matchPGB)),
				numbered(readyIface("eth2", dvpgTarget(matchPGC)), 10),
			},
				dvpgCard(4000, 7, matchPGA),
				e1000eCard(4001, "dvportgroup-old", "", "", ""),
				dvpgCard(4002, 9, "dvportgroup-extra"))
			network.SetInfoOwnedObjectsForTest(info, []ctrlclient.Object{
				orphanNetOP("vm-net-old-eth1", "eth1", "dvportgroup-old"),
			})
			return info
		}

		expectAllDeferred := func(p *network.Plan, reason string) {
			Expect(actionKinds(p)).To(Equal([]string{
				"eth0=NoOp", "eth1=Deferred", "eth2=Deferred", "adapter-4002=Deferred",
			}))
			Expect(p.Actions[1].Proposed).To(Equal(network.ActionEdit))
			Expect(p.Actions[2].Proposed).To(Equal(network.ActionAdd))
			Expect(p.Actions[3].Proposed).To(Equal(network.ActionRemove))
			for _, a := range p.Actions[1:] {
				Expect(a.Reason).To(Equal(reason))
				Expect(a.DeviceKey).To(BeZero())
				Expect(a.Message).ToNot(BeEmpty())
			}
			Expect(p.DeviceChanges().Specs()).To(BeEmpty())
		}

		It("applies the changes while powered off", func() {
			in := poweredOffInput("eth0", "eth1", "eth2")
			in.MutableNetworks = true
			p := matchAndPlan(ctx, newInfo(), in)
			Expect(actionKinds(p)).To(Equal([]string{
				"eth0=NoOp", "eth1=Edit", "eth2=Add", "adapter-4002=Remove",
			}))
			Expect(specOps(p)).To(Equal([]string{
				"remove:4002", "edit:4001", fmt.Sprintf("add:%d", p.Actions[2].DeviceKey),
			}))
		})

		DescribeTable("defers every hardware change unless powered off",
			func(powerState vimtypes.VirtualMachinePowerState) {
				in := poweredOffInput("eth0", "eth1", "eth2")
				in.MutableNetworks = true
				in.PowerState = powerState
				expectAllDeferred(matchAndPlan(ctx, newInfo(), in), network.PlanReasonPowerOffRequired)
			},
			Entry("powered on", vimtypes.VirtualMachinePowerStatePoweredOn),
			Entry("suspended", vimtypes.VirtualMachinePowerStateSuspended),
			Entry("unknown", vimtypes.VirtualMachinePowerState("")),
		)

		It("defers every hardware change when the dispatch path does not apply them", func() {
			in := poweredOffInput("eth0", "eth1", "eth2")
			in.MutableNetworks = true
			in.ApplyChanges = false
			expectAllDeferred(matchAndPlan(ctx, newInfo(), in), network.PlanReasonNetworkChangesNotAllowed)
		})
	})
})

var _ = Describe("DeviceChanges", Label(testlabels.API), func() {

	var (
		c       *network.DeviceChanges
		adapter network.ObservedAdapter
		other   network.ObservedAdapter
		e1000e  *vimtypes.VirtualE1000e
	)

	BeforeEach(func() {
		c = network.NewDeviceChanges()
		e1000e = e1000eCard(4000, matchPGA, matchExtA, matchMACA, "subnet-a")
		adapter = network.NewObservedAdapterForTest(e1000e)
		other = network.NewObservedAdapterForTest(dvpgCard(4001, 8, matchPGB))
	})

	It("returns one typed deep copy per key", func() {
		before := encodeDevices(e1000e)

		dev, err := c.Edit(adapter)
		Expect(err).ToNot(HaveOccurred())
		card, ok := dev.(*vimtypes.VirtualE1000e)
		Expect(ok).To(BeTrue())
		Expect(card).ToNot(BeIdenticalTo(e1000e))
		Expect(encodeDevices(card)).To(Equal(before))

		card.ExternalId = "changed"
		card.Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo).Port.PortgroupKey = "changed"
		Expect(encodeDevices(e1000e)).To(Equal(before))

		again, err := c.Edit(adapter)
		Expect(err).ToNot(HaveOccurred())
		Expect(again).To(BeIdenticalTo(dev))

		Expect(c.Specs()).To(HaveLen(1))
	})

	DescribeTable("copies every field of a fully populated card",
		func(newCard func() vimtypes.BaseVirtualDevice) {
			observed := newCard()
			before := xmlDevice(observed)

			dev, err := network.NewDeviceChanges().Edit(network.NewObservedAdapterForTest(observed))
			Expect(err).ToNot(HaveOccurred())
			Expect(dev).ToNot(BeIdenticalTo(observed))
			Expect(fmt.Sprintf("%T", dev)).To(Equal(fmt.Sprintf("%T", observed)))
			Expect(xmlDevice(dev)).To(Equal(before))
			Expect(dev).To(Equal(observed))
		},
		Entry("VMXNET3", func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualVmxnet3{
				Uptv2Enabled: ptr.To(true),
				StrictLatencyConfig: &vimtypes.VirtualVmxnet3StrictLatencyConfig{
					Allowed:            ptr.To(true),
					MeasureLatency:     ptr.To(false),
					MaxTxQueues:        4,
					MaxRxQueues:        8,
					TxDataRingDescSize: 256,
					RxDataRingDescSize: 512,
					DisableOffload:     "lro",
				},
			}
			populateEthernetCard(card.GetVirtualEthernetCard())
			return card
		}),
		Entry("E1000e", func() vimtypes.BaseVirtualDevice {
			card := &vimtypes.VirtualE1000e{}
			populateEthernetCard(card.GetVirtualEthernetCard())
			return card
		}),
	)

	It("rejects an Edit of a key being removed and a Remove of a key being edited", func() {
		Expect(c.Remove(adapter)).To(Succeed())
		Expect(c.Remove(adapter)).To(Succeed())
		_, err := c.Edit(adapter)
		Expect(err).To(MatchError(ContainSubstring("being removed")))

		_, err = c.Edit(other)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.Remove(other)).To(MatchError(ContainSubstring("being edited")))

		Expect(c.Removed(4000)).To(BeTrue())
		Expect(c.Removed(4001)).To(BeFalse())
		_, ok := c.Lookup(4000)
		Expect(ok).To(BeFalse())
		_, ok = c.Lookup(4001)
		Expect(ok).To(BeTrue())
	})

	It("rejects an adapter without a device", func() {
		_, err := c.Edit(network.ObservedAdapter{Key: 1})
		Expect(err).To(HaveOccurred())
		Expect(c.Remove(network.ObservedAdapter{Key: 1})).ToNot(Succeed())
		Expect(c.Len()).To(BeZero())
		Expect(c.Specs()).To(BeNil())
	})

	It("orders removals first, then edits and additions as made", func() {
		k1 := c.Add(&vimtypes.VirtualVmxnet3{})
		_, err := c.Edit(other)
		Expect(err).ToNot(HaveOccurred())
		k2 := c.Add(&vimtypes.VirtualVmxnet3{})
		Expect(c.Remove(adapter)).To(Succeed())

		Expect(k1).To(BeNumerically("<", 0))
		Expect(k2).To(BeNumerically("<", 0))
		Expect(k1).ToNot(Equal(k2))
		Expect(c.Len()).To(Equal(4))

		var ops []string
		for _, s := range c.Specs() {
			spec := s.GetVirtualDeviceConfigSpec()
			ops = append(ops, fmt.Sprintf("%s:%d", spec.Operation, spec.Device.GetVirtualDevice().Key))
		}
		Expect(ops).To(Equal([]string{
			"remove:4000",
			fmt.Sprintf("add:%d", k1),
			"edit:4001",
			fmt.Sprintf("add:%d", k2),
		}))
	})
})

var _ = Describe("EthernetCardBackings", Label(testlabels.API), func() {
	It("resolves the card backing of each Ready interface with a resolved network", func() {
		backingA := charDVPGBacking(matchSwitch, matchPGA)

		ready := readyIface("eth0", dvpgTarget(matchPGA))
		network.SetResolutionBackingForTest(&ready.Provider, fakeNetworkRef{backing: backingA})

		failing := readyIface("eth1", dvpgTarget(matchPGB))
		network.SetResolutionBackingForTest(&failing.Provider, fakeNetworkRef{err: errors.New("boom")})

		unresolved := readyIface("eth2", dvpgTarget(matchPGC))

		pending := phaseIface("eth3", network.ResolutionPending)
		network.SetResolutionBackingForTest(&pending.Provider, fakeNetworkRef{backing: backingA})

		info := matchInfo([]network.InterfaceState{ready, failing, unresolved, pending})

		backings, err := network.EthernetCardBackings(context.Background(), &vmopv1.VirtualMachine{}, info)
		Expect(err).To(MatchError(ContainSubstring("boom")))
		Expect(err).To(MatchError(ContainSubstring(`"eth1"`)))
		Expect(backings).To(HaveLen(1))
		Expect(backings["eth0"]).To(BeIdenticalTo(backingA))

		backings, err = network.EthernetCardBackings(context.Background(), nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(backings).To(BeNil())
	})
})

// planParityIface describes a Ready interface for the parity test.
type planParityIface struct {
	name  string
	pg    string
	extID string
	mac   string
}

// planParityCase runs the same fixture through ReconcileNetworkInterfaces,
// today's powered-off device reconciliation, and BuildPlan. Each side gets
// its own copies of the devices and provider resources because
// ReconcileNetworkInterfaces mutates its inputs.
type planParityCase struct {
	mutableNetworks bool
	disabled        bool
	ifaces          []planParityIface
	orphans         func() []ctrlclient.Object
	devices         func() []vimtypes.BaseVirtualDevice
	expectedOps     []string
}

func (tc planParityCase) run() {
	ctx := pkgcfg.UpdateContext(matchContext(false), func(config *pkgcfg.Config) {
		config.Features.MutableNetworks = tc.mutableNetworks
	})

	orphans := func() []ctrlclient.Object {
		if tc.orphans == nil {
			return nil
		}
		return tc.orphans()
	}
	devices := func() []vimtypes.BaseVirtualDevice {
		if tc.devices == nil {
			return nil
		}
		return tc.devices()
	}

	var (
		results  = &network.NetworkInterfaceResults{}
		states   []network.InterfaceState
		backings = map[string]vimtypes.BaseVirtualDeviceBackingInfo{}
	)

	for _, f := range tc.ifaces {
		backing := charDVPGBacking(matchSwitch, f.pg)
		backings[f.name] = backing

		dev := network.Device{
			InterfaceName: f.name,
			Backing:       fakeNetworkRef{backing: backing},
			NetworkID:     f.pg,
			MacAddress:    f.mac,
			ExternalID:    f.extID,
		}
		card, err := network.CreateVirtualEthernetCard(ctx, dev,
			vmopv1.VirtualMachineNetworkInterfaceSpec{Name: f.name})
		Expect(err).ToNot(HaveOccurred())
		dev.EthCard = card
		results.Devices = append(results.Devices, dev)

		target := &network.Attachment{
			Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: f.pg},
			ExternalID: network.FieldIntent{Op: network.IntentKeep},
			MAC:        network.MACPolicy{Pinned: f.mac},
		}
		if f.extID != "" {
			target.ExternalID = network.FieldIntent{Op: network.IntentSet, Value: f.extID}
		}
		states = append(states, readyIface(f.name, target))
	}

	// Today's code lists the orphaned provider resources only with
	// MutableNetworks while the VM is powered off.
	if tc.mutableNetworks {
		results.OrphanedNetworkInterfaces = orphans()
	}

	oracle, err := network.ReconcileNetworkInterfaces(ctx, results, devices())
	Expect(err).ToNot(HaveOccurred())

	info := matchInfo(states, devices()...)
	info.Enabled = !tc.disabled
	network.SetInfoOwnedObjectsForTest(info, orphans())
	before := encodeDevices(adapterDevices(info)...)

	p := matchAndPlan(ctx, info, network.PlanInput{
		PowerState:      vimtypes.VirtualMachinePowerStatePoweredOff,
		ApplyChanges:    true,
		MutableNetworks: tc.mutableNetworks,
		CardBackings:    backings,
	})
	planned := p.DeviceChanges().Specs()

	By("leaving the observed devices unchanged")
	Expect(encodeDevices(adapterDevices(info)...)).To(Equal(before))

	By("producing the same operations in the same order")
	ops := func(specs []vimtypes.BaseVirtualDeviceConfigSpec) []string {
		var out []string
		for _, s := range specs {
			spec := s.GetVirtualDeviceConfigSpec()
			key := spec.Device.GetVirtualDevice().Key
			if spec.Operation == vimtypes.VirtualDeviceConfigSpecOperationAdd {
				key = 0
			}
			out = append(out, fmt.Sprintf("%s:%d", spec.Operation, key))
		}
		return out
	}
	Expect(ops(planned)).To(Equal(ops(oracle)))
	Expect(ops(planned)).To(Equal(tc.expectedOps))

	By("producing the same devices, except for the temporary keys of additions")
	// Today's additions carry random negative keys; the planner assigns
	// distinct ones.
	for _, specs := range [][]vimtypes.BaseVirtualDeviceConfigSpec{planned, oracle} {
		for _, s := range specs {
			spec := s.GetVirtualDeviceConfigSpec()
			if spec.Operation == vimtypes.VirtualDeviceConfigSpecOperationAdd {
				Expect(spec.Device.GetVirtualDevice().Key).To(BeNumerically("<", 0))
				spec.Device.GetVirtualDevice().Key = 0
			}
		}
	}
	Expect(encodeSpecs(planned)).To(Equal(encodeSpecs(oracle)))

	xmlSpecs := func(specs []vimtypes.BaseVirtualDeviceConfigSpec) string {
		data, err := xml.Marshal(vimtypes.VirtualMachineConfigSpec{DeviceChange: specs})
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
	Expect(xmlSpecs(planned)).To(Equal(xmlSpecs(oracle)))
}

var _ = Describe("BuildPlan parity with ReconcileNetworkInterfaces", Label(testlabels.API), func() {

	const (
		pgOld   = "dvportgroup-old"
		pgExtra = "dvportgroup-extra"
		macOld  = "00:50:56:00:00:99"
	)

	orphanEth0 := func() []ctrlclient.Object {
		return []ctrlclient.Object{orphanNetOP("vm-net-a-eth0", "eth0", matchPGA)}
	}

	DescribeTable("device changes",
		func(tc planParityCase) { tc.run() },

		Entry("an interface is added to a VM without adapters", planParityCase{
			ifaces:      []planParityIface{{name: "eth0", pg: matchPGA}},
			expectedOps: []string{"add:0"},
		}),

		Entry("an interface with a pinned MAC address and external ID is added", planParityCase{
			ifaces:      []planParityIface{{name: "eth0", pg: matchPGA, extID: matchExtA, mac: matchMACA}},
			expectedOps: []string{"add:0"},
		}),

		Entry("the adapter is unchanged", planParityCase{
			ifaces: []planParityIface{{name: "eth0", pg: matchPGA}},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{dvpgCard(4000, 7, matchPGA)}
			},
		}),

		Entry("an NSX adapter is unchanged", planParityCase{
			ifaces: []planParityIface{{name: "eth0", pg: matchPGA, extID: matchExtA, mac: matchMACA}},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{nsxCard(4000, matchExtA, matchMACA)}
			},
		}),

		Entry("an extra adapter is removed", planParityCase{
			ifaces: []planParityIface{{name: "eth0", pg: matchPGA}},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{
					dvpgCard(4000, 7, matchPGB),
					dvpgCard(4001, 8, matchPGA),
				}
			},
			expectedOps: []string{"remove:4000"},
		}),

		Entry("the network changes without orphan evidence", planParityCase{
			mutableNetworks: true,
			ifaces:          []planParityIface{{name: "eth0", pg: matchPGB}},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{dvpgCard(4000, 7, matchPGA)}
			},
			expectedOps: []string{"remove:4000", "add:0"},
		}),

		Entry("an NSX attachment changes without orphan evidence", planParityCase{
			mutableNetworks: true,
			ifaces:          []planParityIface{{name: "eth0", pg: matchPGA, extID: matchExtB, mac: matchMACB}},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{nsxCard(4000, matchExtA, matchMACA)}
			},
			expectedOps: []string{"remove:4000", "add:0"},
		}),

		Entry("a same-name orphan's adapter is edited with MutableNetworks", planParityCase{
			mutableNetworks: true,
			ifaces:          []planParityIface{{name: "eth0", pg: matchPGB, extID: matchExtB, mac: matchMACB}},
			orphans:         orphanEth0,
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{e1000eCard(4000, matchPGA, "", macOld, "subnet-old")}
			},
			expectedOps: []string{"edit:4000"},
		}),

		Entry("a same-name orphan's adapter is edited to a generated MAC address", planParityCase{
			mutableNetworks: true,
			ifaces:          []planParityIface{{name: "eth0", pg: matchPGB}},
			orphans:         orphanEth0,
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{e1000eCard(4000, matchPGA, "", macOld, "subnet-old")}
			},
			expectedOps: []string{"edit:4000"},
		}),

		Entry("an unlabeled same-name orphan's adapter is edited with MutableNetworks", planParityCase{
			mutableNetworks: true,
			ifaces:          []planParityIface{{name: "eth0", pg: matchPGB}},
			orphans: func() []ctrlclient.Object {
				return []ctrlclient.Object{orphanNetOP("foo-eth0", "", matchPGA)}
			},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{e1000eCard(4000, matchPGA, "", "", "")}
			},
			expectedOps: []string{"edit:4000"},
		}),

		Entry("a same-name orphan's adapter is replaced without MutableNetworks", planParityCase{
			ifaces:  []planParityIface{{name: "eth0", pg: matchPGB}},
			orphans: orphanEth0,
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{e1000eCard(4000, matchPGA, "", macOld, "subnet-old")}
			},
			expectedOps: []string{"remove:4000", "add:0"},
		}),

		Entry("unchanged, edited, added, and removed adapters together", planParityCase{
			mutableNetworks: true,
			ifaces: []planParityIface{
				{name: "eth0", pg: matchPGA},
				{name: "eth1", pg: matchPGB},
				{name: "eth2", pg: matchPGC},
			},
			orphans: func() []ctrlclient.Object {
				return []ctrlclient.Object{orphanNetOP("vm-net-old-eth1", "eth1", pgOld)}
			},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{
					dvpgCard(4000, 7, matchPGA),
					e1000eCard(4001, pgOld, "", "", ""),
					dvpgCard(4002, 9, pgExtra),
				}
			},
			expectedOps: []string{"remove:4002", "edit:4001", "add:0"},
		}),

		Entry("interfaces are added around a matched adapter", planParityCase{
			ifaces: []planParityIface{
				{name: "eth0", pg: matchPGA},
				{name: "eth1", pg: matchPGB},
				{name: "eth2", pg: matchPGC},
			},
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{
					dvpgCard(4000, 7, matchPGB),
					dvpgCard(4001, 8, pgOld),
				}
			},
			expectedOps: []string{"remove:4001", "add:0", "add:0"},
		}),

		Entry("the network is disabled", planParityCase{
			disabled: true,
			devices: func() []vimtypes.BaseVirtualDevice {
				return []vimtypes.BaseVirtualDevice{
					dvpgCard(4000, 7, matchPGA),
					dvpgCard(4001, 8, matchPGB),
				}
			},
			expectedOps: []string{"remove:4000", "remove:4001"},
		}),
	)
})
