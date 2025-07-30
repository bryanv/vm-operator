// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vsphere

import (
	"context"
	"fmt"

	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha4"
	pkgcond "github.com/vmware-tanzu/vm-operator/pkg/conditions"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/providers"
	vcclient "github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/client"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/placement"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
)

func (vs *vSphereVMProvider) PlaceVirtualMachineGroup(
	ctx context.Context,
	group *vmopv1.VirtualMachineGroup,
	groupPlacements []providers.VMGroupPlacement) error {

	ctx = context.WithValue(ctx, vimtypes.ID{}, vs.getOpID(ctx, group, "groupPlacement"))

	vcClient, err := vs.getVcClient(ctx)
	if err != nil {
		return err
	}

	configSpecs, err := vs.vmGroupGetVMPlacementConfigSpecs(ctx, vcClient, groupPlacements)
	if err != nil {
		return err
	}

	if len(configSpecs) == 0 {
		return nil
	}

	results, err := vs.vmGroupDoPlacement(ctx, vcClient, group.Namespace, configSpecs)
	if err != nil {
		return err
	}

	if err := applyPlacementResultsToGroups(results, groupPlacements); err != nil {
		return err
	}

	return nil
}

func (vs *vSphereVMProvider) vmGroupGetVMPlacementConfigSpecs(
	ctx context.Context,
	vcClient *vcclient.Client,
	groupPlacements []providers.VMGroupPlacement) ([]vimtypes.VirtualMachineConfigSpec, error) {

	var configSpecs []vimtypes.VirtualMachineConfigSpec
	// All VMs must all have the same child resource pool name or none at all since
	// placement takes a single list of resource pool candidates.
	var childResourcePoolName *string

	for _, grpPlacement := range groupPlacements {
		for _, vm := range grpPlacement.VMMembers {
			logger := pkgutil.FromContextOrDefault(ctx).WithValues(
				"childGroupName", grpPlacement.VMGroup.Name,
				"vm", vm.Name,
			)

			vmCtx := pkgctx.VirtualMachineContext{
				Context: ctx,
				Logger:  logger,
				VM:      vm,
			}

			configSpec, childRPName, err := vs.vmGroupGetVMPlacementConfigSpec(vmCtx, vcClient)
			if err != nil {
				return nil, err
			}

			if childResourcePoolName == nil {
				childResourcePoolName = &childRPName
			} else if childRPName != *childResourcePoolName {
				return nil, fmt.Errorf("all VMs being placed as group must belong to same child ResourcePool")
			}

			configSpecs = append(configSpecs, *configSpec)
		}
	}

	return configSpecs, nil
}

func (vs *vSphereVMProvider) vmGroupGetVMPlacementConfigSpec(
	vmCtx pkgctx.VirtualMachineContext,
	vcClient *vcclient.Client) (*vimtypes.VirtualMachineConfigSpec, string, error) {

	// This reuses parts of the VM controller driven create VM path to
	// generate the VM's placement ConfigSpec. Later, we should work on
	// reducing the duplication here.

	createArgs := &VMCreateArgs{}

	{
		// Partial vmCreateGetPrereqs():

		if err := vs.vmCreateGetVirtualMachineClass(vmCtx, createArgs); err != nil {
			return nil, "", err
		}

		if err := vs.vmCreateGetVirtualMachineImage(vmCtx, createArgs); err != nil {
			return nil, "", err
		}

		if err := vs.vmCreateGetSetResourcePolicy(vmCtx, createArgs); err != nil {
			return nil, "", err
		}

		if err := vs.vmCreateGetStoragePrereqs(vmCtx, vcClient, createArgs); err != nil {
			return nil, "", err
		}
	}

	err := vs.vmCreateGenConfigSpec(vmCtx, createArgs)
	if err != nil {
		return nil, "", err
	}

	{
		// Partial vmCreateDoPlacement():

		placementConfigSpec, err := virtualmachine.CreateConfigSpecForPlacement(
			vmCtx,
			vmCtx.VM,
			createArgs.ConfigSpec,
			createArgs.Storage.StorageClassToPolicyID)
		if err != nil {
			return nil, "", err
		}

		return &placementConfigSpec, createArgs.ChildResourcePoolName, nil
	}
}

func (vs *vSphereVMProvider) vmGroupDoPlacement(
	ctx context.Context,
	vcClient *vcclient.Client,
	namespace string,
	configSpecs []vimtypes.VirtualMachineConfigSpec) (map[string]placement.Result, error) {

	return placement.GroupPlacement(
		ctx,
		vs.k8sClient,
		vcClient.VimClient(),
		vcClient.Finder(),
		namespace,
		"",
		configSpecs,
	)
}

func applyPlacementResultsToGroups(
	results map[string]placement.Result,
	groupPlacements []providers.VMGroupPlacement) error {

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
