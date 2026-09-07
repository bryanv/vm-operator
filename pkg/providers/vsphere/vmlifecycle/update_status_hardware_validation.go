// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle

import (
	"fmt"
	"strings"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/conditions"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
)

// IssueReporter defines the common interface for hardware validation issues.
// This interface allows polymorphic handling of different issue types.
type IssueReporter interface {
	// HasIssues returns true if there are any issues to report.
	HasIssues() bool
	// Message formats all issues into a human-readable message.
	Message() string
}

// Compile-time checks to ensure issue types implement IssueReporter.
var (
	_ IssueReporter = (*ControllerIssues)(nil)
	_ IssueReporter = (*VolumeIssues)(nil)
	_ IssueReporter = (*CDROMIssues)(nil)
	_ IssueReporter = (*NICIssues)(nil)
	_ IssueReporter = (*HardwareConfigIssues)(nil)
)

// ControllerIssues stores controller configuration issues found during verification.
// The missing and unexpected controllers are sorted.
type ControllerIssues struct {
	Missing    []pkgutil.ControllerID
	Unexpected []pkgutil.ControllerID
}

// HasIssues returns true if there are any issues to report.
func (c *ControllerIssues) HasIssues() bool {
	return len(c.Missing) > 0 || len(c.Unexpected) > 0
}

// Message formats all issues into a human-readable message.
// Each issue type is on a separate line, and items are formatted as
// comma-separated lists for better readability.
func (c *ControllerIssues) Message() string {
	return formatIssues(
		formatList(c.Missing, "missing controllers"),
		formatList(c.Unexpected, "unexpected controllers"),
	)
}

// VolumeIssues stores volume configuration issues found during verification.
// The missing and unexpected volumes are sorted. The incomplete placement
// volumes are appended in order when looping through the spec list.
type VolumeIssues struct {
	Missing             []pkgutil.DevicePlacement
	Unexpected          []pkgutil.DevicePlacement
	IncompletePlacement []string
}

// HasIssues returns true if there are any issues to report.
func (v *VolumeIssues) HasIssues() bool {
	return len(v.Missing) > 0 ||
		len(v.Unexpected) > 0 ||
		len(v.IncompletePlacement) > 0
}

// Message formats all issues into a human-readable message.
// Each issue type is on a separate line, and items are formatted as
// comma-separated lists for better readability.
func (v *VolumeIssues) Message() string {
	return formatIssues(
		formatList(v.Missing, "missing volumes"),
		formatList(v.Unexpected, "unexpected volumes"),
		formatStrings(v.IncompletePlacement, "volumes with incomplete placement"),
	)
}

// CDROMIssues stores CD-ROM device configuration issues found during verification.
// The missing and unexpected CD-ROM devices are sorted. The incomplete placement
// CD-ROMs and CD-ROM failed resolution are appended in order when looping through
// the spec list.
type CDROMIssues struct {
	Missing             []pkgutil.DevicePlacement
	Unexpected          []pkgutil.DevicePlacement
	IncompletePlacement []string
	FailedResolution    []string
}

// HasIssues returns true if there are any issues to report.
func (c *CDROMIssues) HasIssues() bool {
	return len(c.Missing) > 0 ||
		len(c.Unexpected) > 0 ||
		len(c.IncompletePlacement) > 0 ||
		len(c.FailedResolution) > 0
}

// Message formats all issues into a human-readable message.
// Each issue type is on a separate line, and items are formatted as
// comma-separated lists for better readability.
func (c *CDROMIssues) Message() string {
	return formatIssues(
		formatList(c.Missing, "missing CD-ROM devices"),
		formatList(c.Unexpected, "unexpected CD-ROM devices"),
		formatStrings(c.IncompletePlacement, "CD-ROM devices with incomplete placement"),
		formatStrings(c.FailedResolution, "CD-ROM devices with failed resolution"),
	)
}

// HardwareConfigIssues stores all hardware device configuration issues
// found during verification. This is kept for backward compatibility
// and can be used to aggregate all device-specific issues.
type HardwareConfigIssues struct {
	ControllerIssues ControllerIssues
	VolumeIssues     VolumeIssues
	CDROMIssues      CDROMIssues
	NICIssues        NICIssues
}

// HasIssues returns true if there are any issues to report.
func (h *HardwareConfigIssues) HasIssues() bool {
	return h.ControllerIssues.HasIssues() ||
		h.VolumeIssues.HasIssues() ||
		h.CDROMIssues.HasIssues() ||
		h.NICIssues.HasIssues()
}

// Message formats all issues into a concise summary message.
// Following Kubernetes best practices, this provides a concise summary
// rather than duplicating all detailed messages. The message references
// the specific condition types where detailed information is available.
// Example output: "Hardware configuration issues detected. See VirtualMachineHardwareControllersVerified, VirtualMachineHardwareVolumesVerified conditions for details.".
func (h *HardwareConfigIssues) Message() string {
	var conditionTypes []string

	if h.ControllerIssues.HasIssues() {
		conditionTypes = append(conditionTypes, vmopv1.VirtualMachineHardwareControllersVerified)
	}
	if h.VolumeIssues.HasIssues() {
		conditionTypes = append(conditionTypes, vmopv1.VirtualMachineHardwareVolumesVerified)
	}
	if h.CDROMIssues.HasIssues() {
		conditionTypes = append(conditionTypes, vmopv1.VirtualMachineHardwareCDROMVerified)
	}
	if h.NICIssues.HasIssues() {
		conditionTypes = append(conditionTypes, vmopv1.VirtualMachineHardwareNICsVerified)
	}

	if len(conditionTypes) == 0 {
		return ""
	}

	return fmt.Sprintf("Hardware configuration issues detected. See %s conditions for details.",
		strings.Join(conditionTypes, ", "))
}

// formatIssues combines multiple message strings, filtering out empty ones.
// Non-empty messages are joined with newlines.
func formatIssues(messages ...string) string {
	var parts []string
	for _, msg := range messages {
		if msg != "" {
			parts = append(parts, msg)
		}
	}
	return strings.Join(parts, "\n")
}

// formatList formats a slice of items that implement fmt.Stringer into a message.
// Returns an empty string if the slice is empty.
func formatList[T fmt.Stringer](items []T, label string) string {
	if len(items) == 0 {
		return ""
	}
	itemStrs := make([]string, 0, len(items))
	for _, item := range items {
		itemStrs = append(itemStrs, item.String())
	}
	return fmt.Sprintf("%s: %s", label, strings.Join(itemStrs, ", "))
}

// formatStrings formats a slice of strings into a message.
// Returns an empty string if the slice is empty.
func formatStrings(items []string, label string) string {
	if len(items) == 0 {
		return ""
	}
	return fmt.Sprintf("%s: %s", label, strings.Join(items, ", "))
}

// NICIssues stores network interface placement issues found during
// verification of the interfaces that carry a unit number. Interface names
// are appended in order when looping through the spec list.
type NICIssues struct {
	// Missing holds the names of interfaces whose declared unit number has no
	// device at that slot (the G13 state: an admitted-but-not-yet-applied
	// renumber on a powered-on VM, or a G7-skipped backfill write).
	Missing []string
	// Mismatched holds the names of interfaces whose declared unit number has
	// a device that disagrees with the interface's desired state.
	Mismatched []string
}

// HasIssues returns true if there are any issues to report.
func (n *NICIssues) HasIssues() bool {
	return len(n.Missing) > 0 ||
		len(n.Mismatched) > 0
}

// Message formats all issues into a human-readable message.
// Each issue type is on a separate line, and items are formatted as
// comma-separated lists for better readability.
func (n *NICIssues) Message() string {
	return formatIssues(
		formatStrings(n.Missing, "network interfaces with no device at their declared unit number"),
		formatStrings(n.Mismatched, "network interfaces whose device at the declared unit number does not match the desired state"),
	)
}

// reconcileHardwareCondition updates the hardware device configuration conditions
// by verifying that the VM's hardware device configuration matches the desired
// state specified in the spec. It sets individual conditions for controllers,
// volumes, and CD-ROMs, then aggregates them into VirtualMachineHardwareDeviceConfigVerified.
// The aggregated condition provides a concise summary rather than duplicating
// all condition messages. Detailed information is available in the individual
// device-specific conditions.
func reconcileHardwareCondition(
	vmCtx pkgctx.VirtualMachineContext,
	k8sClient ctrlclient.Client,
	_ *object.VirtualMachine,
	_ ReconcileStatusData) []error { //nolint:unparam

	var (
		issues = HardwareConfigIssues{}
		hwInfo = pkgutil.BuildHardwareInfo(vmCtx.MoVM)
	)

	// The disk/CD-ROM/controller placement checks are gated on the
	// VMSharedDisks capability, which backfills and verifies those fields;
	// they must not run for VMs whose specs were never disk-backfilled just
	// because the NIC capability widened the outer gate.
	if pkgcfg.FromContext(vmCtx).Features.VMSharedDisks {
		checkControllers(vmCtx.VM, hwInfo, &issues.ControllerIssues)
		checkVolumes(vmCtx.VM, hwInfo, &issues.VolumeIssues)
		checkCDROMDevices(vmCtx, k8sClient, hwInfo, &issues.CDROMIssues)
	}

	// The NIC check runs only when the VMNetworkUnitNumbers capability is
	// enabled: without a declared unit number there is no slot to verify and
	// un-numbered interfaces keep their pre-existing MAC/ExternalID/backing
	// matching, so there is nothing to report for them.
	if pkgcfg.FromContext(vmCtx).Features.VMNetworkUnitNumbers {
		checkNICPlacement(vmCtx, k8sClient, &issues.NICIssues)
	}

	if issues.HasIssues() {
		conditions.MarkFalse(
			vmCtx.VM,
			vmopv1.VirtualMachineHardwareDeviceConfigVerified,
			vmopv1.VirtualMachineHardwareDeviceConfigMismatchReason,
			"%s",
			issues.Message())
		return nil
	}

	conditions.MarkTrue(vmCtx.VM, vmopv1.VirtualMachineHardwareDeviceConfigVerified)
	return nil
}

// checkControllers verifies that all controllers specified in the VM spec are
// attached and no extra controllers exist. It sets the
// VirtualMachineHardwareControllersVerified condition.
func checkControllers(
	vm *vmopv1.VirtualMachine,
	hwInfo pkgutil.HardwareInfo,
	issues *ControllerIssues) {

	var (
		expected = sets.New[pkgutil.ControllerID]()
		actual   = hwInfo.Controllers
	)

	if hw := vm.Spec.Hardware; hw != nil {
		for _, ctrl := range hw.IDEControllers {
			expected.Insert(pkgutil.ControllerID{
				ControllerType: vmopv1.VirtualControllerTypeIDE,
				BusNumber:      ctrl.BusNumber,
			})
		}
		for _, ctrl := range hw.NVMEControllers {
			expected.Insert(pkgutil.ControllerID{
				ControllerType: vmopv1.VirtualControllerTypeNVME,
				BusNumber:      ctrl.BusNumber,
			})
		}
		for _, ctrl := range hw.SATAControllers {
			expected.Insert(pkgutil.ControllerID{
				ControllerType: vmopv1.VirtualControllerTypeSATA,
				BusNumber:      ctrl.BusNumber,
			})
		}
		for _, ctrl := range hw.SCSIControllers {
			expected.Insert(pkgutil.ControllerID{
				ControllerType: vmopv1.VirtualControllerTypeSCSI,
				BusNumber:      ctrl.BusNumber,
			})
		}
	}

	issues.Missing, issues.Unexpected = pkgutil.DiffSets(expected, actual)

	if issues.HasIssues() {
		conditions.MarkFalse(
			vm,
			vmopv1.VirtualMachineHardwareControllersVerified,
			vmopv1.VirtualMachineHardwareControllersMismatchReason,
			"%s",
			issues.Message())
		return
	}

	conditions.MarkTrue(vm, vmopv1.VirtualMachineHardwareControllersVerified)
}

// checkVolumes verifies that all volumes in the spec are attached and
// no extra disks are attached that aren't in the spec. It sets the
// VirtualMachineHardwareVolumesVerified condition.
func checkVolumes(
	vm *vmopv1.VirtualMachine,
	hwInfo pkgutil.HardwareInfo,
	issues *VolumeIssues) {

	var (
		expected = sets.New[pkgutil.DevicePlacement]()
		actual   = sets.New[pkgutil.DevicePlacement]()
	)

	for _, vol := range vm.Spec.Volumes {
		if vol.ControllerType == "" ||
			vol.ControllerBusNumber == nil ||
			vol.UnitNumber == nil {
			issues.IncompletePlacement = append(issues.IncompletePlacement, vol.Name)
			continue
		}

		expected.Insert(pkgutil.DevicePlacement{
			Key:                 vol.Name,
			ControllerType:      vol.ControllerType,
			ControllerBusNumber: *vol.ControllerBusNumber,
			UnitNumber:          *vol.UnitNumber,
		})
	}

	// Build mapping from disk UUID to volume name for attached PVC volumes.
	// vm.Status.Volumes is reconciled by the volume batch controller, which
	// runs as a separate reconciler from this one. This creates a potential
	// race condition where vm.Status.Volumes may not be up-to-date when this
	// check runs. We rely on eventual consistency here: the volume batch
	// reconciler will eventually update vm.Status.Volumes, and this check
	// will be correct on subsequent reconciliation cycles. The volume batch
	// reconciler will eventually be moved into the same reconciler to
	// eliminate this race condition.
	volNameByDiskUUID := make(map[string]string)
	for _, volStatus := range vm.Status.Volumes {
		// Ignore Attached status here since we will be checking again the
		// hardware status directly.
		if volStatus.DiskUUID != "" {
			volNameByDiskUUID[volStatus.DiskUUID] = volStatus.Name
		}
	}

	// Build actual disks from hardware info.
	// hwInfo.Disks uses disk UUID as the key (from BuildHardwareInfo).
	// We need to map disk UUID to volume name to match against expected placements.
	// The placementKey in DevicePlacement should be the volume name (from spec),
	// not the disk UUID.
	for _, diskPlacement := range hwInfo.Disks.UnsortedList() {
		// Look up volume name by disk UUID
		// diskPlacement.Key is the disk UUID (from BuildHardwareInfo)
		if volName, found := volNameByDiskUUID[diskPlacement.Key]; found {
			actual.Insert(pkgutil.DevicePlacement{
				Key:                 volName, // Use volume name as the key
				ControllerType:      diskPlacement.ControllerType,
				ControllerBusNumber: diskPlacement.ControllerBusNumber,
				UnitNumber:          diskPlacement.UnitNumber,
			})
		}
	}

	issues.Missing, issues.Unexpected = pkgutil.DiffSets(expected, actual)

	if issues.HasIssues() {
		conditions.MarkFalse(
			vm,
			vmopv1.VirtualMachineHardwareVolumesVerified,
			vmopv1.VirtualMachineHardwareVolumesMismatchReason,
			"%s",
			issues.Message())
		return
	}

	conditions.MarkTrue(vm, vmopv1.VirtualMachineHardwareVolumesVerified)
}

// checkCDROMDevices verifies that all CD-ROM devices in the spec have
// matching virtual devices with the same backing file name, and no extra
// CD-ROM devices exist that aren't in the spec. It sets the
// VirtualMachineHardwareCDROMVerified condition.
func checkCDROMDevices(
	vmCtx pkgctx.VirtualMachineContext,
	k8sClient ctrlclient.Client,
	hwInfo pkgutil.HardwareInfo,
	issues *CDROMIssues) {

	var (
		vm       = vmCtx.VM
		expected = sets.New[pkgutil.DevicePlacement]()
		actual   = hwInfo.CDROMs
	)

	if vm.Spec.Hardware != nil {
		for _, cdromSpec := range vm.Spec.Hardware.Cdrom {
			if cdromSpec.ControllerType == "" ||
				cdromSpec.ControllerBusNumber == nil ||
				cdromSpec.UnitNumber == nil {
				issues.IncompletePlacement = append(issues.IncompletePlacement, cdromSpec.Name)
				continue
			}

			backingFileName, err := virtualmachine.GetBackingFileNameByImageRef(
				vmCtx, k8sClient, cdromSpec.Image, vm.Namespace, false, nil)
			if err != nil {
				vmCtx.Logger.Error(err, "failed to resolve backing file name for CD-ROM device",
					"cdromName", cdromSpec.Name, "image", cdromSpec.Image)
				issues.FailedResolution = append(issues.FailedResolution, cdromSpec.Name)
				continue
			}

			expected.Insert(pkgutil.DevicePlacement{
				Key:                 backingFileName,
				ControllerType:      cdromSpec.ControllerType,
				ControllerBusNumber: *cdromSpec.ControllerBusNumber,
				UnitNumber:          *cdromSpec.UnitNumber,
			})
		}
	}

	issues.Missing, issues.Unexpected = pkgutil.DiffSets(expected, actual)

	if issues.HasIssues() {
		conditions.MarkFalse(
			vmCtx.VM,
			vmopv1.VirtualMachineHardwareCDROMVerified,
			vmopv1.VirtualMachineHardwareCDROMMismatchReason,
			"%s",
			issues.Message())
		return
	}

	conditions.MarkTrue(vmCtx.VM, vmopv1.VirtualMachineHardwareCDROMVerified)
}

// checkNICPlacement verifies, for every spec.network.interfaces entry
// carrying a unit number, that the declared PCI unit slot is occupied by a
// device that agrees with the interface's desired state. It sets the
// VirtualMachineHardwareNICsVerified condition.
//
// This is a steady-state condition rather than an Event (spec.md G8.1): an
// explicit spec value disagreeing with the observed hardware, and an
// interface the schema-upgrade backfill deliberately skipped (G7), are both
// re-observable on every reconcile, and the condition additionally covers a
// slot the mutating webhook invented (I18) — a one-shot Event structurally
// cannot. Only the "value came from a positional zip" fact stays an Event
// (the backfill's NICUnitNumberBackfillAmbiguous), because nothing in the
// resulting spec records it.
//
// The check runs regardless of the VM's power state: a renumber is admitted
// on a powered-on VM but its device change is not applied until the next
// powered-off reconcile (I5), so an interface can legitimately declare an
// unoccupied slot for days (G13). Reporting that as a condition — never an
// error — is the not-yet-converged signal the boot-order reconciler also
// relies on.
//
// The desired-state comparison deliberately reuses the provider-dispatched
// matcher (network.FindMatchingEthCardForInterfaceSpec) evaluated over a
// single-card list holding only the device at the declared slot. That is the
// same criteria the reconcile-time compare-then-replace applies — backing
// per provider, MAC only when the interface specifies one, ExternalID only
// when non-empty — so the two can never drift, and device type is excluded
// on the same basis as reconcile (I2). The single-card list is essential:
// running the matcher over the full card list could return some OTHER
// interface's device (matched by backing), which would misreport.
func checkNICPlacement(
	vmCtx pkgctx.VirtualMachineContext,
	k8sClient ctrlclient.Client,
	issues *NICIssues) {

	vm := vmCtx.VM

	if vmCtx.MoVM.Config == nil {
		// Nothing observed: callers do not run hardware verification without
		// a vSphere config; do not touch the condition.
		return
	}

	if vm.Spec.Network == nil {
		conditions.MarkTrue(vm, vmopv1.VirtualMachineHardwareNICsVerified)
		return
	}

	// Observed ethernet devices (includes SR-IOV cards, which embed
	// VirtualEthernetCard), keyed lookup by declared unit number.
	ethCards := object.VirtualDeviceList(vmCtx.MoVM.Config.Hardware.Device).
		SelectByType((*vimtypes.VirtualEthernetCard)(nil))

	for i := range vm.Spec.Network.Interfaces {
		iface := &vm.Spec.Network.Interfaces[i]
		if iface.UnitNumber == nil {
			// Un-numbered interfaces are matched by MAC/ExternalID/backing at
			// reconcile time and have no declared slot to verify; they are
			// never reported here.
			continue
		}

		locatedIdx := -1
		for j, dev := range ethCards {
			if u := dev.GetVirtualDevice().UnitNumber; u != nil && *u == *iface.UnitNumber {
				locatedIdx = j
				break
			}
		}

		if locatedIdx < 0 {
			// The G13 state: the declared slot holds no device.
			issues.Missing = append(issues.Missing, iface.Name)
			continue
		}

		// Compare the device at the declared slot against the interface's
		// desired state with the provider-dispatched matcher over a
		// single-card list (see the doc comment for why).
		if network.FindMatchingEthCardForInterfaceSpec(
			vmCtx,
			k8sClient,
			*iface,
			object.VirtualDeviceList{ethCards[locatedIdx]}) < 0 {
			issues.Mismatched = append(issues.Mismatched, iface.Name)
		}
	}

	if issues.HasIssues() {
		conditions.MarkFalse(
			vm,
			vmopv1.VirtualMachineHardwareNICsVerified,
			vmopv1.VirtualMachineHardwareNICsMismatchReason,
			"%s",
			issues.Message())
		return
	}

	conditions.MarkTrue(vm, vmopv1.VirtualMachineHardwareNICsVerified)
}
