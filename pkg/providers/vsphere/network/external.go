// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/constants"
)

// newExternalNetworkInterfaceObject returns an empty object for the network
// interface CR type selected by the interface's ExternalRef.
func newExternalNetworkInterfaceObject(
	group string,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec) (ctrlclient.Object, error) {

	kind := interfaceSpec.ExternalRef.Kind

	switch {
	case group == netopv1alpha1.SchemeGroupVersion.Group && kind == "NetworkInterface":
		return &netopv1alpha1.NetworkInterface{}, nil
	case group == ncpv1alpha1.SchemeGroupVersion.Group && kind == "VirtualNetworkInterface":
		return &ncpv1alpha1.VirtualNetworkInterface{}, nil
	case group == vpcv1alpha1.SchemeGroupVersion.Group && kind == "SubnetPort":
		return &vpcv1alpha1.SubnetPort{}, nil
	default:
		return nil, fmt.Errorf("unsupported external network interface %s %s",
			interfaceSpec.ExternalRef.APIVersion, kind)
	}
}

// getExternalNetworkInterface gets the pre-created network interface CR
// referenced by the interface's ExternalRef. VM Operator does not create,
// own, or delete this CR. For VPC, the VM association metadata required by
// NSX is set on the SubnetPort.
func getExternalNetworkInterface(
	ctx context.Context,
	vm *vmopv1.VirtualMachine,
	client ctrlclient.Client,
	group string,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec) (ctrlclient.Object, error) {

	if !pkgcfg.FromContext(ctx).Features.BringYourOwnNetworkInterface {
		return nil, fmt.Errorf("external network interfaces are not enabled")
	}

	obj, err := newExternalNetworkInterfaceObject(group, interfaceSpec)
	if err != nil {
		return nil, err
	}

	key := ctrlclient.ObjectKey{Namespace: vm.Namespace, Name: interfaceSpec.ExternalRef.Name}
	if err := client.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("failed to get external network interface %s: %w", key.Name, err)
	}

	if !obj.GetDeletionTimestamp().IsZero() {
		return nil, fmt.Errorf("external network interface %s is being deleted", key.Name)
	}

	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "VirtualMachine" {
			return nil, fmt.Errorf("external network interface %s is owned by VirtualMachine %s", key.Name, ref.Name)
		}
	}

	if subnetPort, ok := obj.(*vpcv1alpha1.SubnetPort); ok {
		if err := associateExternalSubnetPort(ctx, vm, client, subnetPort, interfaceSpec.Name); err != nil {
			return nil, err
		}
	}

	return obj, nil
}

func getExternalNetworkInterfaceDevice(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
	vimClient *vim25.Client,
	clusterMoRef *vimtypes.ManagedObjectReference,
	group string,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec) (Device, error) {

	obj, err := getExternalNetworkInterface(vmCtx, vmCtx.VM, client, group, interfaceSpec)
	if err != nil {
		return Device{}, err
	}

	switch netIf := obj.(type) {
	case *netopv1alpha1.NetworkInterface:
		return getNetOPNetworkInterfaceDevice(vmCtx, vimClient, netIf, clusterMoRef, interfaceSpec)
	case *ncpv1alpha1.VirtualNetworkInterface:
		return getNCPNetworkInterfaceDevice(vmCtx, vimClient, clusterMoRef, netIf, interfaceSpec)
	case *vpcv1alpha1.SubnetPort:
		return getVPCSubnetPortDevice(vmCtx, vimClient, clusterMoRef, netIf, interfaceSpec)
	default:
		return Device{}, fmt.Errorf("unsupported network interface CR type: %T", obj)
	}
}

func vpcAttachmentRef(vm *vmopv1.VirtualMachine, interfaceName string) string {
	return "virtualmachine/" + vm.Name + "/" + interfaceName
}

// associateExternalSubnetPort sets the VM association labels and attachment
// annotation on an external SubnetPort.
func associateExternalSubnetPort(
	ctx context.Context,
	vm *vmopv1.VirtualMachine,
	client ctrlclient.Client,
	subnetPort *vpcv1alpha1.SubnetPort,
	interfaceName string) error {

	if vmName := subnetPort.Labels[VMNameLabel]; vmName != "" && vmName != vm.Name {
		return fmt.Errorf("external SubnetPort %s is associated with VM %s", subnetPort.Name, vmName)
	}

	attachmentRef := vpcAttachmentRef(vm, interfaceName)
	if ref := subnetPort.Annotations[pkgconst.VPCAttachmentRef]; ref != "" &&
		!strings.HasPrefix(ref, vpcAttachmentRef(vm, "")) {
		return fmt.Errorf("external SubnetPort %s is attached to %s", subnetPort.Name, ref)
	}

	if subnetPort.Labels[VMNameLabel] == vm.Name &&
		subnetPort.Labels[VMInterfaceNameLabel] == interfaceName &&
		subnetPort.Annotations[pkgconst.VPCAttachmentRef] == attachmentRef {
		return nil
	}

	base := subnetPort.DeepCopy()
	if subnetPort.Labels == nil {
		subnetPort.Labels = map[string]string{}
	}
	if subnetPort.Annotations == nil {
		subnetPort.Annotations = map[string]string{}
	}
	subnetPort.Labels[VMNameLabel] = vm.Name
	subnetPort.Labels[VMInterfaceNameLabel] = interfaceName
	subnetPort.Annotations[pkgconst.VPCAttachmentRef] = attachmentRef

	if err := client.Patch(ctx, subnetPort, ctrlclient.MergeFrom(base)); err != nil {
		return fmt.Errorf("failed to associate external SubnetPort %s: %w", subnetPort.Name, err)
	}

	return nil
}

// ReleaseExternalSubnetPorts removes the VM association metadata from the
// external SubnetPorts that are no longer referenced by the VM's spec and are
// not attached to any of the VM's current ethernet cards.
func ReleaseExternalSubnetPorts(
	ctx context.Context,
	vm *vmopv1.VirtualMachine,
	client ctrlclient.Client,
	currentEthCards object.VirtualDeviceList) error {

	referenced := map[string]struct{}{}
	if vm.Spec.Network != nil && !vm.Spec.Network.Disabled {
		for _, interfaceSpec := range vm.Spec.Network.Interfaces {
			if ref := interfaceSpec.ExternalRef; ref != nil && ref.Kind == "SubnetPort" {
				referenced[ref.Name] = struct{}{}
			}
		}
	}

	return releaseExternalSubnetPorts(ctx, vm, client, func(subnetPort *vpcv1alpha1.SubnetPort) bool {
		if _, ok := referenced[subnetPort.Name]; ok {
			return false
		}
		return subnetPort.Status.Attachment.ID == "" ||
			findMatchingEthCardVPCSubnetPort(subnetPort, currentEthCards) < 0
	})
}

// ReleaseAllExternalSubnetPorts removes the VM association metadata from all
// external SubnetPorts associated with the VM. This is used once the VM has
// been deleted.
func ReleaseAllExternalSubnetPorts(
	ctx context.Context,
	vm *vmopv1.VirtualMachine,
	client ctrlclient.Client) error {

	return releaseExternalSubnetPorts(ctx, vm, client, func(*vpcv1alpha1.SubnetPort) bool {
		return true
	})
}

func releaseExternalSubnetPorts(
	ctx context.Context,
	vm *vmopv1.VirtualMachine,
	client ctrlclient.Client,
	shouldRelease func(*vpcv1alpha1.SubnetPort) bool) error {

	if !client.Scheme().Recognizes(vpcv1alpha1.SchemeGroupVersion.WithKind("SubnetPortList")) {
		return nil
	}

	var list vpcv1alpha1.SubnetPortList
	if err := client.List(ctx, &list,
		ctrlclient.InNamespace(vm.Namespace),
		ctrlclient.MatchingLabels{VMNameLabel: vm.Name}); err != nil {

		if meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("failed to list external SubnetPorts: %w", err)
	}

	for i := range list.Items {
		subnetPort := &list.Items[i]

		// SubnetPorts owned by the VM are managed by VM Operator and are
		// deleted with the VM.
		if isOwnedBy(vm, subnetPort) || !shouldRelease(subnetPort) {
			continue
		}

		base := subnetPort.DeepCopy()
		delete(subnetPort.Labels, VMNameLabel)
		delete(subnetPort.Labels, VMInterfaceNameLabel)
		delete(subnetPort.Annotations, pkgconst.VPCAttachmentRef)

		if err := client.Patch(ctx, subnetPort, ctrlclient.MergeFrom(base)); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to release external SubnetPort %s: %w", subnetPort.Name, err)
		}
	}

	return nil
}
