// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
)

const sriovKind = "VirtualSriovEthernetCard"

// sriovCreateExplicitUnits are the explicit UnitNumber values E14 requests for
// an SR-IOV card in a folder.CreateVM ConfigSpec (the class-ConfigSpec path
// the product uses for SR-IOV). Each probes a distinct static PCI-unit band
// (device_keys.txt): 8 is inside the 7-16 ethernet band, 18 is the first PCI
// passthrough unit, 40 is inside the 36-45 band govmomi's
// VirtualDeviceList.Name associates with SR-IOV, 3 is the SCSI HBA band, and
// 200 has no PCI-unit meaning.
var sriovCreateExplicitUnits = []int32{8, 18, 40, 3, 200}

// sriovReconfigureExplicitUnits are the explicit UnitNumber values E14
// requests for an SR-IOV card on a ReconfigVM_Task Add: one inside the
// ethernet band and one inside govmomi's SR-IOV band.
var sriovReconfigureExplicitUnits = []int32{13, 40}

// sriovVMXLine matches the VMX keys that say which device namespace a NIC
// lives in (ethernetN vs pciPassthruN) and where it sits on the bus.
var sriovVMXLine = regexp.MustCompile(
	`^(ethernet|pciPassthru)\d+\.(present|virtualDev|pciSlotNumber|networkName|pfId|id|deviceId|` +
		`vendorId|systemId|dvs\.portgroupId|addressType)\s*=`)

// sriovBacking returns the SR-IOV physical-function backing E14 uses, resolved
// once per run. A PF selected by -sriov-pnic or -sriov-physical-function is
// looked up in the pinned host's ConfigTarget so its DeviceId, VendorId, and
// SystemId are filled from the platform rather than by hand; with neither
// flag, the automatic assignment sentinel is used.
func (r *runner) sriovBacking(
	ctx context.Context) (*vimtypes.VirtualPCIPassthroughDeviceBackingInfo, string, *vimtypes.ConfigTarget, error) {

	if r.cfg.sriovPNIC == "" && r.cfg.sriovPhysicalFunction == "" {
		return &vimtypes.VirtualPCIPassthroughDeviceBackingInfo{Id: "Automatic-0000:00:00.0"},
			"No -sriov-pnic or -sriov-physical-function; using automatic PF assignment.", nil, nil
	}

	if r.host == nil {
		return nil, "", nil, errors.New("-sriov-pnic/-sriov-physical-function need -host to resolve the PF")
	}

	var moPool mo.ResourcePool

	err := r.resourcePool.Properties(ctx, r.resourcePool.Reference(), []string{"owner"}, &moPool)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to resolve resource pool owner: %w", err)
	}

	var moCR mo.ComputeResource

	err = object.NewComputeResource(r.client.Client, moPool.Owner).Properties(
		ctx, moPool.Owner, []string{"environmentBrowser"}, &moCR)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to read environmentBrowser: %w", err)
	}

	if moCR.EnvironmentBrowser == nil {
		return nil, "", nil, errors.New("compute resource has no environmentBrowser")
	}

	ct, err := object.NewEnvironmentBrowser(r.client.Client, *moCR.EnvironmentBrowser).
		QueryConfigTarget(ctx, r.host)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to query ConfigTarget: %w", err)
	}

	var seen []string

	for i := range ct.Sriov {
		info := &ct.Sriov[i]
		pci := info.PciDevice
		seen = append(seen, fmt.Sprintf("%s(pnic=%s vf=%t)", pci.Id, info.Pnic, info.VirtualFunction))

		if info.VirtualFunction {
			continue
		}

		if (r.cfg.sriovPNIC != "" && info.Pnic != r.cfg.sriovPNIC) ||
			(r.cfg.sriovPhysicalFunction != "" && pci.Id != r.cfg.sriovPhysicalFunction) {
			continue
		}

		backing := &vimtypes.VirtualPCIPassthroughDeviceBackingInfo{
			Id:       pci.Id,
			DeviceId: pciID(int32(pci.DeviceId)),
			SystemId: info.SystemId,
			VendorId: pci.VendorId,
		}

		note := fmt.Sprintf("Resolved SR-IOV PF from the host ConfigTarget: pnic=%s id=%s "+
			"deviceId=%s vendorId=%s deviceName=%q.",
			info.Pnic, backing.Id, backing.DeviceId, pciID(int32(backing.VendorId)), pci.DeviceName)

		return backing, note, ct, nil
	}

	return nil, "", nil, fmt.Errorf("no SR-IOV PF matching pnic=%q id=%q in the host ConfigTarget; saw %v",
		r.cfg.sriovPNIC, r.cfg.sriovPhysicalFunction, seen)
}

// sriovCtx is the per-run state E14's helpers share.
type sriovCtx struct {
	net     object.NetworkReference
	backing *vimtypes.VirtualPCIPassthroughDeviceBackingInfo
	// target is the pinned host's ConfigTarget, or nil with automatic PF
	// assignment. The ordering VM draws a non-SR-IOV passthrough device
	// from it.
	target  *vimtypes.ConfigTarget
	nextKey int32
}

// key returns a fresh negative device key, so several devices can be added in
// one ConfigSpec without colliding on the placeholder key.
func (sc *sriovCtx) key() int32 {
	sc.nextKey--

	return sc.nextKey
}

// newSriovCard builds a VirtualSriovEthernetCard on the SR-IOV network with
// the resolved physical-function backing.
func (r *runner) newSriovCard(
	ctx context.Context,
	sc *sriovCtx,
	unitNumber *int32) (vimtypes.BaseVirtualDevice, error) {

	return r.newSriovCardOn(ctx, sc, sc.net, unitNumber)
}

// newSriovCardOn is newSriovCard on a specific network, so cards in one
// ConfigSpec can be told apart by backing.
func (r *runner) newSriovCardOn(
	ctx context.Context,
	sc *sriovCtx,
	net object.NetworkReference,
	unitNumber *int32) (vimtypes.BaseVirtualDevice, error) {

	backing, err := net.EthernetCardBackingInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get ethernet card backing for %v: %w", net.Reference(), err)
	}

	pf := *sc.backing

	return &vimtypes.VirtualSriovEthernetCard{
		VirtualEthernetCard: vimtypes.VirtualEthernetCard{
			VirtualDevice: vimtypes.VirtualDevice{
				Key:        sc.key(),
				Backing:    backing,
				UnitNumber: unitNumber,
			},
			AddressType: string(vimtypes.VirtualEthernetCardMacTypeGenerated),
		},
		SriovBacking: &vimtypes.VirtualSriovEthernetCardSriovBackingInfo{
			PhysicalFunctionBacking: &pf,
		},
	}, nil
}

// newVmxnet3 builds an operator-shaped vmxnet3 card with a unique key.
func (r *runner) newVmxnet3(
	ctx context.Context,
	sc *sriovCtx,
	unitNumber *int32) (vimtypes.BaseVirtualDevice, error) {

	dev, err := r.newEthCard(ctx, r.network, unitNumber)
	if err != nil {
		return nil, err
	}

	dev.GetVirtualDevice().Key = sc.key()

	return dev, nil
}

// sriovConfigSpec is baseConfigSpec with the full memory reservation locked,
// which SR-IOV (like PCI passthrough) requires to power on.
func (r *runner) sriovConfigSpec(name string) vimtypes.VirtualMachineConfigSpec {
	spec := r.baseConfigSpec(name)
	spec.MemoryReservationLockedToMax = ptr.To(true)

	// E14 powers VMs on. They are diskless, so keep them from PXE-booting
	// onto the (possibly shared) SR-IOV network by parking them in BIOS
	// setup at power-on. (A CD-ROM-only boot order is rejected when the VM
	// has no CD-ROM.)
	spec.BootOptions = &vimtypes.VirtualMachineBootOptions{EnterBIOSSetup: ptr.To(true)}

	return spec
}

// observeSriovStep records the whole virtual PCI bus (not only ethernet
// cards, so the SR-IOV card can be placed against every other occupant) plus
// the VMX lines that say which device namespace each NIC lives in.
func (r *runner) observeSriovStep(ctx context.Context, vm *object.VirtualMachine, s *step) {
	observed, err := r.observePCIBus(ctx, vm)
	if err != nil {
		s.Notes = append(s.Notes, fmt.Sprintf("Failed to observe hardware: %v", err))
	} else {
		s.Observed = observed
	}

	lines, err := r.vmxDeviceLines(ctx, vm)
	if err != nil {
		s.Notes = append(s.Notes, fmt.Sprintf("Failed to read the VMX file: %v", err))

		return
	}

	s.VMX = lines
}

// vmxDeviceLines downloads the VM's .vmx and returns the ethernetN and
// pciPassthruN lines that identify each NIC's device namespace and bus slot.
func (r *runner) vmxDeviceLines(ctx context.Context, vm *object.VirtualMachine) ([]string, error) {
	var moVM mo.VirtualMachine

	err := vm.Properties(ctx, vm.Reference(), []string{"config.files.vmPathName"}, &moVM)
	if err != nil {
		return nil, fmt.Errorf("failed to read vmPathName: %w", err)
	}

	var p object.DatastorePath
	if moVM.Config == nil || !p.FromString(moVM.Config.Files.VmPathName) {
		return nil, fmt.Errorf("cannot parse vmPathName for %s", vm.Reference().Value)
	}

	ds, err := r.finder.Datastore(ctx, p.Datastore)
	if err != nil {
		return nil, fmt.Errorf("failed to find datastore %q: %w", p.Datastore, err)
	}

	rc, _, err := ds.Download(ctx, p.Path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", p.String(), err)
	}
	defer func() { _ = rc.Close() }()

	var lines []string

	sc := bufio.NewScanner(rc)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if sriovVMXLine.MatchString(line) {
			lines = append(lines, line)
		}
	}

	slices.Sort(lines)

	return lines, sc.Err()
}

// sriovOf returns the SR-IOV cards among infos.
func sriovOf(infos []deviceInfo) []deviceInfo {
	var out []deviceInfo

	for _, d := range infos {
		if d.Kind == sriovKind {
			out = append(out, d)
		}
	}

	return out
}

// addedSince returns the devices in after whose key is absent from before.
func addedSince(before, after []deviceInfo) []deviceInfo {
	var out []deviceInfo

	for _, a := range after {
		if !slices.ContainsFunc(before, func(b deviceInfo) bool { return b.Key == a.Key }) {
			out = append(out, a)
		}
	}

	return out
}

// bandOf names the static PCI-unit band (device_keys.txt) a unit falls in,
// plus govmomi's SR-IOV naming band.
func bandOf(unit *int32) string {
	if unit == nil {
		return "no unit"
	}

	u := *unit

	switch {
	case u >= nicUnitNumberFirst && u <= nicUnitNumberLast:
		return "ethernet band 7-16"
	case u == 17:
		return "VMCI unit 17"
	case u >= 18 && u <= 21:
		return "PCI passthrough band 18-21"
	case u >= 36 && u <= 45:
		return "PCI passthrough band 38-161, inside govmomi's SR-IOV naming band 36-45"
	case u >= 38 && u <= 161:
		return "PCI passthrough band 38-161"
	default:
		return "outside every documented band"
	}
}

// keyRangeOf names the static device-key range (device_keys.txt) a key falls in.
func keyRangeOf(key int32) string {
	switch {
	case key >= 4000 && key <= 4009:
		return "ethernet keys 4000-4009"
	case key >= 13000 && key <= 13127:
		return "PCI passthrough keys 13000-13127"
	default:
		return "outside the ethernet and passthrough key ranges"
	}
}

// describeSriov renders one SR-IOV card for a finding line.
func describeSriov(d deviceInfo) string {
	slot := "nil"
	if d.PCISlotNumber != nil {
		slot = fmt.Sprintf("%d", *d.PCISlotNumber)
	}

	return fmt.Sprintf("key=%d (%s) unit=%s (%s) pciSlot=%s",
		d.Key, keyRangeOf(d.Key), d.unit(), bandOf(d.UnitNumber), slot)
}

// faultTypes lists the fault type names a step captured.
func faultTypes(s step) []string {
	types := make([]string, 0, len(s.Faults))
	for _, f := range s.Faults {
		types = append(types, f.Type)
	}

	return types
}

// judgeExplicit records how the platform treated an explicitly requested
// unit number for the device(s) a step added: honoured, renumbered, or
// rejected.
func (r *result) judgeExplicit(label string, want int32, s step, added []deviceInfo) {
	if s.Err != "" {
		r.findf("%s: explicit unit %d (%s) was REJECTED: %v — `%s`.",
			label, want, bandOf(&want), faultTypes(s), s.Err)

		return
	}

	for _, d := range added {
		if d.UnitNumber != nil && *d.UnitNumber == want {
			r.findf("%s: explicit unit %d (%s) was HONOURED — %s.", label, want, bandOf(&want), describeSriov(d))

			return
		}
	}

	if len(added) == 0 {
		r.findf("%s: explicit unit %d succeeded but no new device was observed.", label, want)

		return
	}

	for _, d := range added {
		r.findf("%s: explicit unit %d (%s) was silently RENUMBERED — %s.",
			label, want, bandOf(&want), describeSriov(d))
	}
}

// runE14 answers Q3: does a VirtualSriovEthernetCard draw from the same 7-16
// unit-number band and 4000-series device keys as other NIC types? The spec
// assumed so because VirtualSriovEthernetCard is a VirtualEthernetCard, but
// two pieces of prior art disagree: the product's
// networkextraconfig.isEthernetDevice excludes SR-IOV as living outside the
// ethernetX VMX namespace, and govmomi's VirtualDeviceList.Name names
// ethernet units 36-45 as SR-IOV. So E14 records, for every SR-IOV card, its
// Key, UnitNumber, PCI slot, and the VMX namespace it lands in, across:
//
//   - CreateVM with SR-IOV UnitNumber nil (alongside a vmxnet3);
//   - ReconfigVM_Task Adds with nil and explicit units;
//   - CreateVM with explicit SR-IOV units in each static PCI-unit band;
//   - cross-type collisions (vmxnet3 at an SR-IOV unit and vice versa);
//   - whether SR-IOV counts against the 10-NIC ethernet limit;
//   - a power cycle (a VF is only assigned at power-on), a powered-on
//     hot-add (informational), and remove/re-add slot reuse.
//
// The experiment needs an SR-IOV-capable pNIC and network, which not every
// testbed has (R5). An unavailable environment is recorded as an explicit skip.
func (r *runner) runE14(ctx context.Context) *result {
	res := &result{
		ID:        e14SRIOV,
		Title:     "SR-IOV ethernet cards: unit-number band, device keys, and VMX namespace",
		Questions: []string{"Q3"},
		Status:    statusRecorded,
	}

	if r.cfg.sriovNetwork == "" {
		return res.skip("no -sriov-network supplied; this experiment needs an SR-IOV-capable " +
			"pNIC/host and a suitable network (R5)")
	}

	net, err := r.finder.Network(ctx, r.cfg.sriovNetwork)
	if err != nil {
		return res.fail(fmt.Errorf("failed to find SR-IOV network %q: %w", r.cfg.sriovNetwork, err))
	}

	backing, note, target, err := r.sriovBacking(ctx)
	if err != nil {
		return res.fail(err)
	}

	res.findf("%s", note)

	sc := &sriovCtx{net: net, backing: backing, target: target}

	r.e14AutoAndLifecycle(ctx, sc, res)
	r.e14CreateExplicit(ctx, sc, res)
	r.e14NICLimit(ctx, sc, res)
	r.e14Ordering(ctx, sc, res)
	r.e14Summarise(res)

	return res
}

// e14AutoAndLifecycle drives one VM through nil and explicit adds, cross-type
// collisions, a power cycle, a powered-on hot-add, and remove/re-add.
func (r *runner) e14AutoAndLifecycle(ctx context.Context, sc *sriovCtx, res *result) {
	vmx, err := r.newVmxnet3(ctx, sc, nil)
	if err != nil {
		res.findf("Lifecycle VM: failed to build vmxnet3: %v", err)

		return
	}

	sriov, err := r.newSriovCard(ctx, sc, nil)
	if err != nil {
		res.findf("Lifecycle VM: failed to build SR-IOV card: %v", err)

		return
	}

	spec := r.sriovConfigSpec(r.vmName(res.ID, "life"))
	spec.DeviceChange = addSpec(vmx, sriov)

	create := step{Name: "Lifecycle VM: CreateVM with a vmxnet3 and an SR-IOV card, both UnitNumber nil",
		Requested: requestedFrom(spec.DeviceChange)}

	vm, err := r.createVM(ctx, spec)
	if err != nil {
		create.Err = err.Error()
		create.Faults = captureFaults(err)
		res.Steps = append(res.Steps, create)
		res.findf("Lifecycle VM: CreateVM with an SR-IOV card FAILED: %v — `%s`.", faultTypes(create), err)

		return
	}

	r.observeSriovStep(ctx, vm, &create)
	res.Steps = append(res.Steps, create)

	for _, d := range sriovOf(create.Observed) {
		res.findf("CreateVM, SR-IOV UnitNumber nil: platform assigned %s.", describeSriov(d))
	}

	prev := create.Observed

	// Reconfigure Adds: nil, then each explicit unit.
	adds := []*int32{nil}
	for _, u := range sriovReconfigureExplicitUnits {
		adds = append(adds, ptr.To(u))
	}

	for _, u := range adds {
		card, err := r.newSriovCard(ctx, sc, u)
		if err != nil {
			res.findf("Lifecycle VM: failed to build SR-IOV card: %v", err)

			return
		}

		label := "ReconfigVM_Task Add, SR-IOV UnitNumber nil"
		if u != nil {
			label = fmt.Sprintf("ReconfigVM_Task Add, SR-IOV explicit unit %d", *u)
		}

		s := r.sriovReconfigure(ctx, vm, "Lifecycle VM: "+label, addSpec(card))
		res.Steps = append(res.Steps, s)
		added := sriovOf(addedSince(prev, s.Observed))

		if u == nil {
			switch {
			case s.Err != "":
				res.findf("%s FAILED: %v — `%s`.", label, faultTypes(s), s.Err)
			case len(added) == 0:
				res.findf("%s succeeded but no new SR-IOV card was observed.", label)
			default:
				res.findf("%s: platform assigned %s.", label, describeSriov(added[0]))
			}
		} else {
			res.judgeExplicit(label, *u, s, added)
		}

		if len(s.Observed) > 0 {
			prev = s.Observed
		}
	}

	r.e14Collisions(ctx, sc, vm, &prev, res)
	r.e14PowerCycle(ctx, sc, vm, &prev, res)
	r.e14RemoveReadd(ctx, sc, vm, res)
}

// sriovReconfigure sends a device change and records requested vs observed,
// observing the whole PCI bus and the VMX namespace.
func (r *runner) sriovReconfigure(
	ctx context.Context,
	vm *object.VirtualMachine,
	name string,
	changes []vimtypes.BaseVirtualDeviceConfigSpec) step {

	s := step{Name: name, Requested: requestedFrom(changes)}

	err := r.reconfigure(ctx, vm, vimtypes.VirtualMachineConfigSpec{DeviceChange: changes})
	if err != nil {
		s.Err = err.Error()
		s.Faults = captureFaults(err)
	}

	r.observeSriovStep(ctx, vm, &s)

	return s
}

// e14Collisions requests a vmxnet3 at an SR-IOV card's unit and an SR-IOV card
// at a vmxnet3's unit. A collision fault means the two share one unit space;
// success at the same unit would mean they do not.
func (r *runner) e14Collisions(
	ctx context.Context,
	sc *sriovCtx,
	vm *object.VirtualMachine,
	prev *[]deviceInfo,
	res *result) {

	var sriovUnit, vmxUnit *int32

	for _, d := range *prev {
		switch {
		case d.Kind == sriovKind && sriovUnit == nil:
			sriovUnit = d.UnitNumber
		case strings.HasPrefix(d.Kind, "VirtualVmxnet3") && vmxUnit == nil:
			vmxUnit = d.UnitNumber
		}
	}

	if sriovUnit != nil {
		card, err := r.newVmxnet3(ctx, sc, ptr.To(*sriovUnit))
		if err == nil {
			label := fmt.Sprintf("Collision: vmxnet3 at the first SR-IOV card's unit %d", *sriovUnit)
			s := r.sriovReconfigure(ctx, vm, "Lifecycle VM: "+label, addSpec(card))
			res.Steps = append(res.Steps, s)
			r.judgeCollision(res, label, *sriovUnit, s, addedSince(*prev, s.Observed))

			if len(s.Observed) > 0 {
				*prev = s.Observed
			}
		}
	}

	if vmxUnit != nil {
		card, err := r.newSriovCard(ctx, sc, ptr.To(*vmxUnit))
		if err == nil {
			label := fmt.Sprintf("Collision: SR-IOV card at the vmxnet3's unit %d", *vmxUnit)
			s := r.sriovReconfigure(ctx, vm, "Lifecycle VM: "+label, addSpec(card))
			res.Steps = append(res.Steps, s)
			r.judgeCollision(res, label, *vmxUnit, s, addedSince(*prev, s.Observed))

			if len(s.Observed) > 0 {
				*prev = s.Observed
			}
		}
	}
}

// judgeCollision records the outcome of requesting an occupied unit.
func (r *runner) judgeCollision(res *result, label string, unit int32, s step, added []deviceInfo) {
	switch {
	case s.Err != "":
		res.findf("%s: REJECTED %v — `%s`.", label, faultTypes(s), s.Err)
	case len(added) == 0:
		res.findf("%s: succeeded but no new device was observed.", label)
	default:
		for _, d := range added {
			outcome := "silently RENUMBERED"
			if d.UnitNumber != nil && *d.UnitNumber == unit {
				outcome = "ACCEPTED at the same unit (the two types do not share a unit space)"
			}

			res.findf("%s: %s — kind=%s %s.", label, outcome, d.Kind, describeSriov(d))
		}
	}
}

// e14PowerCycle powers the VM on (which assigns each SR-IOV card a VF),
// attempts an informational SR-IOV hot-add, and powers off again, comparing
// every NIC's key, unit, and slot across the cycle.
func (r *runner) e14PowerCycle(
	ctx context.Context,
	sc *sriovCtx,
	vm *object.VirtualMachine,
	prev *[]deviceInfo,
	res *result) {

	on := step{Name: "Lifecycle VM: powered on"}

	err := r.powerState(ctx, vm, true)
	if err != nil {
		on.Err = err.Error()
		on.Faults = captureFaults(err)
		res.Steps = append(res.Steps, on)
		res.findf("Power-on FAILED: %v — `%s`. Power-cycle stability not observed.", faultTypes(on), err)

		return
	}

	r.observeSriovStep(ctx, vm, &on)
	res.Steps = append(res.Steps, on)
	r.compareCycle(res, "powered off -> powered on", *prev, on.Observed)

	for _, d := range sriovOf(on.Observed) {
		vf := d.SriovVF
		if vf == "" {
			vf = "(virtualFunctionBacking not populated in config.hardware)"
		}

		res.findf("Powered on: SR-IOV card key=%d unit=%s pciSlot=%s, PF %q, VF %s.",
			d.Key, d.unit(), derefOr(d.PCISlotNumber), d.SriovPF, vf)
	}

	card, err := r.newSriovCard(ctx, sc, nil)
	if err == nil {
		label := "Hot-add an SR-IOV card while powered on (informational)"
		s := r.sriovReconfigure(ctx, vm, "Lifecycle VM: "+label, addSpec(card))
		res.Steps = append(res.Steps, s)

		added := addedSince(on.Observed, s.Observed)

		switch {
		case s.Err != "":
			res.findf("%s: REJECTED %v — `%s`.", label, faultTypes(s), s.Err)
		case len(added) == 0:
			res.findf("%s: succeeded but no new device was observed.", label)
		default:
			res.findf("%s: ACCEPTED — %s.", label, describeSriov(added[0]))
		}

		if len(s.Observed) > 0 {
			on.Observed = s.Observed
		}
	}

	off := step{Name: "Lifecycle VM: powered back off"}

	err = r.powerState(ctx, vm, false)
	if err != nil {
		off.Err = err.Error()
		res.Steps = append(res.Steps, off)
		res.findf("Power-off FAILED: `%s`.", err)

		return
	}

	r.observeSriovStep(ctx, vm, &off)
	res.Steps = append(res.Steps, off)
	r.compareCycle(res, "powered on -> powered off", on.Observed, off.Observed)
	*prev = off.Observed
}

// compareCycle reports whether every ethernet card's key, unit, and slot
// survived a power-state transition.
func (r *runner) compareCycle(res *result, label string, before, after []deviceInfo) {
	var changed []string

	for _, b := range before {
		if !isEthKind(b.Kind) {
			continue
		}

		i := slices.IndexFunc(after, func(a deviceInfo) bool { return a.Key == b.Key })
		if i < 0 {
			changed = append(changed, fmt.Sprintf("key %d disappeared", b.Key))

			continue
		}

		a := after[i]

		// A PCI slot is first assigned at power-on (nil before), so nil ->
		// value is an assignment, not a move.
		slotMoved := b.PCISlotNumber != nil && !int32PtrEqual(a.PCISlotNumber, b.PCISlotNumber)
		if !int32PtrEqual(a.UnitNumber, b.UnitNumber) || slotMoved {
			changed = append(changed, fmt.Sprintf("key %d moved unit %s->%s slot %v->%v",
				b.Key, b.unit(), a.unit(), derefOr(b.PCISlotNumber), derefOr(a.PCISlotNumber)))
		}
	}

	if len(changed) == 0 {
		res.findf("Power cycle (%s): every NIC's key and unit number were unchanged, and no "+
			"already-assigned PCI slot moved (slots are first assigned at power-on).", label)

		return
	}

	res.findf("Power cycle (%s): NIC identity CHANGED: %s.", label, strings.Join(changed, "; "))
}

// e14RemoveReadd removes the first SR-IOV card and adds a fresh one with a nil
// unit, to see whether the freed unit is reused (compare E11 for vmxnet3).
func (r *runner) e14RemoveReadd(
	ctx context.Context,
	sc *sriovCtx,
	vm *object.VirtualMachine,
	res *result) {

	hw, err := r.hardware(ctx, vm)
	if err != nil {
		res.findf("Remove/re-add: failed to read hardware: %v", err)

		return
	}

	cards := hw.SelectByType((*vimtypes.VirtualSriovEthernetCard)(nil))
	if len(cards) == 0 {
		return
	}

	victim := deviceInfoFor(cards[0])

	rm := r.sriovReconfigure(ctx, vm, "Lifecycle VM: remove the first SR-IOV card", removeSpec(cards[0]))
	res.Steps = append(res.Steps, rm)

	if rm.Err != "" {
		res.findf("Remove SR-IOV card FAILED: `%s`.", rm.Err)

		return
	}

	card, err := r.newSriovCard(ctx, sc, nil)
	if err != nil {
		return
	}

	add := r.sriovReconfigure(ctx, vm, "Lifecycle VM: add a new SR-IOV card, UnitNumber nil", addSpec(card))
	res.Steps = append(res.Steps, add)

	added := sriovOf(addedSince(rm.Observed, add.Observed))

	switch {
	case add.Err != "":
		res.findf("Re-add after remove FAILED: `%s`.", add.Err)
	case len(added) == 0:
		res.findf("Re-add after remove succeeded but no new SR-IOV card was observed.")
	default:
		reused := int32PtrEqual(added[0].UnitNumber, victim.UnitNumber)
		res.findf("Remove/re-add: removed SR-IOV card had %s; the new nil-unit card got %s (freed unit reused: %t).",
			describeSriov(victim), describeSriov(added[0]), reused)
	}
}

// e14CreateExplicit creates one VM per explicit SR-IOV unit, each alongside a
// vmxnet3 at unit 7, to see how CreateVM treats an explicit SR-IOV unit in
// each static PCI-unit band.
func (r *runner) e14CreateExplicit(ctx context.Context, sc *sriovCtx, res *result) {
	for _, u := range sriovCreateExplicitUnits {
		vmx, err := r.newVmxnet3(ctx, sc, ptr.To(nicUnitNumberFirst))
		if err != nil {
			res.findf("CreateVM explicit %d: failed to build vmxnet3: %v", u, err)

			continue
		}

		card, err := r.newSriovCard(ctx, sc, ptr.To(u))
		if err != nil {
			res.findf("CreateVM explicit %d: failed to build SR-IOV card: %v", u, err)

			continue
		}

		spec := r.sriovConfigSpec(r.vmName(res.ID, fmt.Sprintf("u%d", u)))
		spec.DeviceChange = addSpec(vmx, card)

		label := fmt.Sprintf("CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit %d", u)
		s := step{Name: label, Requested: requestedFrom(spec.DeviceChange)}

		vm, err := r.createVM(ctx, spec)
		if err != nil {
			s.Err = err.Error()
			s.Faults = captureFaults(err)
			res.Steps = append(res.Steps, s)
			res.judgeExplicit(label, u, s, nil)

			continue
		}

		r.observeSriovStep(ctx, vm, &s)
		res.Steps = append(res.Steps, s)
		res.judgeExplicit(label, u, s, sriovOf(s.Observed))
	}
}

// e14NICLimit creates a VM with ten vmxnet3 cards (the ethernet key/unit
// capacity, 4000-4009 / 7-16) plus one SR-IOV card. Success means SR-IOV does
// not count against the ten-ethernet-card limit.
func (r *runner) e14NICLimit(ctx context.Context, sc *sriovCtx, res *result) {
	devices := make([]vimtypes.BaseVirtualDevice, 0, 11)

	for range 10 {
		dev, err := r.newVmxnet3(ctx, sc, nil)
		if err != nil {
			res.findf("NIC limit: failed to build vmxnet3: %v", err)

			return
		}

		devices = append(devices, dev)
	}

	card, err := r.newSriovCard(ctx, sc, nil)
	if err != nil {
		res.findf("NIC limit: failed to build SR-IOV card: %v", err)

		return
	}

	devices = append(devices, card)

	spec := r.sriovConfigSpec(r.vmName(res.ID, "limit"))
	spec.DeviceChange = addSpec(devices...)

	label := "NIC limit: CreateVM with ten vmxnet3 cards plus one SR-IOV card, all UnitNumber nil"
	s := step{Name: label, Requested: requestedFrom(spec.DeviceChange)}

	vm, err := r.createVM(ctx, spec)
	if err != nil {
		s.Err = err.Error()
		s.Faults = captureFaults(err)
		res.Steps = append(res.Steps, s)
		res.findf("NIC limit: ten vmxnet3 + one SR-IOV was REJECTED %v — `%s`. If the fault "+
			"concerns the NIC count, SR-IOV counts against the ten-NIC limit.", faultTypes(s), err)

		return
	}

	r.observeSriovStep(ctx, vm, &s)
	res.Steps = append(res.Steps, s)

	var vmxCount int

	for _, d := range s.Observed {
		if isEthKind(d.Kind) && d.Kind != sriovKind {
			vmxCount++
		}
	}

	for _, d := range sriovOf(s.Observed) {
		res.findf("NIC limit: ten vmxnet3 + one SR-IOV was ACCEPTED (%d non-SR-IOV NICs observed); "+
			"SR-IOV card got %s — SR-IOV does not count against the ten-ethernet-card limit.",
			vmxCount, describeSriov(d))
	}
}

// e14Summarise adds a whole-run verdict across every distinct SR-IOV card
// observed. A card is identified by its VM (the step-name prefix before ":")
// and its device key, so a card re-observed across steps counts once.
func (r *runner) e14Summarise(res *result) {
	type cardID struct {
		vm  string
		key int32
	}

	cards := map[cardID]deviceInfo{}

	for _, s := range res.Steps {
		vm, _, _ := strings.Cut(s.Name, ":")

		for _, d := range sriovOf(s.Observed) {
			cards[cardID{vm, d.Key}] = d
		}
	}

	var inBand, outBand, ethKeys, otherKeys int

	for _, d := range cards {
		if d.UnitNumber != nil && *d.UnitNumber >= nicUnitNumberFirst && *d.UnitNumber <= nicUnitNumberLast {
			inBand++
		} else {
			outBand++
		}

		if d.Key >= 4000 && d.Key <= 4009 {
			ethKeys++
		} else {
			otherKeys++
		}
	}

	res.findf("Summary across %d distinct SR-IOV cards (per VM and device key): "+
		"%d inside the 7-16 band, %d outside it; %d with 4000-series keys, %d with other keys.",
		len(cards), inBand, outBand, ethKeys, otherKeys)

	if inBand == 0 && outBand > 0 {
		res.findf("VERDICT: SR-IOV cards do NOT share the 7-16 ethernet unit band or the 4000-series " +
			"keys; the spec's Q3 assumption is wrong and the plan must treat SR-IOV separately.")
	} else if outBand == 0 && inBand > 0 {
		res.findf("VERDICT: SR-IOV cards share the 7-16 ethernet unit band; the spec's Q3 assumption holds.")
	}
}

// isEthKind reports whether a recorded device kind is an ethernet card.
func isEthKind(kind string) bool {
	switch kind {
	case sriovKind, "VirtualVmxnet3", "VirtualVmxnet3Vrdma", "VirtualVmxnet2", "VirtualVmxnet",
		"VirtualE1000", "VirtualE1000e", "VirtualPCNet32":
		return true
	default:
		return false
	}
}

// int32PtrEqual compares two optional int32 values.
func int32PtrEqual(a, b *int32) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// derefOr renders an optional int32 for display.
func derefOr(v *int32) string {
	if v == nil {
		return "nil"
	}

	return fmt.Sprintf("%d", *v)
}

// e14Ordering creates a never-powered-on VM with three SR-IOV cards on
// distinct portgroups (so each is identifiable by backing), to learn how a
// multi-card create orders SR-IOV cards against the ConfigSpec, then probes
// gap filling after removing the middle card, an explicit device Key, and
// whether a non-SR-IOV PCI passthrough device shares the pciPassthruN pool.
func (r *runner) e14Ordering(ctx context.Context, sc *sriovCtx, res *result) {
	names := splitList(r.cfg.sriovOrderNetworks)
	if len(names) < 2 {
		res.findf("Ordering: skipped; pass at least two -sriov-order-networks to tell SR-IOV cards apart.")

		return
	}

	devices := make([]vimtypes.BaseVirtualDevice, 0, len(names))

	for _, n := range names {
		net, err := r.finder.Network(ctx, n)
		if err != nil {
			res.findf("Ordering: failed to find network %q: %v", n, err)

			return
		}

		card, err := r.newSriovCardOn(ctx, sc, net, nil)
		if err != nil {
			res.findf("Ordering: %v", err)

			return
		}

		devices = append(devices, card)
	}

	spec := r.sriovConfigSpec(r.vmName(res.ID, "order"))
	spec.DeviceChange = addSpec(devices...)

	create := step{Name: fmt.Sprintf("Ordering VM: CreateVM with %d SR-IOV cards on %v, "+
		"in that ConfigSpec order, all UnitNumber nil", len(names), names),
		Requested: requestedFrom(spec.DeviceChange)}

	vm, err := r.createVM(ctx, spec)
	if err != nil {
		create.Err = err.Error()
		create.Faults = captureFaults(err)
		res.Steps = append(res.Steps, create)
		res.findf("Ordering: CreateVM FAILED `%s`.", err)

		return
	}

	r.observeSriovStep(ctx, vm, &create)

	raw, err := r.observeEthCards(ctx, vm)
	if err == nil {
		var order []string
		for _, d := range raw {
			order = append(order, fmt.Sprintf("key=%d unit=%s %s", d.Key, d.unit(), d.Backing))
		}

		create.Notes = append(create.Notes, "config.hardware.device order (unsorted): "+
			strings.Join(order, "; "))
	}

	res.Steps = append(res.Steps, create)

	for i, d := range sriovOf(create.Observed) {
		res.findf("Ordering: SR-IOV card #%d by unit is %s backing=%s.", i, describeSriov(d), d.Backing)
	}

	prev := create.Observed
	prev = r.e14RemoveMiddle(ctx, sc, vm, prev, res)
	prev = r.e14ExplicitKey(ctx, sc, vm, prev, res)
	r.e14PassthroughPool(ctx, sc, vm, prev, res)
}

// e14RemoveMiddle removes the middle SR-IOV card by unit and adds a new
// nil-unit card: does the new card fill the gap, or take the next free unit
// below the lowest occupied one?
func (r *runner) e14RemoveMiddle(
	ctx context.Context,
	sc *sriovCtx,
	vm *object.VirtualMachine,
	prev []deviceInfo,
	res *result) []deviceInfo {

	cards := sriovOf(prev)
	if len(cards) < 3 {
		return prev
	}

	middle := cards[len(cards)/2]

	hw, err := r.hardware(ctx, vm)
	if err != nil {
		return prev
	}

	dev := hw.FindByKey(middle.Key)
	if dev == nil {
		return prev
	}

	rm := r.sriovReconfigure(ctx, vm, fmt.Sprintf("Ordering VM: remove the middle SR-IOV card (key %d)", middle.Key),
		removeSpec(dev))
	res.Steps = append(res.Steps, rm)

	if rm.Err != "" {
		res.findf("Remove middle SR-IOV card FAILED `%s`.", rm.Err)

		return prev
	}

	card, err := r.newSriovCard(ctx, sc, nil)
	if err != nil {
		return rm.Observed
	}

	add := r.sriovReconfigure(ctx, vm, "Ordering VM: add a new SR-IOV card, UnitNumber nil", addSpec(card))
	res.Steps = append(res.Steps, add)

	added := sriovOf(addedSince(rm.Observed, add.Observed))
	if add.Err != "" || len(added) == 0 {
		res.findf("Gap fill: re-add after removing the middle card failed or added nothing (`%s`).", add.Err)

		return rm.Observed
	}

	res.findf("Gap fill: removed middle card %s; the new nil-unit card got %s (gap filled: %t).",
		describeSriov(middle), describeSriov(added[0]), added[0].Key == middle.Key)

	return add.Observed
}

// e14ExplicitKey adds an SR-IOV card with an explicit, positive device Key in
// the passthrough range and a nil unit: can the Key, rather than the unit,
// pin an SR-IOV card's slot?
func (r *runner) e14ExplicitKey(
	ctx context.Context,
	sc *sriovCtx,
	vm *object.VirtualMachine,
	prev []deviceInfo,
	res *result) []deviceInfo {

	const wantKey = int32(13010)

	card, err := r.newSriovCard(ctx, sc, nil)
	if err != nil {
		return prev
	}

	card.GetVirtualDevice().Key = wantKey

	label := fmt.Sprintf("explicit device Key %d, UnitNumber nil", wantKey)
	s := r.sriovReconfigure(ctx, vm, "Ordering VM: add an SR-IOV card with "+label, addSpec(card))
	res.Steps = append(res.Steps, s)

	added := sriovOf(addedSince(prev, s.Observed))

	switch {
	case s.Err != "":
		res.findf("Explicit Key: %s was REJECTED %v — `%s`.", label, faultTypes(s), s.Err)

		return prev
	case len(added) == 0:
		res.findf("Explicit Key: %s succeeded but no new SR-IOV card was observed.", label)

		return s.Observed
	default:
		res.findf("Explicit Key: %s -> %s (Key honoured: %t).", label, describeSriov(added[0]),
			added[0].Key == wantKey)

		return s.Observed
	}
}

// e14PassthroughPool adds a non-SR-IOV PCI passthrough device (config only;
// the VM is never powered on) drawn from the host's ConfigTarget, then another
// SR-IOV card, to see whether plain passthrough and SR-IOV share one
// pciPassthruN pool and fill it from opposite ends.
func (r *runner) e14PassthroughPool(
	ctx context.Context,
	sc *sriovCtx,
	vm *object.VirtualMachine,
	prev []deviceInfo,
	res *result) {

	dev, desc := passthroughFromTarget(sc)
	if dev == nil {
		res.findf("Passthrough pool: skipped; %s.", desc)

		return
	}

	dev.GetVirtualDevice().Key = sc.key()

	s := r.sriovReconfigure(ctx, vm, "Ordering VM: add a non-SR-IOV PCI passthrough device "+desc+
		" (config only, never powered on)", addSpec(dev))
	res.Steps = append(res.Steps, s)

	added := addedSince(prev, s.Observed)

	switch {
	case s.Err != "":
		res.findf("Passthrough pool: adding %s was REJECTED %v — `%s`.", desc, faultTypes(s), s.Err)

		return
	case len(added) == 0:
		res.findf("Passthrough pool: adding %s succeeded but no new device was observed.", desc)

		return
	}

	res.findf("Passthrough pool: non-SR-IOV passthrough %s got kind=%s %s.", desc, added[0].Kind,
		describeSriov(added[0]))

	card, err := r.newSriovCard(ctx, sc, nil)
	if err != nil {
		return
	}

	s2 := r.sriovReconfigure(ctx, vm, "Ordering VM: add another SR-IOV card after the passthrough device",
		addSpec(card))
	res.Steps = append(res.Steps, s2)

	if a := sriovOf(addedSince(s.Observed, s2.Observed)); len(a) > 0 {
		res.findf("Passthrough pool: the next SR-IOV card got %s.", describeSriov(a[0]))
	}
}

// passthroughFromTarget builds a non-SR-IOV VirtualPCIPassthrough from the
// ConfigTarget: a DirectPath device if one is listed, else a dynamic
// DirectPath device, else nil with a reason.
func passthroughFromTarget(sc *sriovCtx) (vimtypes.BaseVirtualDevice, string) {
	if sc.target == nil {
		return nil, "no ConfigTarget (automatic PF assignment)"
	}

	for _, base := range sc.target.PciPassthrough {
		info, ok := base.(*vimtypes.VirtualMachinePciPassthroughInfo)
		if !ok {
			// *VirtualMachineSriovInfo is SR-IOV; not what this step wants.
			continue
		}

		pci := info.PciDevice

		return &vimtypes.VirtualPCIPassthrough{
			VirtualDevice: vimtypes.VirtualDevice{
				Backing: &vimtypes.VirtualPCIPassthroughDeviceBackingInfo{
					Id:       pci.Id,
					DeviceId: pciID(int32(pci.DeviceId)),
					SystemId: info.SystemId,
					VendorId: pci.VendorId,
				},
			},
		}, fmt.Sprintf("DirectPath %s (%s)", pci.Id, pci.DeviceName)
	}

	for _, info := range sc.target.DynamicPassthrough {
		return &vimtypes.VirtualPCIPassthrough{
			VirtualDevice: vimtypes.VirtualDevice{
				Backing: &vimtypes.VirtualPCIPassthroughDynamicBackingInfo{
					AllowedDevice: []vimtypes.VirtualPCIPassthroughAllowedDevice{{
						VendorId: info.VendorId,
						DeviceId: info.DeviceId,
					}},
				},
			},
		}, fmt.Sprintf("dynamic DirectPath %s:%s (%s)", pciID(info.VendorId), pciID(info.DeviceId), info.DeviceName)
	}

	return nil, "the host ConfigTarget lists no non-SR-IOV passthrough device"
}

// pciID renders a PCI vendor or device ID as the four-hex-digit string the
// passthrough backing expects. The VIM API stores these 16-bit IDs in signed
// fields, so an ID above 0x7fff arrives negative and must be reinterpreted,
// not range-checked.
func pciID(v int32) string {
	return fmt.Sprintf("%x", uint16(v)) //nolint:gosec // Reinterpreting a 16-bit ID, see above.
}

// splitList parses a comma-separated list, dropping empty entries.
func splitList(s string) []string {
	var out []string

	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}
