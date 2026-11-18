// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

const (
	matchPGA    = "dvportgroup-a"
	matchPGB    = "dvportgroup-b"
	matchPGC    = "dvportgroup-c"
	matchSwitch = "switch-uuid-1"
	matchMACA   = "00:50:56:00:00:0a"
	matchMACB   = "00:50:56:00:00:0b"
	matchExtA   = "ext-id-a"
	matchExtB   = "ext-id-b"
	matchLSUUID = "6d6c7d4e-2a42-4f5e-9d0b-1c3b8a5e7f21"
)

// matchCard returns a VMXNET3 card with the given key and unit number. A
// negative unit number leaves the unit number unset.
func matchCard(key, unitNumber int32, c charCard) *vimtypes.VirtualVmxnet3 {
	c.key = key
	card := c.vmxnet3()
	if unitNumber >= 0 {
		card.UnitNumber = ptr.To(unitNumber)
	}
	return card
}

func dvpgCard(key, unitNumber int32, pg string) *vimtypes.VirtualVmxnet3 {
	return matchCard(key, unitNumber, charCard{
		addressType: vimtypes.VirtualEthernetCardMacTypeGenerated,
		mac:         fmt.Sprintf("00:50:56:00:01:%02x", key%256),
		backing:     charDVPGBacking(matchSwitch, pg),
	})
}

func dvpgTarget(pg string) *network.Attachment {
	return &network.Attachment{
		Backing:    network.BackingID{Kind: network.BackingKindDVPortgroup, ID: pg},
		ExternalID: network.FieldIntent{Op: network.IntentKeep},
	}
}

func readyIface(name string, target *network.Attachment) network.InterfaceState {
	return network.InterfaceState{
		Spec: vmopv1.VirtualMachineNetworkInterfaceSpec{Name: name},
		Provider: network.Resolution{
			Phase: network.ResolutionReady,
			Ref: network.ProviderRef{
				Type:      pkgcfg.NetworkProviderTypeVDS,
				Namespace: providerTestNS,
				Name:      "vm-" + name,
			},
			Target: target,
		},
	}
}

func phaseIface(name string, phase network.ResolutionPhase) network.InterfaceState {
	return network.InterfaceState{
		Spec: vmopv1.VirtualMachineNetworkInterfaceSpec{Name: name},
		Provider: network.Resolution{
			Phase: phase,
			Ref: network.ProviderRef{
				Type:      pkgcfg.NetworkProviderTypeVDS,
				Namespace: providerTestNS,
				Name:      "vm-" + name,
			},
		},
	}
}

func numbered(iface network.InterfaceState, unitNumber int32) network.InterfaceState {
	iface.Spec.UnitNumber = ptr.To(unitNumber)
	return iface
}

func matchInfo(
	ifaces []network.InterfaceState,
	devices ...vimtypes.BaseVirtualDevice) *network.Info {

	info := &network.Info{
		Identity:   network.ObservationIdentity{VMUID: "match-vm-uid"},
		Enabled:    true,
		Complete:   true,
		Interfaces: ifaces,
	}
	for _, dev := range devices {
		info.Adapters = append(info.Adapters, network.NewObservedAdapterForTest(dev))
	}
	return info
}

func matchContext(unitNumbers bool) context.Context {
	return pkgcfg.UpdateContext(
		pkgcfg.NewContextWithDefaultConfig(),
		func(config *pkgcfg.Config) {
			config.Features.VMNetworkUnitNumbers = unitNumbers
		})
}

// claims returns the claimed adapter key of each interface by name. An
// interface without a claim is absent.
func claims(info *network.Info) map[string]int32 {
	out := map[string]int32{}
	for _, iface := range info.Interfaces {
		switch iface.Match.Kind {
		case network.MatchUnitNumber, network.MatchRecord, network.MatchIdentity,
			network.MatchOrphan, network.MatchPositional:
			out[iface.Spec.Name] = iface.Match.AdapterKey
		case network.MatchNone, network.MatchAmbiguous:
		}
	}
	return out
}

func kinds(info *network.Info) map[string]network.MatchKind {
	out := map[string]network.MatchKind{}
	for _, iface := range info.Interfaces {
		out[iface.Spec.Name] = iface.Match.Kind
	}
	return out
}

func changes(info *network.Info) map[string]network.AttachmentChange {
	out := map[string]network.AttachmentChange{}
	for _, iface := range info.Interfaces {
		out[iface.Spec.Name] = iface.AttachmentChange
	}
	return out
}

// specIdxByKey converts the claims to the device key to spec index map that
// MapEthernetDevicesToSpecIdx returns.
func specIdxByKey(info *network.Info) map[int32]int {
	out := map[int32]int{}
	for i, iface := range info.Interfaces {
		switch iface.Match.Kind {
		case network.MatchUnitNumber, network.MatchRecord, network.MatchIdentity,
			network.MatchOrphan, network.MatchPositional:
			out[iface.Match.AdapterKey] = i
		case network.MatchNone, network.MatchAmbiguous:
		}
	}
	return out
}

func orphanNetOP(name, ifaceLabel, pg string) *netopv1alpha1.NetworkInterface {
	obj := &netopv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: providerTestNS},
		Status: netopv1alpha1.NetworkInterfaceStatus{
			NetworkID: pg,
		},
	}
	if ifaceLabel != "" {
		obj.Labels = map[string]string{network.VMInterfaceNameLabel: ifaceLabel}
	}
	return obj
}

var _ = Describe("MatchInterfaces", Label(testlabels.API), func() {

	var (
		ctx  context.Context
		info *network.Info
	)

	BeforeEach(func() {
		ctx = matchContext(false)
	})

	Context("unit numbers", func() {
		BeforeEach(func() {
			ctx = matchContext(true)
		})

		It("claims the adapter at exactly each interface's unit number", func() {
			info = matchInfo(
				[]network.InterfaceState{
					numbered(readyIface("eth0", dvpgTarget(matchPGA)), 8),
					numbered(readyIface("eth1", dvpgTarget(matchPGA)), 7),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
			Expect(kinds(info)).To(HaveEach(network.MatchUnitNumber))
			Expect(changes(info)).To(HaveEach(network.AttachmentChangeUnchanged))
			Expect(info.Disposition).To(HaveEach(network.AdapterDispositionClaimed))
		})

		It("keeps each interface's adapter when the numbered spec entries are reordered", func() {
			a := numbered(readyIface("eth0", dvpgTarget(matchPGA)), 8)
			b := numbered(readyIface("eth1", dvpgTarget(matchPGB)), 7)
			devices := []vimtypes.BaseVirtualDevice{dvpgCard(4000, 7, matchPGB), dvpgCard(4001, 8, matchPGA)}

			info = matchInfo([]network.InterfaceState{a, b}, devices...)
			network.MatchInterfaces(ctx, info)
			before := claims(info)

			info = matchInfo([]network.InterfaceState{b, a}, devices...)
			network.MatchInterfaces(ctx, info)
			Expect(claims(info)).To(Equal(before))
			Expect(before).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
		})

		It("does not claim a free adapter with a matching backing when the unit number misses", func() {
			info = matchInfo(
				[]network.InterfaceState{
					numbered(readyIface("eth0", dvpgTarget(matchPGA)), 9),
				},
				dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match).To(Equal(network.Match{Kind: network.MatchNone}))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
			Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionUnclaimed,
			}))
		})

		It("does not protect adapters for a pending numbered interface that misses", func() {
			info = matchInfo(
				[]network.InterfaceState{
					numbered(phaseIface("eth0", network.ResolutionPending), 9),
				},
				dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchNone))
			Expect(info.Disposition[4000]).To(Equal(network.AdapterDispositionUnclaimed))
		})

		It("claims numbered interfaces first and matches the unnumbered ones from the rest", func() {
			info = matchInfo(
				[]network.InterfaceState{
					phaseIface("eth0", network.ResolutionPending),
					numbered(readyIface("eth1", dvpgTarget(matchPGB)), 9),
					readyIface("eth2", dvpgTarget(matchPGC)),
					numbered(readyIface("eth3", dvpgTarget(matchPGA)), 7),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGB),
				dvpgCard(4002, 10, matchPGC))
			network.MatchInterfaces(ctx, info)

			// eth1's unit number misses, so it claims nothing even though 4001
			// is on its portgroup. eth0 takes the remaining adapter by
			// position.
			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth2": 4002, "eth3": 4000}))
			Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
				"eth0": network.MatchPositional,
				"eth1": network.MatchNone,
				"eth2": network.MatchIdentity,
				"eth3": network.MatchUnitNumber,
			}))
		})

		It("classifies a numbered adapter whose attachment differs from the ready target as a replacement", func() {
			info = matchInfo(
				[]network.InterfaceState{
					numbered(readyIface("eth0", dvpgTarget(matchPGB)), 7),
				},
				dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchUnitNumber))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
		})
	})

	When("VMNetworkUnitNumbers is disabled", func() {
		It("ignores unit numbers and zips by position", func() {
			info = matchInfo(
				[]network.InterfaceState{
					numbered(phaseIface("eth0", network.ResolutionPending), 8),
					numbered(phaseIface("eth1", network.ResolutionPending), 7),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4000, "eth1": 4001}))
			Expect(kinds(info)).To(HaveEach(network.MatchPositional))
		})
	})

	Context("identity", func() {
		It("reserves unique claims before resolving several candidates in device order", func() {
			eth1 := readyIface("eth1", dvpgTarget(matchPGA))
			eth1.Provider.Target.MAC.Pinned = "00:50:56:00:01:a0"

			info = matchInfo(
				[]network.InterfaceState{
					readyIface("eth0", dvpgTarget(matchPGA)),
					eth1,
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			// eth1's pinned MAC is that of 4000.
			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
			Expect(kinds(info)).To(HaveEach(network.MatchIdentity))
			Expect(changes(info)).To(HaveEach(network.AttachmentChangeUnchanged))
		})

		It("assigns duplicate backings in spec and device order", func() {
			info = matchInfo(
				[]network.InterfaceState{
					readyIface("eth0", dvpgTarget(matchPGA)),
					readyIface("eth1", dvpgTarget(matchPGA)),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4000, "eth1": 4001}))
			Expect(kinds(info)).To(HaveEach(network.MatchIdentity))
		})

		It("matches by identity regardless of device order", func() {
			info = matchInfo(
				[]network.InterfaceState{
					readyIface("eth0", dvpgTarget(matchPGA)),
					readyIface("eth1", dvpgTarget(matchPGB)),
				},
				dvpgCard(4000, 7, matchPGB),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
		})

		It("ignores the observed switch UUID when the target does not know it", func() {
			info = matchInfo(
				[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))},
				dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchIdentity))
		})

		It("compares the switch UUID when the target sets it", func() {
			target := dvpgTarget(matchPGA)
			target.Backing.SwitchUUID = "other-switch"
			info = matchInfo(
				[]network.InterfaceState{readyIface("eth0", target)},
				dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchPositional))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
		})

		Context("NSX segment", func() {
			nsxTarget := func(extID, mac string) *network.Attachment {
				a := &network.Attachment{
					Backing:    network.BackingID{Kind: network.BackingKindNSXSegment, ID: matchLSUUID},
					ExternalID: network.FieldIntent{Op: network.IntentKeep},
					MAC:        network.MACPolicy{Pinned: mac},
				}
				if extID != "" {
					a.ExternalID = network.FieldIntent{Op: network.IntentSet, Value: extID}
				}
				return a
			}

			nsxCardOn := func(key int32, pg, extID, mac string) *vimtypes.VirtualVmxnet3 {
				return matchCard(key, -1, charCard{
					addressType: vimtypes.VirtualEthernetCardMacTypeManual,
					mac:         mac,
					externalID:  extID,
					backing:     charDVPGBacking(matchSwitch, pg),
				})
			}

			It("matches by external ID and MAC and never by portgroup", func() {
				info = matchInfo(
					[]network.InterfaceState{
						readyIface("eth0", nsxTarget(matchExtA, matchMACA)),
						readyIface("eth1", nsxTarget(matchExtB, matchMACB)),
					},
					nsxCardOn(4000, matchPGA, matchExtB, matchMACB),
					nsxCardOn(4001, matchPGB, matchExtA, matchMACA))
				network.MatchInterfaces(ctx, info)

				Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
				Expect(kinds(info)).To(HaveEach(network.MatchIdentity))
				Expect(changes(info)).To(HaveEach(network.AttachmentChangeUnchanged))
			})

			It("classifies a changed external ID with the same MAC as a replacement", func() {
				info = matchInfo(
					[]network.InterfaceState{readyIface("eth0", nsxTarget(matchExtB, matchMACA))},
					nsxCardOn(4000, matchPGA, matchExtA, matchMACA))
				network.MatchInterfaces(ctx, info)

				Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchPositional))
				Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
			})

			It("does not treat a target with no identity as evidence", func() {
				info = matchInfo(
					[]network.InterfaceState{readyIface("eth0", nsxTarget("", ""))},
					nsxCardOn(4000, matchPGA, matchExtA, matchMACA))
				network.MatchInterfaces(ctx, info)

				Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchPositional))
				Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
			})
		})
	})

	Context("positional fallback", func() {
		phases := []network.ResolutionPhase{
			network.ResolutionReady,
			network.ResolutionPending,
			network.ResolutionFailed,
			network.ResolutionUnknown,
		}

		It("keeps every interface in the ordered input regardless of its phase", func() {
			for _, p0 := range phases {
				for _, p1 := range phases {
					By(fmt.Sprintf("eth0 %s, eth1 %s", p0, p1))

					// The Ready targets match no adapter, so each interface
					// lacks identity evidence.
					iface := func(name string, p network.ResolutionPhase) network.InterfaceState {
						if p == network.ResolutionReady {
							return readyIface(name, dvpgTarget(matchPGC))
						}
						return phaseIface(name, p)
					}

					info = matchInfo(
						[]network.InterfaceState{iface("eth0", p0), iface("eth1", p1)},
						dvpgCard(4000, 7, matchPGA),
						dvpgCard(4001, 8, matchPGA))
					network.MatchInterfaces(ctx, info)

					Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4000, "eth1": 4001}))
					Expect(kinds(info)).To(HaveEach(network.MatchPositional))
				}
			}
		})

		It("zips the pending entries around the identity claims", func() {
			info = matchInfo(
				[]network.InterfaceState{
					phaseIface("eth0", network.ResolutionPending),
					readyIface("eth1", dvpgTarget(matchPGB)),
					phaseIface("eth2", network.ResolutionFailed),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGB),
				dvpgCard(4002, 9, matchPGC))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4000, "eth1": 4001, "eth2": 4002}))
			Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
				"eth0": network.MatchPositional,
				"eth1": network.MatchIdentity,
				"eth2": network.MatchPositional,
			}))
			Expect(changes(info)).To(Equal(map[string]network.AttachmentChange{
				"eth0": network.AttachmentChangeUnknown,
				"eth1": network.AttachmentChangeUnchanged,
				"eth2": network.AttachmentChangeUnknown,
			}))
		})

		It("leaves the surplus interfaces unclaimed", func() {
			info = matchInfo(
				[]network.InterfaceState{
					readyIface("eth0", dvpgTarget(matchPGB)),
					readyIface("eth1", dvpgTarget(matchPGB)),
					readyIface("eth2", dvpgTarget(matchPGB)),
				},
				dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4000}))
			Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
				"eth0": network.MatchPositional,
				"eth1": network.MatchNone,
				"eth2": network.MatchNone,
			}))
			Expect(changes(info)).To(Equal(map[string]network.AttachmentChange{
				"eth0": network.AttachmentChangeReplacement,
				"eth1": network.AttachmentChangeUnknown,
				"eth2": network.AttachmentChangeUnknown,
			}))
		})

		It("leaves the surplus adapters unclaimed", func() {
			info = matchInfo(
				[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGC))},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGB),
				dvpgCard(4002, 9, matchPGC))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4002}))
			Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionUnclaimed,
				4001: network.AdapterDispositionUnclaimed,
				4002: network.AdapterDispositionClaimed,
			}))
		})
	})

	Context("orphaned provider resources", func() {
		var newCRRef network.ProviderRef

		BeforeEach(func() {
			newCRRef = network.ProviderRef{
				Type:      pkgcfg.NetworkProviderTypeVDS,
				Namespace: providerTestNS,
				Name:      "vm-net-b-eth0",
			}
		})

		withOwned := func(info *network.Info, objs ...ctrlclient.Object) *network.Info {
			network.SetInfoOwnedObjectsForTest(info, objs)
			return info
		}

		It("claims the adapter of the same-name orphan as a pending replacement", func() {
			eth0 := phaseIface("eth0", network.ResolutionPending)
			eth0.Provider.Ref = newCRRef

			info = withOwned(
				matchInfo(
					[]network.InterfaceState{eth0},
					dvpgCard(4000, 7, matchPGB),
					dvpgCard(4001, 8, matchPGA)),
				orphanNetOP("vm-net-a-eth0", "eth0", matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchOrphan))
			Expect(info.Interfaces[0].Match.AdapterKey).To(Equal(int32(4001)))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
			Expect(info.Disposition[4000]).To(Equal(network.AdapterDispositionUnclaimed))
		})

		It("claims the adapter of the same-name orphan as a ready replacement", func() {
			eth0 := readyIface("eth0", dvpgTarget(matchPGB))
			eth0.Provider.Ref = newCRRef

			info = withOwned(
				matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA)),
				orphanNetOP("vm-net-a-eth0", "eth0", matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchOrphan))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
		})

		It("matches an unlabeled orphan by its name suffix", func() {
			eth0 := phaseIface("eth0", network.ResolutionPending)
			eth0.Provider.Ref = newCRRef

			info = withOwned(
				matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA)),
				orphanNetOP("vm-net-a-eth0", "", matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchOrphan))
		})

		It("uses a numbered claim's same-name orphan as replacement evidence", func() {
			ctx = matchContext(true)
			eth0 := numbered(phaseIface("eth0", network.ResolutionPending), 7)
			eth0.Provider.Ref = newCRRef

			info = withOwned(
				matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA)),
				orphanNetOP("vm-net-a-eth0", "eth0", matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchUnitNumber))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
		})

		It("does not use an orphan labeled with a different interface name", func() {
			eth0 := phaseIface("eth0", network.ResolutionPending)
			eth0.Provider.Ref = newCRRef

			info = withOwned(
				matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA)),
				orphanNetOP("vm-net-a-eth0", "eth1", matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchPositional))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
		})

		It("does not treat the desired resource as an orphan", func() {
			eth0 := phaseIface("eth0", network.ResolutionPending)
			eth0.Provider.Ref = network.ProviderRef{
				Type:      pkgcfg.NetworkProviderTypeVDS,
				Namespace: providerTestNS,
				Name:      "vm-net-a-eth0",
			}

			info = withOwned(
				matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA)),
				orphanNetOP("vm-net-a-eth0", "eth0", matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchPositional))
		})

		It("retires an unclaimed adapter whose orphan belongs to a removed interface", func() {
			info = withOwned(
				matchInfo(
					[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))},
					dvpgCard(4000, 7, matchPGA),
					dvpgCard(4001, 8, matchPGB),
					dvpgCard(4002, 9, matchPGC)),
				orphanNetOP("vm-net-b-eth1", "eth1", matchPGB))
			network.MatchInterfaces(ctx, info)

			Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionClaimed,
				4001: network.AdapterDispositionRetiring,
				4002: network.AdapterDispositionUnclaimed,
			}))
		})

		Context("an unreferenced external network interface", func() {
			external := func(name, ifaceLabel, pg string) ctrlclient.Object {
				obj := orphanNetOP(name, ifaceLabel, pg)
				obj.Annotations = map[string]string{pkgconst.ExternalNetworkInterfaceAnnotation: "true"}
				return obj
			}

			withExternal := func(info *network.Info, objs ...ctrlclient.Object) *network.Info {
				network.SetInfoExternalObjectsForTest(info, objs)
				return info
			}

			It("retires an unclaimed adapter whose external CR belongs to a removed interface", func() {
				info = withExternal(
					matchInfo(
						[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))},
						dvpgCard(4000, 7, matchPGA),
						dvpgCard(4001, 8, matchPGB)),
					external("byo-b", "eth1", matchPGB))
				network.MatchInterfaces(ctx, info)

				Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
					4000: network.AdapterDispositionClaimed,
					4001: network.AdapterDispositionRetiring,
				}))
			})

			It("claims the adapter of the same-name external CR as a pending replacement", func() {
				eth0 := phaseIface("eth0", network.ResolutionPending)
				eth0.Provider.Ref = newCRRef

				info = withExternal(
					matchInfo(
						[]network.InterfaceState{eth0},
						dvpgCard(4000, 7, matchPGB),
						dvpgCard(4001, 8, matchPGA)),
					external("byo-a", "eth0", matchPGA))
				network.MatchInterfaces(ctx, info)

				Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchOrphan))
				Expect(info.Interfaces[0].Match.AdapterKey).To(Equal(int32(4001)))
				Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
			})
		})
	})

	// Spec 017 T018: as in the legacy device reconciliation, the last reported
	// attachment of a provider resource that is not Ready is identity
	// evidence.
	Context("identity for a provider resource that is not ready", func() {
		pendingWith := func(name string, obj ctrlclient.Object) network.InterfaceState {
			iface := phaseIface(name, network.ResolutionPending)
			if obj != nil {
				network.SetResolutionObjectForTest(&iface.Provider, obj)
			}
			return iface
		}

		netOPWithExtID := func(name, extID string) *netopv1alpha1.NetworkInterface {
			obj := orphanNetOP(name, "", matchPGA)
			obj.Status.ExternalID = extID
			return obj
		}

		ncpWith := func(name, interfaceID, mac string) *ncpv1alpha1.VirtualNetworkInterface {
			return &ncpv1alpha1.VirtualNetworkInterface{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: providerTestNS},
				Status: ncpv1alpha1.VirtualNetworkInterfaceStatus{
					InterfaceID: interfaceID,
					MacAddress:  mac,
				},
			}
		}

		extIDCard := func(key, unitNumber int32, extID string) *vimtypes.VirtualVmxnet3 {
			return matchCard(key, unitNumber, charCard{
				externalID: extID,
				backing:    charDVPGBacking(matchSwitch, matchPGA),
			})
		}

		DescribeTable("claims the card its last reported external ID names regardless of spec order",
			func(reversed bool) {
				ifaces := []network.InterfaceState{
					pendingWith("eth0", netOPWithExtID("vm-eth0", matchExtA)),
					pendingWith("eth1", netOPWithExtID("vm-eth1", matchExtB)),
				}
				if reversed {
					ifaces[0], ifaces[1] = ifaces[1], ifaces[0]
				}
				info = matchInfo(ifaces,
					extIDCard(4000, 7, matchExtB),
					extIDCard(4001, 8, matchExtA))
				network.MatchInterfaces(ctx, info)

				Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
				Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
					"eth0": network.MatchIdentity,
					"eth1": network.MatchIdentity,
				}))
			},
			Entry("in spec order", false),
			Entry("in reverse spec order", true),
		)

		DescribeTable("claims the card its last reported interface ID and MAC address name regardless of spec order",
			func(reversed bool) {
				ifaces := []network.InterfaceState{
					pendingWith("eth0", ncpWith("vm-eth0", matchExtA, matchMACA)),
					pendingWith("eth1", ncpWith("vm-eth1", matchExtB, matchMACB)),
				}
				if reversed {
					ifaces[0], ifaces[1] = ifaces[1], ifaces[0]
				}
				info = matchInfo(ifaces,
					nsxCard(4000, matchExtB, matchMACB),
					nsxCard(4001, matchExtA, matchMACA))
				network.MatchInterfaces(ctx, info)

				Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
				Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
					"eth0": network.MatchIdentity,
					"eth1": network.MatchIdentity,
				}))
			},
			Entry("in spec order", false),
			Entry("in reverse spec order", true),
		)

		It("zips interfaces whose resources report no attachment by position", func() {
			info = matchInfo(
				[]network.InterfaceState{
					pendingWith("eth0", orphanNetOP("vm-eth0", "", "")),
					pendingWith("eth1", nil),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4000, "eth1": 4001}))
			Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
				"eth0": network.MatchPositional,
				"eth1": network.MatchPositional,
			}))
		})

		It("leaves an interface without evidence the card that evidence does not claim", func() {
			info = matchInfo(
				[]network.InterfaceState{
					pendingWith("eth0", nil),
					pendingWith("eth1", netOPWithExtID("vm-eth1", matchExtA)),
				},
				extIDCard(4000, 7, matchExtA),
				extIDCard(4001, 8, matchExtB))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
			Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
				"eth0": network.MatchPositional,
				"eth1": network.MatchIdentity,
			}))
		})

		It("does not claim a card the last reported attachment contradicts", func() {
			info = matchInfo(
				[]network.InterfaceState{
					pendingWith("eth0", netOPWithExtID("vm-eth0", matchExtB)),
					readyIface("eth1", dvpgTarget(matchPGA)),
				},
				extIDCard(4000, 7, matchExtA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			// eth0's resource names an external ID no card carries, so eth0 is
			// not an identity claim on any card.
			Expect(kinds(info)["eth0"]).ToNot(Equal(network.MatchIdentity))
		})
	})

	Context("attachment change for a provider that is not ready", func() {
		var eth0 network.InterfaceState

		BeforeEach(func() {
			eth0 = phaseIface("eth0", network.ResolutionPending)
		})

		It("is Unchanged when the adapter carries the pending resource's last reported attachment", func() {
			network.SetResolutionObjectForTest(&eth0.Provider,
				orphanNetOP("vm-eth0", "eth0", matchPGA))

			info = matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			// The pending resource's last reported attachment is identity
			// evidence too.
			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchIdentity))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnchanged))
		})

		It("is Unknown when the pending resource reports no attachment", func() {
			network.SetResolutionObjectForTest(&eth0.Provider,
				orphanNetOP("vm-eth0", "eth0", ""))

			info = matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
		})

		It("is Unknown when the pending resource reports a different attachment", func() {
			network.SetResolutionObjectForTest(&eth0.Provider,
				orphanNetOP("vm-eth0", "eth0", matchPGB))

			info = matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
		})

		It("is Unknown when the resource does not exist yet", func() {
			info = matchInfo([]network.InterfaceState{eth0}, dvpgCard(4000, 7, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
		})
	})

	Context("operation records", func() {
		var restore func()

		AfterEach(func() {
			restore()
		})

		recordsFor := func(byKey map[int32]string) func(network.ObservedAdapter, types.UID) (string, bool) {
			return func(a network.ObservedAdapter, vmUID types.UID) (string, bool) {
				Expect(vmUID).To(Equal(types.UID("match-vm-uid")))
				name, ok := byKey[a.Key]
				return name, ok
			}
		}

		It("claims the adapter whose record names the interface", func() {
			restore = network.SetAdapterRecordInterfaceForTest(recordsFor(map[int32]string{4001: "eth0"}))

			info = matchInfo(
				[]network.InterfaceState{
					readyIface("eth0", dvpgTarget(matchPGA)),
					phaseIface("eth1", network.ResolutionPending),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGB))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
			Expect(kinds(info)).To(Equal(map[string]network.MatchKind{
				"eth0": network.MatchRecord,
				"eth1": network.MatchPositional,
			}))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeReplacement))
		})

		It("protects every candidate of conflicting records and keeps them from other interfaces", func() {
			restore = network.SetAdapterRecordInterfaceForTest(recordsFor(map[int32]string{
				4000: "eth0",
				4001: "eth0",
			}))

			info = matchInfo(
				[]network.InterfaceState{
					readyIface("eth0", dvpgTarget(matchPGA)),
					readyIface("eth1", dvpgTarget(matchPGA)),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA),
				dvpgCard(4002, 9, matchPGB))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchAmbiguous))
			Expect(info.Interfaces[0].Match.Candidates).To(ConsistOf(int32(4000), int32(4001)))
			Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
			Expect(claims(info)).To(Equal(map[string]int32{"eth1": 4002}))
			Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionProtected,
				4001: network.AdapterDispositionProtected,
				4002: network.AdapterDispositionClaimed,
			}))
		})

		It("retires an adapter whose record names a removed interface", func() {
			restore = network.SetAdapterRecordInterfaceForTest(recordsFor(map[int32]string{4000: "eth9"}))

			info = matchInfo(
				[]network.InterfaceState{phaseIface("eth0", network.ResolutionPending)},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001}))
			Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionRetiring,
				4001: network.AdapterDispositionClaimed,
			}))
		})
	})

	Context("dispositions", func() {
		It("protects every unclaimed adapter when the observation is incomplete", func() {
			info = matchInfo(
				[]network.InterfaceState{readyIface("eth0", dvpgTarget(matchPGA))},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGB))
			info.Complete = false
			network.MatchInterfaces(ctx, info)

			Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionClaimed,
				4001: network.AdapterDispositionProtected,
			}))
		})

		It("protects every unclaimed adapter while an unclaimed unnumbered interface is pending", func() {
			restore := network.SetAdapterRecordInterfaceForTest(
				func(a network.ObservedAdapter, _ types.UID) (string, bool) {
					if a.Key == 4000 || a.Key == 4001 {
						return "eth0", true
					}
					return "", false
				})
			defer restore()

			info = matchInfo(
				[]network.InterfaceState{
					readyIface("eth0", dvpgTarget(matchPGA)),
					phaseIface("eth1", network.ResolutionUnknown),
				},
				dvpgCard(4000, 7, matchPGA),
				dvpgCard(4001, 8, matchPGA))
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[1].Match.Kind).To(Equal(network.MatchNone))
			Expect(info.Disposition).To(HaveEach(network.AdapterDispositionProtected))
		})

		It("leaves every adapter unclaimed when the network is disabled, as today's removal does", func() {
			info = matchInfo(nil, dvpgCard(4000, 7, matchPGA))
			info.Enabled = false
			network.MatchInterfaces(ctx, info)

			Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
				4000: network.AdapterDispositionUnclaimed,
			}))
		})

		It("has an empty disposition when there are no adapters", func() {
			info = matchInfo([]network.InterfaceState{phaseIface("eth0", network.ResolutionPending)})
			network.MatchInterfaces(ctx, info)

			Expect(info.Interfaces[0].Match).To(Equal(network.Match{Kind: network.MatchNone}))
			Expect(info.Disposition).To(BeEmpty())
		})
	})
})

var _ = Describe("Observe and Prepare matching", Label(testlabels.API), func() {

	It("matches a named network by device name although the adapter reports the network MoRef", func() {
		ctx := providerTestContext(pkgcfg.NetworkProviderTypeNamed)
		vm := providerTestVM()
		vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth0", Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeNamed, "VM Network")},
			{Name: "eth1", Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeNamed, "Other Network")},
		}

		named := func(key int32, name, moRef string) *vimtypes.VirtualVmxnet3 {
			return matchCard(key, -1, charCard{
				addressType: vimtypes.VirtualEthernetCardMacTypeGenerated,
				backing: &vimtypes.VirtualEthernetCardNetworkBackingInfo{
					VirtualDeviceDeviceBackingInfo: vimtypes.VirtualDeviceDeviceBackingInfo{DeviceName: name},
					Network:                        &vimtypes.ManagedObjectReference{Type: "Network", Value: moRef},
				},
			})
		}

		info := network.Observe(ctx, network.ObserveInput{
			Client: builder.NewFakeClient(),
			VM:     vm,
			MoVM: providerTestMoVM(
				named(4000, "Other Network", "network-2"),
				named(4001, "VM Network", "network-1")),
		})

		Expect(claims(info)).To(Equal(map[string]int32{"eth0": 4001, "eth1": 4000}))
		Expect(kinds(info)).To(HaveEach(network.MatchIdentity))
		Expect(changes(info)).To(HaveEach(network.AttachmentChangeUnchanged))
	})

	It("matches in Prepare", func() {
		ctx := providerTestContext(pkgcfg.NetworkProviderTypeVDS)
		vm := providerTestVM()
		vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
			{Name: "eth0", Network: providerTestNetworkRef(pkgcfg.NetworkProviderTypeVDS, "net-a")},
		}

		info, err := network.Prepare(ctx, network.PrepareInput{
			Client: builder.NewFakeClient(),
			VM:     vm,
			MoVM:   providerTestMoVM(dvpgCard(4000, 7, matchPGA)),
		})
		Expect(err).ToNot(HaveOccurred())

		// The created resource is not ready, so the adapter is claimed by
		// position and its attachment change is not known.
		Expect(info.Interfaces[0].Provider.Phase).To(Equal(network.ResolutionPending))
		Expect(info.Interfaces[0].Match.Kind).To(Equal(network.MatchPositional))
		Expect(info.Interfaces[0].AttachmentChange).To(Equal(network.AttachmentChangeUnknown))
		Expect(info.Disposition).To(Equal(map[int32]network.AdapterDisposition{
			4000: network.AdapterDispositionClaimed,
		}))
	})
})

// parityCase runs the same fixtures through MapEthernetDevicesToSpecIdx and
// Observe. Since spec 017 T018, MapEthernetDevicesToSpecIdx derives from the
// shared match, so the two must agree.
type parityCase struct {
	mutableNetworks bool
	unitNumbers     bool
	interfaces      []vmopv1.VirtualMachineNetworkInterfaceSpec
	objects         []ctrlclient.Object
	devices         []vimtypes.BaseVirtualDevice
}

func (tc parityCase) run() (legacy, matched map[int32]int, info *network.Info) {
	ctx := pkgcfg.UpdateContext(
		providerTestContext(pkgcfg.NetworkProviderTypeVDS),
		func(config *pkgcfg.Config) {
			config.Features.MutableNetworks = tc.mutableNetworks
			config.Features.VMNetworkUnitNumbers = tc.unitNumbers
		})

	vm := providerTestVM()
	vm.Spec.Network.Interfaces = tc.interfaces

	moVM := providerTestMoVM(tc.devices...)
	client := builder.NewFakeClient(tc.objects...)

	legacy = network.MapEthernetDevicesToSpecIdx(
		pkgctx.VirtualMachineContext{
			Context: ctx,
			Logger:  suite.GetLogger().WithName("parity"),
			VM:      vm,
		},
		client,
		moVM)

	info = network.Observe(ctx, network.ObserveInput{
		Client: client,
		VM:     vm,
		MoVM:   moVM,
	})
	Expect(info.Complete).To(BeTrue())

	return legacy, specIdxByKey(info), info
}

func parityInterfaces(n int, providerType pkgcfg.NetworkProviderType, networks ...string) []vmopv1.VirtualMachineNetworkInterfaceSpec {
	var out []vmopv1.VirtualMachineNetworkInterfaceSpec
	for i := range n {
		networkName := "net-a"
		if i < len(networks) {
			networkName = networks[i]
		}
		out = append(out, vmopv1.VirtualMachineNetworkInterfaceSpec{
			Name:    fmt.Sprintf("eth%d", i),
			Network: providerTestNetworkRef(providerType, networkName),
		})
	}
	return out
}

func parityOwned(obj ctrlclient.Object, ifaceName string) ctrlclient.Object {
	obj.SetNamespace(providerTestNS)
	obj.SetLabels(map[string]string{
		pkgconst.NetworkInterfaceVMNameLabel: providerTestVMName,
		network.VMInterfaceNameLabel:         ifaceName,
	})
	obj.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: vmopv1.GroupVersion.String(),
			Kind:       "VirtualMachine",
			Name:       providerTestVMName,
			UID:        "provider-test-vm-uid",
		},
	})
	return obj
}

func parityNetOP(networkName, ifaceName, pg, extID string, ready, legacyName bool) ctrlclient.Object {
	obj := &netopv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name: network.NetOPCRName(providerTestVMName, networkName, ifaceName, legacyName),
		},
		Status: netopv1alpha1.NetworkInterfaceStatus{
			NetworkID:  pg,
			ExternalID: extID,
		},
	}
	if ready {
		obj.Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
			{Type: netopv1alpha1.NetworkInterfaceReady, Status: corev1.ConditionTrue},
		}
	}
	return parityOwned(obj, ifaceName)
}

func parityNCP(networkName, ifaceName, interfaceID, mac string) ctrlclient.Object {
	return parityOwned(&ncpv1alpha1.VirtualNetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name: network.NCPCRName(providerTestVMName, networkName, ifaceName, false),
		},
		Status: ncpv1alpha1.VirtualNetworkInterfaceStatus{
			InterfaceID: interfaceID,
			MacAddress:  mac,
			ProviderStatus: &ncpv1alpha1.VirtualNetworkInterfaceProviderStatus{
				NsxLogicalSwitchID: matchLSUUID,
			},
			Conditions: []ncpv1alpha1.VirtualNetworkCondition{
				{Type: "Ready", Status: "True"},
			},
		},
	}, ifaceName)
}

func parityVPC(networkName, ifaceName, attachmentID, mac string) ctrlclient.Object {
	return parityOwned(&vpcv1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name: network.VPCCRName(providerTestVMName, networkName, ifaceName),
		},
		Status: vpcv1alpha1.SubnetPortStatus{
			Attachment: vpcv1alpha1.PortAttachment{ID: attachmentID},
			NetworkInterfaceConfig: vpcv1alpha1.NetworkInterfaceConfig{
				LogicalSwitchUUID: matchLSUUID,
				MACAddress:        mac,
			},
			Conditions: []vpcv1alpha1.Condition{
				{Type: vpcv1alpha1.Ready, Status: corev1.ConditionTrue},
			},
		},
	}, ifaceName)
}

func nsxCard(key int32, extID, mac string) *vimtypes.VirtualVmxnet3 {
	return matchCard(key, -1, charCard{
		addressType: vimtypes.VirtualEthernetCardMacTypeManual,
		mac:         mac,
		externalID:  extID,
		backing:     charDVPGBacking(matchSwitch, matchPGA),
	})
}

func indistinguishableCards(n int) []vimtypes.BaseVirtualDevice {
	var out []vimtypes.BaseVirtualDevice
	for i := range n {
		out = append(out, dvpgCard(int32(4000+i), int32(7+i), matchPGA))
	}
	return out
}

var _ = Describe("MatchInterfaces parity with MapEthernetDevicesToSpecIdx", Label(testlabels.API), func() {

	DescribeTable("agrees with today's mapping",
		func(tc parityCase) {
			legacy, matched, _ := tc.run()
			if len(legacy) == 0 {
				Expect(matched).To(BeEmpty())
			} else {
				Expect(matched).To(Equal(legacy))
			}
		},

		// The immutable-networks positional zip, as pinned by the T012
		// characterization tests: indistinguishable cards and no provider
		// resources.
		Entry("immutable: equal counts", parityCase{
			interfaces: parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS),
			devices:    indistinguishableCards(2),
		}),
		Entry("immutable: more cards than interfaces", parityCase{
			interfaces: parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS),
			devices:    indistinguishableCards(3),
		}),
		Entry("immutable: more interfaces than cards", parityCase{
			interfaces: parityInterfaces(3, pkgcfg.NetworkProviderTypeVDS),
			devices:    indistinguishableCards(1),
		}),
		Entry("immutable: no cards", parityCase{
			interfaces: parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS),
		}),
		Entry("immutable: non-Ethernet devices interleaved", parityCase{
			interfaces: parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS),
			devices: []vimtypes.BaseVirtualDevice{
				dvpgCard(4000, 7, matchPGA),
				&vimtypes.VirtualDisk{VirtualDevice: vimtypes.VirtualDevice{Key: 2000}},
				dvpgCard(4001, 8, matchPGA),
			},
		}),
		Entry("immutable: unit numbers declared while VMNetworkUnitNumbers is disabled", parityCase{
			interfaces: func() []vmopv1.VirtualMachineNetworkInterfaceSpec {
				ifaces := parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS)
				ifaces[0].UnitNumber = ptr.To(int32(8))
				ifaces[1].UnitNumber = ptr.To(int32(7))
				return ifaces
			}(),
			devices: indistinguishableCards(2),
		}),
		Entry("immutable: unnumbered interfaces while VMNetworkUnitNumbers is enabled", parityCase{
			unitNumbers: true,
			interfaces:  parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS),
			devices:     indistinguishableCards(2),
		}),
		Entry("immutable: ready resources in spec and device order", parityCase{
			interfaces: parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS, "net-a", "net-b"),
			objects: []ctrlclient.Object{
				parityNetOP("net-a", "eth0", matchPGA, "", true, false),
				parityNetOP("net-b", "eth1", matchPGB, "", true, false),
			},
			devices: []vimtypes.BaseVirtualDevice{dvpgCard(4000, 7, matchPGA), dvpgCard(4001, 8, matchPGB)},
		}),

		// Per-interface resource matching with ready resources.
		Entry("mutable: VDS identity in reverse device order", parityCase{
			mutableNetworks: true,
			interfaces:      parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS, "net-a", "net-b"),
			objects: []ctrlclient.Object{
				parityNetOP("net-a", "eth0", matchPGA, "", true, false),
				parityNetOP("net-b", "eth1", matchPGB, "", true, false),
			},
			devices: []vimtypes.BaseVirtualDevice{dvpgCard(4000, 7, matchPGB), dvpgCard(4001, 8, matchPGA)},
		}),
		Entry("mutable: VDS external ID", parityCase{
			mutableNetworks: true,
			interfaces:      parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS, "net-a", "net-a"),
			objects: []ctrlclient.Object{
				parityNetOP("net-a", "eth0", matchPGA, matchExtA, true, false),
				parityNetOP("net-a", "eth1", matchPGA, matchExtB, true, false),
			},
			devices: []vimtypes.BaseVirtualDevice{
				matchCard(4000, 7, charCard{externalID: matchExtB, backing: charDVPGBacking("", matchPGA)}),
				matchCard(4001, 8, charCard{externalID: matchExtA, backing: charDVPGBacking("", matchPGA)}),
			},
		}),
		Entry("mutable: VDS duplicate backings", parityCase{
			mutableNetworks: true,
			interfaces:      parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS, "net-a", "net-a"),
			objects: []ctrlclient.Object{
				parityNetOP("net-a", "eth0", matchPGA, "", true, false),
				parityNetOP("net-a", "eth1", matchPGA, "", true, false),
			},
			devices: indistinguishableCards(2),
		}),
		Entry("mutable: VDS resource that is not ready", parityCase{
			mutableNetworks: true,
			interfaces:      parityInterfaces(1, pkgcfg.NetworkProviderTypeVDS),
			objects:         []ctrlclient.Object{parityNetOP("net-a", "eth0", matchPGA, "", false, false)},
			devices:         indistinguishableCards(1),
		}),
		Entry("mutable: VDS legacy v1alpha1 resource name", parityCase{
			mutableNetworks: true,
			interfaces:      parityInterfaces(1, pkgcfg.NetworkProviderTypeVDS),
			objects: []ctrlclient.Object{
				parityNetOP("net-a", "eth0", matchPGA, "", true, true),
			},
			devices: indistinguishableCards(1),
		}),
		Entry("mutable: NSX-T identity in reverse device order", parityCase{
			mutableNetworks: true,
			interfaces:      parityInterfaces(2, pkgcfg.NetworkProviderTypeNSXT, "net-a", "net-b"),
			objects: []ctrlclient.Object{
				parityNCP("net-a", "eth0", matchExtA, matchMACA),
				parityNCP("net-b", "eth1", matchExtB, matchMACB),
			},
			devices: []vimtypes.BaseVirtualDevice{
				nsxCard(4000, matchExtB, matchMACB),
				nsxCard(4001, matchExtA, matchMACA),
			},
		}),
		Entry("mutable: VPC identity in reverse device order", parityCase{
			mutableNetworks: true,
			interfaces:      parityInterfaces(2, pkgcfg.NetworkProviderTypeVPC, "net-a", "net-b"),
			objects: []ctrlclient.Object{
				parityVPC("net-a", "eth0", matchExtA, matchMACA),
				parityVPC("net-b", "eth1", matchExtB, ""),
			},
			devices: []vimtypes.BaseVirtualDevice{
				nsxCard(4000, matchExtB, matchMACB),
				nsxCard(4001, matchExtA, matchMACA),
			},
		}),
	)

	// These cases diverged from the mapping MapEthernetDevicesToSpecIdx
	// computed before spec 017 T018: the accepted shared positional fallback
	// (Q5) applies with MutableNetworks, the identity predicate is
	// FindMatchingEthCard's, numbered interfaces match exactly, and identity
	// precedes position. Since T018, MapEthernetDevicesToSpecIdx derives from
	// the shared match, so it returns the shared result. The second column is
	// the pre-T018 result, kept for reference only.
	DescribeTable("MapEthernetDevicesToSpecIdx follows the shared match where it differs from the pre-T018 mapping",
		func(tc parityCase, _ /* pre-T018 result */, expectedMatched map[int32]int, expectedKinds []network.MatchKind) {
			legacy, matched, info := tc.run()
			Expect(matched).To(Equal(expectedMatched))
			Expect(legacy).To(Equal(expectedMatched))

			var got []network.MatchKind
			for _, iface := range info.Interfaces {
				got = append(got, iface.Match.Kind)
			}
			Expect(got).To(Equal(expectedKinds))
		},

		Entry("mutable: the resource's portgroup differs, so the adapter is claimed by position",
			parityCase{
				mutableNetworks: true,
				interfaces:      parityInterfaces(1, pkgcfg.NetworkProviderTypeVDS),
				objects:         []ctrlclient.Object{parityNetOP("net-a", "eth0", matchPGB, "", true, false)},
				devices:         indistinguishableCards(1),
			},
			map[int32]int{},
			map[int32]int{4000: 0},
			[]network.MatchKind{network.MatchPositional}),
		Entry("mutable: the card has an external ID but the resource does not",
			parityCase{
				mutableNetworks: true,
				interfaces:      parityInterfaces(1, pkgcfg.NetworkProviderTypeVDS),
				objects:         []ctrlclient.Object{parityNetOP("net-a", "eth0", matchPGA, "", true, false)},
				devices: []vimtypes.BaseVirtualDevice{
					matchCard(4000, 7, charCard{externalID: matchExtA, backing: charDVPGBacking("", matchPGA)}),
				},
			},
			map[int32]int{},
			map[int32]int{4000: 0},
			[]network.MatchKind{network.MatchIdentity}),
		Entry("immutable: numbered interfaces match exactly when VMNetworkUnitNumbers is enabled",
			parityCase{
				unitNumbers: true,
				interfaces: func() []vmopv1.VirtualMachineNetworkInterfaceSpec {
					ifaces := parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS)
					ifaces[0].UnitNumber = ptr.To(int32(8))
					ifaces[1].UnitNumber = ptr.To(int32(9))
					return ifaces
				}(),
				devices: indistinguishableCards(2),
			},
			map[int32]int{4000: 0, 4001: 1},
			map[int32]int{4001: 0},
			[]network.MatchKind{network.MatchUnitNumber, network.MatchNone}),
		Entry("immutable: ready identity takes precedence over device order",
			parityCase{
				interfaces: parityInterfaces(2, pkgcfg.NetworkProviderTypeVDS, "net-a", "net-b"),
				objects: []ctrlclient.Object{
					parityNetOP("net-a", "eth0", matchPGA, "", true, false),
					parityNetOP("net-b", "eth1", matchPGB, "", true, false),
				},
				devices: []vimtypes.BaseVirtualDevice{dvpgCard(4000, 7, matchPGB), dvpgCard(4001, 8, matchPGA)},
			},
			map[int32]int{4000: 0, 4001: 1},
			map[int32]int{4001: 0, 4000: 1},
			[]network.MatchKind{network.MatchIdentity, network.MatchIdentity}),
	)
})

var _ = Describe("Observe matching with MoVM", Label(testlabels.API), func() {
	It("does not match when the VM config is not available", func() {
		ctx := providerTestContext(pkgcfg.NetworkProviderTypeVDS)
		vm := providerTestVM()
		vm.Spec.Network.Interfaces = parityInterfaces(1, pkgcfg.NetworkProviderTypeVDS)

		info := network.Observe(ctx, network.ObserveInput{
			Client: builder.NewFakeClient(),
			VM:     vm,
			MoVM:   mo.VirtualMachine{},
		})

		Expect(info.Complete).To(BeFalse())
		Expect(info.Interfaces[0].Match).To(Equal(network.Match{Kind: network.MatchNone}))
		Expect(info.Disposition).To(BeEmpty())
	})
})
