// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vsphere

import (
	"context"
	"fmt"

	vimtypes "github.com/vmware/govmomi/vim25/types"
	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha4"
	pkgcond "github.com/vmware-tanzu/vm-operator/pkg/conditions"
	"github.com/vmware-tanzu/vm-operator/pkg/providers"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/placement"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine"
	kubeutil "github.com/vmware-tanzu/vm-operator/pkg/util/kube"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
)

type vmGroupPlacementArgs struct {
	vmClasses          map[string]*vmopv1.VirtualMachineClass
	vmImages           map[string]*vmopv1.VirtualMachineImage
	cvmImages          map[string]*vmopv1.ClusterVirtualMachineImage
	storageClasses     map[string]*storagev1.StorageClass
	storageClassesToID map[string]string
	minCPUFreq         uint64

	vmSetResourcePolicyName *string
	childRPName             string

	vmConfigSpecs []vimtypes.VirtualMachineConfigSpec
}

func (vs *vSphereVMProvider) PlaceVirtualMachineGroup(
	ctx context.Context,
	group *vmopv1.VirtualMachineGroup,
	groupPlacements []providers.VMGroupPlacement) error {

	ctx = context.WithValue(ctx, vimtypes.ID{}, vs.getOpID(ctx, group, "groupPlacement"))

	placementArgs, err := vs.vmGroupPlacementGetPrereqs(ctx, group.Namespace, groupPlacements)
	if err != nil {
		return err
	}

	if err := vs.vmGroupGenerateConfigSpecs(ctx, groupPlacements, &placementArgs); err != nil {
		return err
	}

	results, err := vs.vmGroupDoPlacement(ctx, group.Namespace, &placementArgs)
	if err != nil {
		return err
	}

	if err := applyPlacementResultsToGroups(groupPlacements, results); err != nil {
		return err
	}

	return nil
}

func (vs *vSphereVMProvider) vmGroupPlacementGetPrereqs(
	ctx context.Context,
	namespace string,
	groupPlacements []providers.VMGroupPlacement) (vmGroupPlacementArgs, error) {

	groupPlacementArgs := vmGroupPlacementArgs{
		vmClasses:          make(map[string]*vmopv1.VirtualMachineClass),
		vmImages:           make(map[string]*vmopv1.VirtualMachineImage),
		cvmImages:          make(map[string]*vmopv1.ClusterVirtualMachineImage),
		storageClasses:     make(map[string]*storagev1.StorageClass),
		storageClassesToID: make(map[string]string),
	}

	for _, grpPlacement := range groupPlacements {
		for _, vm := range grpPlacement.VMMembers {
			if className := vm.Spec.ClassName; className != "" {
				groupPlacementArgs.vmClasses[className] = nil
			}

			if scName := vm.Spec.StorageClass; scName != "" {
				groupPlacementArgs.storageClasses[scName] = nil
			}

			if err := vmGroupResolveImagePrereq(vm, &groupPlacementArgs); err != nil {
				return vmGroupPlacementArgs{}, err
			}

			if err := vmGroupResolveVMSetRPPrereq(vm, &groupPlacementArgs); err != nil {
				return vmGroupPlacementArgs{}, err
			}
		}
	}

	if err := vs.vmGroupGetVMClasses(ctx, namespace, &groupPlacementArgs); err != nil {
		return vmGroupPlacementArgs{}, err
	}

	if err := vs.vmGroupGetStorageClasses(ctx, &groupPlacementArgs); err != nil {
		return vmGroupPlacementArgs{}, err
	}

	if err := vs.vmGroupGetImages(ctx, namespace, &groupPlacementArgs); err != nil {
		return vmGroupPlacementArgs{}, err
	}

	if err := vs.vmGroupGetVMSetResourcePolicy(ctx, namespace, &groupPlacementArgs); err != nil {
		return vmGroupPlacementArgs{}, err
	}

	return groupPlacementArgs, nil
}

func vmGroupResolveImagePrereq(
	vm *vmopv1.VirtualMachine,
	placementArgs *vmGroupPlacementArgs,
) error {

	// VM's Spec.Image should be set at this point. We really want the Kind to be
	// set since it could resolve to a different image by the time we actually try
	// to deploy the VM so kind of hard reuse GetVirtualMachineImageSpecAndStatus()
	// or ResolveImageName() in the group placement context.
	if vm.Spec.Image == nil {
		return fmt.Errorf("VM Spec.Image is nil")
	}

	switch vm.Spec.Image.Kind {
	case vmiKind:
		placementArgs.vmImages[vm.Spec.Image.Name] = nil
	case cvmiKind:
		placementArgs.cvmImages[vm.Spec.Image.Name] = nil
	default:
		return fmt.Errorf("unknown image kind %s", vm.Spec.Image.Kind)
	}

	return nil
}

func vmGroupResolveVMSetRPPrereq(
	vm *vmopv1.VirtualMachine,
	placementArgs *vmGroupPlacementArgs) error {

	// All VMs that we're going to place together must to either belong to the same
	// VirtualMachineSetResourcePolicy or none at all since that determines the child
	// ResourcePools but placement only takes a single list of candidates pools.

	var rpName string
	if rsv := vm.Spec.Reserved; rsv != nil {
		rpName = rsv.ResourcePolicyName
	}

	if placementArgs.vmSetResourcePolicyName == nil {
		placementArgs.vmSetResourcePolicyName = &rpName
	} else if rpName != *placementArgs.vmSetResourcePolicyName {
		return fmt.Errorf("all VirtualMachine members must belong to same VirtualMachineSetResourcePolicy")
	}

	return nil
}

func (vs *vSphereVMProvider) vmGroupGetVMClasses(
	ctx context.Context,
	namespace string,
	placementArgs *vmGroupPlacementArgs,
) error {

	var needMinCPUFreq bool

	for className := range placementArgs.vmClasses {
		vmClass := &vmopv1.VirtualMachineClass{}
		err := vs.k8sClient.Get(ctx, client.ObjectKey{Name: className, Namespace: namespace}, vmClass)
		if err != nil {
			return err
		}
		placementArgs.vmClasses[className] = vmClass

		if !needMinCPUFreq {
			res := vmClass.Spec.Policies.Resources
			needMinCPUFreq = !res.Requests.Cpu.IsZero() || !res.Limits.Cpu.IsZero()
		}
	}

	if needMinCPUFreq {
		freq, err := vs.getOrComputeCPUMinFrequency(ctx)
		if err != nil {
			return err
		}
		placementArgs.minCPUFreq = freq
	}

	return nil
}

func (vs *vSphereVMProvider) vmGroupGetStorageClasses(
	ctx context.Context,
	placementArgs *vmGroupPlacementArgs) error {

	for scName := range placementArgs.storageClasses {
		sc := &storagev1.StorageClass{}
		if err := vs.k8sClient.Get(ctx, client.ObjectKey{Name: scName}, sc); err != nil {
			return err
		}
		placementArgs.storageClasses[scName] = sc

		policyID, err := kubeutil.GetStoragePolicyID(*sc)
		if err != nil {
			return err
		}
		placementArgs.storageClassesToID[scName] = policyID
	}

	return nil
}

func (vs *vSphereVMProvider) vmGroupGetImages(
	ctx context.Context,
	namespace string,
	placementArgs *vmGroupPlacementArgs) error {

	for name := range placementArgs.vmImages {
		vmi := &vmopv1.VirtualMachineImage{}
		if err := vs.k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, vmi); err != nil {
			return err
		}
		placementArgs.vmImages[name] = vmi
	}

	for name := range placementArgs.cvmImages {
		cvmi := &vmopv1.ClusterVirtualMachineImage{}
		if err := vs.k8sClient.Get(ctx, client.ObjectKey{Name: name}, cvmi); err != nil {
			return err
		}
		placementArgs.cvmImages[name] = cvmi
	}

	return nil
}

func (vs *vSphereVMProvider) vmGroupGetVMSetResourcePolicy(
	ctx context.Context,
	namespace string,
	placementArgs *vmGroupPlacementArgs) error {

	name := placementArgs.vmSetResourcePolicyName
	if name == nil || *name == "" {
		return nil
	}

	vmSetResourcePolicy := &vmopv1.VirtualMachineSetResourcePolicy{}
	err := vs.k8sClient.Get(ctx, client.ObjectKey{Name: *name, Namespace: namespace}, vmSetResourcePolicy)
	if err != nil {
		return err
	}

	placementArgs.childRPName = vmSetResourcePolicy.Spec.ResourcePool.Name
	return nil
}

func (vs *vSphereVMProvider) vmGroupGenerateConfigSpecs(
	ctx context.Context,
	groupPlacements []providers.VMGroupPlacement,
	placementArgs *vmGroupPlacementArgs) error {

	for _, grpPlacement := range groupPlacements {
		for _, vm := range grpPlacement.VMMembers {
			vmClass := placementArgs.vmClasses[vm.Spec.ClassName]

			// TODO: Dedupe this more with vmCreateGenConfigSpec()

			var classConfigSpec vimtypes.VirtualMachineConfigSpec
			if rawConfigSpec := vmClass.Spec.ConfigSpec; len(rawConfigSpec) > 0 {
				cs, err := GetVMClassConfigSpec(ctx, rawConfigSpec)
				if err != nil {
					return err
				}
				classConfigSpec = cs
			} else {
				classConfigSpec = virtualmachine.ConfigSpecFromVMClassDevices(&vmClass.Spec)
			}

			imageStatus := vmopv1.VirtualMachineImageStatus{}
			if vm.Spec.Image != nil {
				switch vm.Spec.Image.Kind {
				case vmiKind:
					i := placementArgs.vmImages[vm.Spec.Image.Name]
					imageStatus = i.Status
				case cvmiKind:
					i := placementArgs.cvmImages[vm.Spec.Image.Name]
					imageStatus = i.Status
				}
			}

			configSpec := virtualmachine.CreateConfigSpec(
				ctx,
				vm,
				classConfigSpec,
				vmClass.Spec,
				imageStatus,
				placementArgs.minCPUFreq)

			placementConfigSpec, err := virtualmachine.CreateConfigSpecForPlacement(
				ctx,
				vm,
				configSpec,
				placementArgs.storageClassesToID)
			if err != nil {
				return err
			}

			placementArgs.vmConfigSpecs = append(placementArgs.vmConfigSpecs, placementConfigSpec)
		}
	}

	return nil
}

func (vs *vSphereVMProvider) vmGroupDoPlacement(
	ctx context.Context,
	namespace string,
	placementArgs *vmGroupPlacementArgs) (map[string]placement.Result, error) {

	vcClient, err := vs.getVcClient(ctx)
	if err != nil {
		return nil, err
	}

	return placement.GroupPlacement(
		ctx,
		vs.k8sClient,
		vcClient.VimClient(),
		vcClient.Finder(),
		namespace,
		placementArgs.childRPName,
		placementArgs.vmConfigSpecs,
	)
}

func applyPlacementResultsToGroups(
	groupPlacements []providers.VMGroupPlacement,
	results map[string]placement.Result) error {

	for _, grpPlacement := range groupPlacements {
		vmGroup := grpPlacement.VMGroup

		for _, vm := range grpPlacement.VMMembers {
			result, ok := results[vm.Name]
			if !ok {
				return fmt.Errorf("no placement result for VM %s in group %s", vm.Name, vmGroup.Name)
			}

			idx := findVMMemberStatus(vm.Name, vmGroup.Status.Members)
			if idx < 0 {
				m := vmopv1.VirtualMachineGroupMemberStatus{
					Name: vm.Name,
					Kind: "VirtualMachine",
				}
				vmGroup.Status.Members = append(vmGroup.Status.Members, m)
				idx = len(vmGroup.Status.Members) - 1
			}

			vmGroup.Status.Members[idx].Placement = placeResultToGroupMemberPlacement(vm.Name, &result)
			// TODO: Clear this on failure for the root group
			pkgcond.MarkTrue(&vmGroup.Status.Members[idx], vmopv1.VirtualMachineGroupMemberConditionPlacementReady)
		}
	}

	return nil
}

func findVMMemberStatus(vmName string, members []vmopv1.VirtualMachineGroupMemberStatus) int {
	for i := range members {
		if members[i].Name == vmName && members[i].Kind == "VirtualMachine" {
			return i
		}
	}
	return -1
}

func placeResultToGroupMemberPlacement(
	vmName string,
	result *placement.Result) *vmopv1.VirtualMachinePlacementStatus {

	placementStatus := &vmopv1.VirtualMachinePlacementStatus{}
	placementStatus.Name = vmName
	placementStatus.Zone = result.ZoneName
	placementStatus.Pool = result.PoolMoRef.Value

	if result.HostMoRef != nil {
		placementStatus.Node = result.HostMoRef.Value
	}

	for _, ds := range result.Datastores {
		status := vmopv1.VirtualMachineGroupPlacementDatastoreStatus{
			Name:                 ds.Name,
			ID:                   ds.MoRef.Value,
			URL:                  ds.URL,
			SupportedDiskFormats: ds.DiskFormats,
		}
		if ds.ForDisk {
			status.DiskKey = ptr.To(ds.DiskKey)
		}

		placementStatus.Datastores = append(placementStatus.Datastores, status)
	}

	return placementStatus
}
