// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
)

// AdapterDisposition is what may happen to an observed adapter.
type AdapterDisposition string

const (
	// AdapterDispositionClaimed means a desired interface claims the adapter.
	AdapterDispositionClaimed AdapterDisposition = "Claimed"

	// AdapterDispositionProtected means the adapter must not be removed: it
	// is a candidate of an ambiguous match, the observation is incomplete, or
	// an unclaimed desired interface may still own it.
	AdapterDispositionProtected AdapterDisposition = "Protected"

	// AdapterDispositionRetiring means evidence ties the adapter to an
	// interface that is no longer in the spec.
	AdapterDispositionRetiring AdapterDisposition = "Retiring"

	// AdapterDispositionUnclaimed means the observation is complete and
	// nothing could own the adapter.
	AdapterDispositionUnclaimed AdapterDisposition = "Unclaimed"
)

// adapterRecordInterface returns the name of the interface that an adapter's
// operation record assigns it to for the VM with the given UID. Operation
// records are not decoded yet, so no adapter carries one. It is a variable so
// the record pass can be tested before the record codec exists.
var adapterRecordInterface = func(_ ObservedAdapter, _ types.UID) (string, bool) {
	return "", false
}

// MatchInterfaces claims observed adapters for the desired interfaces of the
// snapshot, stores each claim and its attachment-change classification on
// the interface's InterfaceState, and records the disposition of every
// adapter in info.Disposition.
//
// Adapters are claimed one-to-one, and a later pass never takes an adapter an
// earlier pass claimed:
//
//  1. Unit number. When VMNetworkUnitNumbers is enabled, a numbered interface
//     claims the adapter at exactly its unit number or nothing. A numbered
//     interface never uses any other evidence and never takes part in the
//     positional fallback.
//  2. Operation record. An adapter whose record names the interface. Several
//     such adapters make the interface Ambiguous and protect them all.
//  3. Identity. An unnumbered interface claims the only adapter that its
//     provider evidence identifies. For a Ready provider, that is the
//     adapter whose attachment satisfies the target. For a provider resource
//     that is not Ready, it is the adapter that the attachment its last
//     status still reports matches under the legacy provider-specific rules,
//     using only the fields that status carries. Unique claims are reserved
//     first, in spec order.
//  4. Remaining unnumbered interfaces are then visited in spec order, as the
//     legacy device reconciliation does: an interface with several identity
//     candidates claims the first unclaimed one in device order, and an
//     interface with none claims the first unclaimed adapter that a VM-owned,
//     unreferenced provider resource for the same interface name matches
//     (an orphan).
//  5. Positional. The remaining unnumbered interfaces, in spec order and
//     including the Pending, Failed, and Unknown ones, are zipped with the
//     remaining adapters in observed device order.
//
// The positional zip runs regardless of MutableNetworks and of the network
// hot plug capability. Because it resolves every legacy ambiguity, Ambiguous
// results only from conflicting operation records.
func MatchInterfaces(ctx context.Context, info *Info) {
	if info == nil {
		return
	}
	m := newMatcher(ctx, info)
	m.claimUnitNumbers()
	m.claimRecords()
	m.claimUniqueIdentities()
	m.claimInSpecOrder()
	m.claimPositionally()
	m.classify()
	m.dispose()
}

type matcher struct {
	info *Info

	unitNumbers bool

	// claimedBy maps a claimed adapter key to the claiming interface index.
	claimedBy map[int32]int

	// reserved holds the adapter keys that no pass may claim: candidates of
	// an ambiguous match and adapters whose record names another interface.
	reserved map[int32]struct{}

	// done marks the interfaces whose match is final.
	done []bool

	// numbered marks the interfaces that are matched only by unit number.
	numbered []bool

	// orphans are the VM-owned provider resources that no desired interface
	// references.
	orphans []ctrlclient.Object

	// retiringByRecord holds the adapters whose record names an interface
	// that is not in the spec.
	retiringByRecord map[int32]struct{}
}

func newMatcher(ctx context.Context, info *Info) *matcher {
	m := &matcher{
		info:             info,
		unitNumbers:      pkgcfg.FromContext(ctx).Features.VMNetworkUnitNumbers,
		claimedBy:        map[int32]int{},
		reserved:         map[int32]struct{}{},
		done:             make([]bool, len(info.Interfaces)),
		numbered:         make([]bool, len(info.Interfaces)),
		retiringByRecord: map[int32]struct{}{},
	}

	for i := range info.Interfaces {
		info.Interfaces[i].Match = Match{Kind: MatchNone}
		info.Interfaces[i].AttachmentChange = AttachmentChangeUnknown
	}

	m.orphans = orphanedObjects(info)

	return m
}

// orphanedObjects returns the VM-owned provider resources that no desired
// interface references. A resource is keyed by its provider type and name, so
// resources with the same name from different network providers are distinct.
// Every desired interface takes part, including the Pending, Failed, and
// Unknown ones.
func orphanedObjects(info *Info) []ctrlclient.Object {
	if len(info.owned) == 0 {
		return nil
	}

	referenced := map[string]struct{}{}
	for _, iface := range info.Interfaces {
		if ref := iface.Provider.Ref; ref.Name != "" {
			referenced[qualifiedInterfaceKey(ref.Type, ref.Name)] = struct{}{}
		}
	}

	var orphans []ctrlclient.Object
	for _, obj := range info.owned {
		key := qualifiedInterfaceKey(providerTypeOf(obj), obj.GetName())
		if _, ok := referenced[key]; !ok {
			orphans = append(orphans, obj)
		}
	}
	return orphans
}

func (m *matcher) claim(i int, a ObservedAdapter, kind MatchKind, evidence string) {
	m.info.Interfaces[i].Match = Match{
		Kind:       kind,
		AdapterKey: a.Key,
		Evidence:   evidence,
	}
	m.claimedBy[a.Key] = i
	m.done[i] = true
}

func (m *matcher) available(a ObservedAdapter) bool {
	if _, ok := m.claimedBy[a.Key]; ok {
		return false
	}
	_, ok := m.reserved[a.Key]
	return !ok
}

// availableAdapters returns the adapters no pass has claimed or reserved, in
// observed device order.
func (m *matcher) availableAdapters() []ObservedAdapter {
	var out []ObservedAdapter
	for _, a := range m.info.Adapters {
		if m.available(a) {
			out = append(out, a)
		}
	}
	return out
}

func (m *matcher) claimUnitNumbers() {
	if !m.unitNumbers {
		return
	}

	for i := range m.info.Interfaces {
		unitNumber := m.info.Interfaces[i].Spec.UnitNumber
		if unitNumber == nil {
			continue
		}

		m.numbered[i] = true
		m.done[i] = true

		// A miss leaves the interface MatchNone.
		for _, a := range m.info.Adapters {
			if a.UnitNumber != nil && *a.UnitNumber == *unitNumber && m.available(a) {
				m.claim(i, a, MatchUnitNumber,
					fmt.Sprintf("adapter at unit number %d", *unitNumber))
				break
			}
		}
	}
}

func (m *matcher) claimRecords() {
	names := map[string]int{}
	for i, iface := range m.info.Interfaces {
		if _, ok := names[iface.Spec.Name]; !ok {
			names[iface.Spec.Name] = i
		}
	}

	byInterface := map[int][]ObservedAdapter{}
	for _, a := range m.info.Adapters {
		if !m.available(a) {
			continue
		}
		name, ok := adapterRecordInterface(a, m.info.Identity.VMUID)
		if !ok {
			continue
		}
		i, ok := names[name]
		if !ok {
			// The record ties the adapter to an interface that is no longer
			// in the spec.
			m.reserved[a.Key] = struct{}{}
			m.retiringByRecord[a.Key] = struct{}{}
			continue
		}
		if m.done[i] {
			// A numbered interface does not use record evidence, and its
			// exact claim already decided this adapter's owner if any. The
			// record names an interface that cannot claim this adapter, so
			// keep it out of the legacy passes.
			m.reserved[a.Key] = struct{}{}
			continue
		}
		byInterface[i] = append(byInterface[i], a)
	}

	for i := range m.info.Interfaces {
		candidates := byInterface[i]
		switch len(candidates) {
		case 0:
		case 1:
			m.claim(i, candidates[0], MatchRecord, "operation record")
		default:
			keys := make([]int32, 0, len(candidates))
			for _, a := range candidates {
				keys = append(keys, a.Key)
				m.reserved[a.Key] = struct{}{}
			}
			m.info.Interfaces[i].Match = Match{
				Kind:       MatchAmbiguous,
				Candidates: keys,
				Evidence: fmt.Sprintf(
					"operation records on %d adapters name this interface", len(keys)),
			}
			m.done[i] = true
		}
	}
}

// identityCandidates returns the available adapters that the interface's
// provider evidence identifies. A Ready target is compared with
// compareAttachment. A provider resource that is not Ready but whose status
// still reports its attachment is compared with the legacy provider-specific
// rules, as the legacy device reconciliation did. An interface with neither
// has no candidates and is left to the later passes.
func (m *matcher) identityCandidates(i int) []ObservedAdapter {
	iface := m.info.Interfaces[i]
	r := iface.Provider

	if r.Phase == ResolutionReady {
		if r.Target == nil {
			return nil
		}
		var out []ObservedAdapter
		for _, a := range m.info.Adapters {
			if !m.available(a) {
				continue
			}
			if matches, _ := compareAttachment(*r.Target, a); matches {
				out = append(out, a)
			}
		}
		return out
	}

	// The last reported status of a resource that is not Ready is the same
	// applied-allocation evidence attachmentChange uses.
	if r.obj == nil || !reportsAttachment(r.obj) {
		return nil
	}

	var out []ObservedAdapter
	for _, a := range m.info.Adapters {
		if !m.available(a) || a.device == nil {
			continue
		}
		devices := object.VirtualDeviceList{a.device}
		if providerObjectMatch(r.obj, devices, iface.Spec.MACAddr) == 0 {
			out = append(out, a)
		}
	}
	return out
}

func (m *matcher) claimUniqueIdentities() {
	for i := range m.info.Interfaces {
		if m.done[i] {
			continue
		}
		if candidates := m.identityCandidates(i); len(candidates) == 1 {
			m.claim(i, candidates[0], MatchIdentity, "attachment identity")
		}
	}
}

func (m *matcher) claimInSpecOrder() {
	for i := range m.info.Interfaces {
		if m.done[i] {
			continue
		}

		switch candidates := m.identityCandidates(i); len(candidates) {
		case 0:
		case 1:
			m.claim(i, candidates[0], MatchIdentity, "attachment identity")
			continue
		default:
			m.claim(i, candidates[0], MatchIdentity, fmt.Sprintf(
				"first of %d adapters with the same attachment identity",
				len(candidates)))
			continue
		}

		if a, obj, ok := m.orphanCandidate(i); ok {
			m.claim(i, a, MatchOrphan, fmt.Sprintf(
				"attachment of previous provider resource %s for the same interface",
				obj.GetName()))
		}
	}
}

// orphanCandidate returns the first available adapter that an orphaned
// provider resource for the same interface name matches. Like the legacy
// device reconciliation, resources labeled with the interface name are tried
// before unlabeled resources whose name ends with the interface name.
func (m *matcher) orphanCandidate(i int) (ObservedAdapter, ctrlclient.Object, bool) {
	if len(m.orphans) == 0 {
		return ObservedAdapter{}, nil, false
	}

	// The provider-specific comparison needs the device.
	var (
		adapters []ObservedAdapter
		devices  object.VirtualDeviceList
	)
	for _, a := range m.availableAdapters() {
		if a.device != nil {
			adapters = append(adapters, a)
			devices = append(devices, a.device)
		}
	}
	if len(devices) == 0 {
		return ObservedAdapter{}, nil, false
	}

	name := m.info.Interfaces[i].Spec.Name
	suffix := "-" + name

	var unlabeled []ctrlclient.Object
	for _, obj := range m.orphans {
		v, ok := obj.GetLabels()[VMInterfaceNameLabel]
		if !ok {
			if strings.HasSuffix(obj.GetName(), suffix) {
				unlabeled = append(unlabeled, obj)
			}
			continue
		}
		if v != name {
			continue
		}
		// The orphan's MAC address is not known.
		if idx := providerObjectMatch(obj, devices, ""); idx >= 0 {
			return adapters[idx], obj, true
		}
	}

	for _, obj := range unlabeled {
		if idx := providerObjectMatch(obj, devices, ""); idx >= 0 {
			return adapters[idx], obj, true
		}
	}

	return ObservedAdapter{}, nil, false
}

// providerObjectMatch returns the index of the first device that the
// provider resource's last reported status matches, using the legacy
// provider-specific rules. It returns -1 when no device matches.
func providerObjectMatch(
	obj ctrlclient.Object,
	devices object.VirtualDeviceList,
	macAddress string) int {

	switch o := obj.(type) {
	case *netopv1alpha1.NetworkInterface:
		return findMatchingEthCardNetOpNetIf(o, devices, macAddress)
	case *ncpv1alpha1.VirtualNetworkInterface:
		return findMatchingEthCardNCPNetIf(o, devices)
	case *vpcv1alpha1.SubnetPort:
		return findMatchingEthCardVPCSubnetPort(o, devices)
	}
	return -1
}

// reportsAttachment returns true when the provider resource's status names
// the field that identifies its attachment.
func reportsAttachment(obj ctrlclient.Object) bool {
	switch o := obj.(type) {
	case *netopv1alpha1.NetworkInterface:
		return o.Status.NetworkID != ""
	case *ncpv1alpha1.VirtualNetworkInterface:
		return o.Status.InterfaceID != ""
	case *vpcv1alpha1.SubnetPort:
		return o.Status.Attachment.ID != ""
	}
	return false
}

func (m *matcher) claimPositionally() {
	adapters := m.availableAdapters()

	var next int
	for i := range m.info.Interfaces {
		if m.done[i] || m.numbered[i] {
			continue
		}
		if next >= len(adapters) {
			// No adapter remains, so the interface stays MatchNone.
			break
		}
		m.claim(i, adapters[next], MatchPositional, fmt.Sprintf(
			"position %d of the remaining interfaces and adapters", next))
		next++
	}
}

func (m *matcher) adapter(key int32) (ObservedAdapter, bool) {
	for _, a := range m.info.Adapters {
		if a.Key == key {
			return a, true
		}
	}
	return ObservedAdapter{}, false
}

// classify sets each interface's AttachmentChange from the evidence
// available. Provider readiness alone never establishes a Replacement.
func (m *matcher) classify() {
	for i := range m.info.Interfaces {
		iface := &m.info.Interfaces[i]
		iface.AttachmentChange = m.attachmentChange(*iface)
	}
}

func (m *matcher) attachmentChange(iface InterfaceState) AttachmentChange {
	switch iface.Match.Kind {
	case MatchUnitNumber, MatchRecord, MatchIdentity, MatchOrphan, MatchPositional:
	default:
		return AttachmentChangeUnknown
	}

	a, ok := m.adapter(iface.Match.AdapterKey)
	if !ok {
		return AttachmentChangeUnknown
	}

	if r := iface.Provider; r.Phase == ResolutionReady && r.Target != nil {
		matches, ok := compareAttachment(*r.Target, a)
		switch {
		case !ok:
			return AttachmentChangeUnknown
		case matches:
			return AttachmentChangeUnchanged
		default:
			return AttachmentChangeReplacement
		}
	}

	if m.sameNameOrphanMatches(iface.Spec.Name, a) {
		// The adapter carries the attachment of a previous provider resource
		// for this interface, and the desired resource is a different one.
		return AttachmentChangeReplacement
	}

	// The desired resource is not ready. If it is the resource whose last
	// reported attachment the adapter already carries, the attachment is
	// unchanged; otherwise the applied allocation is not known.
	if obj := iface.Provider.obj; obj != nil && a.device != nil && reportsAttachment(obj) {
		devices := object.VirtualDeviceList{a.device}
		if providerObjectMatch(obj, devices, iface.Spec.MACAddr) == 0 {
			return AttachmentChangeUnchanged
		}
	}

	return AttachmentChangeUnknown
}

// sameNameOrphanMatches returns true when an orphaned provider resource for
// the interface name matches the adapter.
func (m *matcher) sameNameOrphanMatches(name string, a ObservedAdapter) bool {
	if a.device == nil {
		return false
	}
	devices := object.VirtualDeviceList{a.device}
	for _, obj := range m.orphans {
		v, ok := obj.GetLabels()[VMInterfaceNameLabel]
		switch {
		case ok && v != name:
			continue
		case !ok && !strings.HasSuffix(obj.GetName(), "-"+name):
			continue
		}
		if providerObjectMatch(obj, devices, "") == 0 {
			return true
		}
	}
	return false
}

func (m *matcher) dispose() {
	info := m.info
	info.Disposition = make(map[int32]AdapterDisposition, len(info.Adapters))

	protected := map[int32]struct{}{}
	for _, iface := range info.Interfaces {
		if iface.Match.Kind == MatchAmbiguous {
			for _, key := range iface.Match.Candidates {
				protected[key] = struct{}{}
			}
		}
	}

	protectAll := !info.Complete
	for i, iface := range info.Interfaces {
		if m.numbered[i] || iface.Match.Kind != MatchNone {
			continue
		}
		switch iface.Provider.Phase {
		case ResolutionPending, ResolutionUnknown:
			// An unclaimed, unnumbered interface whose provider outcome is
			// not known yet may still own one of the unclaimed adapters.
			protectAll = true
		case ResolutionReady, ResolutionFailed:
		}
	}

	retiringOrphans := m.retiringOrphans()

	for _, a := range info.Adapters {
		_, isClaimed := m.claimedBy[a.Key]
		_, isReserved := m.reserved[a.Key]
		_, isRetiringByRecord := m.retiringByRecord[a.Key]

		// A reserved adapter that is neither an ambiguous candidate nor
		// retiring has a record naming an interface that cannot claim it.
		isProtected := isReserved && !isRetiringByRecord
		if _, ok := protected[a.Key]; ok {
			isProtected = true
		}

		var d AdapterDisposition
		switch {
		case isClaimed:
			d = AdapterDispositionClaimed
		case isProtected || protectAll:
			d = AdapterDispositionProtected
		case isRetiringByRecord || m.matchesAny(a, retiringOrphans):
			d = AdapterDispositionRetiring
		default:
			d = AdapterDispositionUnclaimed
		}
		info.Disposition[a.Key] = d
	}
}

// retiringOrphans returns the orphaned provider resources that belong to an
// interface name that is no longer in the spec.
func (m *matcher) retiringOrphans() []ctrlclient.Object {
	names := map[string]struct{}{}
	for _, iface := range m.info.Interfaces {
		names[iface.Spec.Name] = struct{}{}
	}

	var out []ctrlclient.Object
	for _, obj := range m.orphans {
		if v, ok := obj.GetLabels()[VMInterfaceNameLabel]; ok {
			if _, inSpec := names[v]; !inSpec {
				out = append(out, obj)
			}
			continue
		}
		inSpec := false
		for name := range names {
			if strings.HasSuffix(obj.GetName(), "-"+name) {
				inSpec = true
				break
			}
		}
		if !inSpec {
			out = append(out, obj)
		}
	}
	return out
}

func (m *matcher) matchesAny(a ObservedAdapter, objs []ctrlclient.Object) bool {
	if a.device == nil {
		return false
	}
	devices := object.VirtualDeviceList{a.device}
	for _, obj := range objs {
		if providerObjectMatch(obj, devices, "") == 0 {
			return true
		}
	}
	return false
}

// compareAttachment reports whether an observed adapter satisfies a desired
// attachment. It is the identity predicate shared by MatchInterfaces and
// FindMatchingEthCard:
//
//   - A pinned desired MAC address must equal the adapter's MAC address,
//     ignoring case. A vSphere-managed desired MAC address is not compared.
//   - A desired external ID that is set must equal the adapter's external
//     ID. A desired external ID that is not set is not compared.
//   - The backings must be of the same kind and identify the same network.
//     An adapter without a backing never matches. A distributed switch UUID
//     and a port key are compared only when the desired attachment sets
//     them. A standard network is compared by its normalized ID, or by the
//     adapter's device name when the desired ID is a network name.
//   - An NSX segment is identified by the external ID together with the
//     pinned MAC address, never by the portgroup or opaque network that
//     carries it.
//
// The second result is false when the desired attachment carries no
// comparable identity, i.e. an NSX segment with neither an external ID nor a
// pinned MAC address. The first result is then false.
func compareAttachment(want Attachment, have ObservedAdapter) (bool, bool) {
	if want.Backing.Kind == BackingKindNSXSegment &&
		want.ExternalID.Op != IntentSet && want.MAC.Pinned == "" {
		return false, false
	}

	if want.MAC.Pinned != "" && !strings.EqualFold(want.MAC.Pinned, have.MAC) {
		return false, true
	}

	if want.ExternalID.Op == IntentSet &&
		(have.Attachment.ExternalID.Op != IntentSet ||
			want.ExternalID.Value != have.Attachment.ExternalID.Value) {
		return false, true
	}

	return backingMatches(want.Backing, have), true
}

func backingMatches(want BackingID, have ObservedAdapter) bool {
	got := have.Attachment.Backing

	if want.Kind == BackingKindNSXSegment {
		return true
	}

	if want.Kind == "" || got.Kind == "" || want.Kind != got.Kind {
		return false
	}

	switch want.Kind {
	case BackingKindNetwork:
		if want.ID == "" {
			return false
		}
		return want.ID == got.ID || want.ID == networkDeviceName(have.device)
	case BackingKindDVPortgroup:
		if want.ID != got.ID {
			return false
		}
		if want.SwitchUUID != "" && want.SwitchUUID != got.SwitchUUID {
			return false
		}
		if want.PortKey != "" && want.PortKey != got.PortKey {
			return false
		}
		return true
	case BackingKindOpaque:
		return want.ID == got.ID
	case BackingKindNSXSegment:
		// Handled above.
	}

	return false
}

// networkDeviceName returns the device name of a standard network backing.
func networkDeviceName(dev vimtypes.BaseVirtualDevice) string {
	if dev == nil {
		return ""
	}
	if b, ok := dev.GetVirtualDevice().Backing.(*vimtypes.VirtualEthernetCardNetworkBackingInfo); ok {
		return b.DeviceName
	}
	return ""
}
