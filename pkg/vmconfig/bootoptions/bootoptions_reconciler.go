// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package bootoptions

import (
	"context"
	"fmt"
	"slices"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkglog "github.com/vmware-tanzu/vm-operator/pkg/log"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/pkg/util/resize"
	"github.com/vmware-tanzu/vm-operator/pkg/vmconfig"
)

type reconciler struct{}

var _ vmconfig.Reconciler = reconciler{}

func New() vmconfig.Reconciler {
	return reconciler{}
}

func (r reconciler) Name() string {
	return "bootoptions"
}

func (r reconciler) OnResult(
	_ context.Context,
	_ *vmopv1.VirtualMachine,
	_ mo.VirtualMachine,
	_ error) error {
	return nil
}

// Reconcile configures the VM's boot options.
func Reconcile(
	ctx context.Context,
	k8sClient ctrlclient.Client,
	vimClient *vim25.Client,
	vm *vmopv1.VirtualMachine,
	moVM mo.VirtualMachine,
	configSpec *vimtypes.VirtualMachineConfigSpec) error {

	return New().Reconcile(ctx, k8sClient, vimClient, vm, moVM, configSpec)
}

func (r reconciler) Reconcile(
	ctx context.Context,
	k8sClient ctrlclient.Client,
	vimClient *vim25.Client,
	vm *vmopv1.VirtualMachine,
	moVM mo.VirtualMachine,
	configSpec *vimtypes.VirtualMachineConfigSpec) error {

	if ctx == nil {
		panic("context is nil")
	}
	if k8sClient == nil {
		panic("k8sClient is nil")
	}
	if vimClient == nil {
		panic("vimClient is nil")
	}
	if vm == nil {
		panic("vm is nil")
	}
	if configSpec == nil {
		panic("configSpec is nil")
	}

	ci := vimtypes.VirtualMachineConfigInfo{}
	if moVM.Config != nil {
		ci = *moVM.Config
	}

	vmBootOptions := &vmopv1.VirtualMachineBootOptions{}
	if vm.Spec.BootOptions != nil {
		vmBootOptions = vm.Spec.BootOptions
	}

	var csBootOptions vimtypes.VirtualMachineBootOptions
	if configSpec.BootOptions != nil {
		csBootOptions = *configSpec.BootOptions
	}

	if vmBootOptions.BootDelay != nil {
		csBootOptions.BootDelay = vmBootOptions.BootDelay.Duration.Milliseconds()
	}

	if vmBootOptions.BootRetry != "" {
		csBootOptions.BootRetryEnabled = ptr.To(vmBootOptions.BootRetry == vmopv1.VirtualMachineBootOptionsBootRetryEnabled)
	}

	if vmBootOptions.BootRetryDelay != nil {
		csBootOptions.BootRetryDelay = vmBootOptions.BootRetryDelay.Duration.Milliseconds()
	}

	if vmBootOptions.EFISecureBoot != "" {
		csBootOptions.EfiSecureBootEnabled = ptr.To(vmBootOptions.EFISecureBoot == vmopv1.VirtualMachineBootOptionsEFISecureBootEnabled)
	}

	var networkBootProtocol string
	switch vmBootOptions.NetworkBootProtocol {
	case vmopv1.VirtualMachineBootOptionsNetworkBootProtocolIP6:
		networkBootProtocol = string(vimtypes.VirtualMachineBootOptionsNetworkBootProtocolTypeIpv6)
	case vmopv1.VirtualMachineBootOptionsNetworkBootProtocolIP4:
		networkBootProtocol = string(vimtypes.VirtualMachineBootOptionsNetworkBootProtocolTypeIpv4)
	default:
	}
	csBootOptions.NetworkBootProtocol = networkBootProtocol

	bootOrder, err := reconcileBootOrder(ctx, k8sClient, vm, moVM, vmBootOptions, ci)
	if err != nil {
		return err
	}

	csBootOptions.BootOrder = bootOrder
	cs := vimtypes.VirtualMachineConfigSpec{
		BootOptions: &csBootOptions,
	}

	resize.CompareBootOptions(ci, cs, configSpec)

	return nil
}

func reconcileBootOrder(
	ctx context.Context,
	k8sClient ctrlclient.Client,
	vm *vmopv1.VirtualMachine,
	moVM mo.VirtualMachine,
	vmBootOptions *vmopv1.VirtualMachineBootOptions,
	ci vimtypes.VirtualMachineConfigInfo) ([]vimtypes.BaseVirtualMachineBootOptionsBootableDevice, error) {

	devices := object.VirtualDeviceList(ci.Hardware.Device)
	allDisks := devices.SelectByType((*vimtypes.VirtualDisk)(nil))
	allCdrom := devices.SelectByType((*vimtypes.VirtualCdrom)(nil))

	var (
		bootOrder       []vimtypes.BaseVirtualMachineBootOptionsBootableDevice
		networkInfo     *network.Info
		networkObserved bool
	)
	for _, bd := range vmBootOptions.BootOrder {
		switch bd.Type {
		case vmopv1.VirtualMachineBootOptionsBootableDiskDevice:
			// Get list of volumes from vm.status.volumes and search
			// for volume with matching name and get UUID of volume.
			var volUUID string
			for _, v := range vm.Status.Volumes {
				if v.Name == bd.Name {
					volUUID = v.DiskUUID
					break
				}
			}

			if volUUID == "" {
				return nil, fmt.Errorf("unable to locate disk matching name %q", bd.Name)
			}
			// Search ConfigInfo for disk with matching UUID and obtain
			// device key.
			var (
				diskUUID string
				bdd      *vimtypes.VirtualMachineBootOptionsBootableDiskDevice
			)
			for _, d := range allDisks {
				vd := d.(*vimtypes.VirtualDisk)
				switch tb := vd.Backing.(type) {
				case *vimtypes.VirtualDiskSeSparseBackingInfo:
					diskUUID = tb.Uuid
				case *vimtypes.VirtualDiskSparseVer1BackingInfo:
					// No UUID for this backing type.
					continue
				case *vimtypes.VirtualDiskSparseVer2BackingInfo:
					diskUUID = tb.Uuid
				case *vimtypes.VirtualDiskFlatVer1BackingInfo:
					// No UUID for this backing type.
					continue
				case *vimtypes.VirtualDiskFlatVer2BackingInfo:
					diskUUID = tb.Uuid
				case *vimtypes.VirtualDiskLocalPMemBackingInfo:
					diskUUID = tb.Uuid
				case *vimtypes.VirtualDiskRawDiskMappingVer1BackingInfo:
					diskUUID = tb.Uuid
				case *vimtypes.VirtualDiskRawDiskVer2BackingInfo:
					diskUUID = tb.Uuid
				case *vimtypes.VirtualDiskPartitionedRawDiskVer2BackingInfo:
					diskUUID = tb.Uuid
				}
				if volUUID == diskUUID {
					bdd = &vimtypes.VirtualMachineBootOptionsBootableDiskDevice{
						DeviceKey: vd.Key,
					}
					break
				}
			}

			if bdd == nil {
				return nil, fmt.Errorf("unable to locate a disk matching UUID %q", volUUID)
			}
			bootOrder = append(bootOrder, bdd)
		case vmopv1.VirtualMachineBootOptionsBootableNetworkDevice:
			if vm.Spec.Network == nil {
				return nil, fmt.Errorf("spec.network is not defined for VM %q", vm.NamespacedName())
			}
			// Search vm.spec.network.interfaces for the interface with a
			// matching name.
			if !slices.ContainsFunc(vm.Spec.Network.Interfaces,
				func(iface vmopv1.VirtualMachineNetworkInterfaceSpec) bool {
					return iface.Name == bd.Name
				}) {

				return nil, fmt.Errorf("unable to locate a network interface matching name %q in spec.network", bd.Name)
			}

			// Observe at most once per call, and only when the boot order names
			// a network device.
			if !networkObserved {
				networkInfo = network.ObservedInfo(ctx, k8sClient, vm, moVM)
				networkObserved = true
			}

			bed, ok, err := networkBootDevice(ctx, networkInfo, bd.Name)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			bootOrder = append(bootOrder, bed)
		case vmopv1.VirtualMachineBootOptionsBootableCDRomDevice:
			// If there is a CD-ROM device then append the type to the
			// ConfigSpec boot order. No need to specify device key. The
			// first one will be used.
			if len(allCdrom) == 0 {
				return nil, fmt.Errorf("no cdrom device found")
			}
			bootOrder = append(bootOrder, &vimtypes.VirtualMachineBootOptionsBootableCdromDevice{})
		default:
			return nil, fmt.Errorf("unsupported bootable device type: %q", bd.Type)
		}
	}

	return bootOrder, nil
}

// networkBootDevice returns the bootable Ethernet device for the named
// interface from the shared hardware match, so network boot uses the same card
// that status and device reconciliation attribute to the interface.
//
// When VMNetworkUnitNumbers is enabled, a numbered interface is matched only
// by its exact unit number. If no card is at that unit number yet, for example
// because a unit number change has not been applied, the interface gets no
// boot order entry rather than another interface's card, and the second
// result is false. Any other interface without a card is an error.
func networkBootDevice(
	ctx context.Context,
	info *network.Info,
	name string) (*vimtypes.VirtualMachineBootOptionsBootableEthernetDevice, bool, error) {

	if info != nil {
		for _, iface := range info.Interfaces {
			if iface.Spec.Name != name {
				continue
			}

			if network.IsClaim(iface.Match.Kind) {
				return &vimtypes.VirtualMachineBootOptionsBootableEthernetDevice{
					DeviceKey: iface.Match.AdapterKey,
				}, true, nil
			}

			if iface.Spec.UnitNumber != nil &&
				pkgcfg.FromContext(ctx).Features.VMNetworkUnitNumbers {

				pkglog.FromContextOrDefault(ctx).Info(
					"Skipping network boot device because no Ethernet card is at the interface's unit number",
					"interface", name,
					"unitNumber", *iface.Spec.UnitNumber)
				return nil, false, nil
			}

			break
		}
	}

	return nil, false, fmt.Errorf("unable to locate network interface matching name %q", name)
}
