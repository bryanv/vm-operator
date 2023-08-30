// Copyright (c) 2022 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle

import (
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/virtualmachine"
)

// PowerVirtualMachineNotOn handles when the VM's desired state is one of the not on states.
func PowerVirtualMachineNotOn(
	vmCtx context.VirtualMachineContextA2,
	vcVM *object.VirtualMachine) (*mo.VirtualMachine, error) {

	vmMO := &mo.VirtualMachine{}
	if err := vcVM.Properties(vmCtx, vcVM.Reference(), vmStatusPropertiesSelector, vmMO); err != nil {
		return nil, err
	}

	var err error
	stateChange := false
	currentPowerState := vmMO.Summary.Runtime.PowerState
	desiredPowerState := vmCtx.VM.Spec.PowerState

	switch desiredPowerState {
	case vmopv1.VirtualMachinePowerStateOff:
		stateChange = currentPowerState != types.VirtualMachinePowerStatePoweredOff
		if stateChange {
			err = virtualmachine.ChangePowerState(vmCtx, vcVM, types.VirtualMachinePowerStatePoweredOff)
		}
	case vmopv1.VirtualMachinePowerStateSuspended:
		stateChange = currentPowerState != types.VirtualMachinePowerStateSuspended
		if stateChange {
			err = virtualmachine.ChangePowerState(vmCtx, vcVM, types.VirtualMachinePowerStateSuspended)
		}
	default:
		vmCtx.Logger.Error(nil, "VM has unexpected desired 'not on' PowerState",
			"desiredPowerState", desiredPowerState, "currentPowerState", currentPowerState)
	}

	if stateChange || err != nil {
		// MO properties may be stale: force properties refetch for Status update.
		return nil, err
	}

	return vmMO, nil
}
