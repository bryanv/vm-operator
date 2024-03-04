// Copyright (c) 2023-2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle

import (
	goctx "context"
	"fmt"
	"net"
	"slices"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/api/v1alpha2/common"
	"github.com/vmware-tanzu/vm-operator/pkg/conditions"
	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/topology"
	"github.com/vmware-tanzu/vm-operator/pkg/util"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/virtualmachine"
)

var (
	// The minimum properties needed to be retrieved in order to populate the Status. Callers may
	// provide a MO with more. This often saves us a second round trip in the common steady state.
	vmStatusPropertiesSelector = []string{
		"config.changeTrackingEnabled",
		"config.extraConfig",
		"guest",
		"summary",
	}
)

func UpdateStatus(
	vmCtx context.VirtualMachineContextA2,
	k8sClient ctrlclient.Client,
	vcVM *object.VirtualMachine,
	vmMO *mo.VirtualMachine) error {

	vm := vmCtx.VM

	// This is implicitly true: ensure the condition is set since it is how we determine the old v1a1 Phase.
	conditions.MarkTrue(vmCtx.VM, vmopv1.VirtualMachineConditionCreated)
	// TODO: Might set other "prereq" conditions too for version conversion but we'd have to fib a little.

	if vm.Status.Image == nil {
		// If unset, we don't know if this was a cluster or namespace scoped image at create time.
		vm.Status.Image = &common.LocalObjectRef{
			Name:       vm.Spec.ImageName,
			APIVersion: vmopv1.SchemeGroupVersion.String(),
		}
	}
	if vm.Status.Class == nil {
		// In v1a2 we know this will always be the namespace scoped class since v1a2 doesn't have
		// the bindings. Our handling of this field will be more complicated once we really
		// support class changes and resizing/reconfiguring the VM the fly in response.
		vm.Status.Class = &common.LocalObjectRef{
			Kind:       "VirtualMachineClass",
			APIVersion: vmopv1.SchemeGroupVersion.String(),
			Name:       vm.Spec.ClassName,
		}
	}

	if vmMO == nil {
		// In the common case, our caller will have already gotten the MO properties in order to determine
		// if it had any reconciliation to do, and there was nothing to do since the VM is in the steady
		// state so that MO is still entirely valid here.
		// NOTE: The properties must have been retrieved with at least vmStatusPropertiesSelector.
		vmMO = &mo.VirtualMachine{}
		if err := vcVM.Properties(vmCtx, vcVM.Reference(), vmStatusPropertiesSelector, vmMO); err != nil {
			// Leave the current Status unchanged for now.
			return fmt.Errorf("failed to get VM properties for status update: %w", err)
		}
	}

	var errs []error
	var err error
	summary := vmMO.Summary

	vm.Status.PowerState = convertPowerState(summary.Runtime.PowerState)
	vm.Status.UniqueID = vcVM.Reference().Value
	vm.Status.BiosUUID = summary.Config.Uuid
	vm.Status.InstanceUUID = summary.Config.InstanceUuid
	hardwareVersion, _ := types.ParseHardwareVersion(summary.Config.HwVersion)
	vm.Status.HardwareVersion = int32(hardwareVersion)
	vm.Status.Network = getGuestNetworkStatus(vmCtx, vmMO.Guest)

	vm.Status.Host, err = getRuntimeHostHostname(vmCtx, vcVM, summary.Runtime.Host)
	if err != nil {
		errs = append(errs, err)
	}

	MarkVMToolsRunningStatusCondition(vmCtx.VM, vmMO.Guest)
	MarkCustomizationInfoCondition(vmCtx.VM, vmMO.Guest)
	MarkBootstrapCondition(vmCtx.VM, vmMO.Config)

	if config := vmMO.Config; config != nil {
		vm.Status.ChangeBlockTracking = config.ChangeTrackingEnabled
	} else {
		vm.Status.ChangeBlockTracking = nil
	}

	zoneName := vm.Labels[topology.KubernetesTopologyZoneLabelKey]
	if zoneName == "" {
		cluster, err := virtualmachine.GetVMClusterComputeResource(vmCtx, vcVM)
		if err != nil {
			errs = append(errs, err)
		} else {
			zoneName, err = topology.LookupZoneForClusterMoID(vmCtx, k8sClient, cluster.Reference().Value)
			if err != nil {
				errs = append(errs, err)
			} else {
				if vm.Labels == nil {
					vm.Labels = map[string]string{}
				}
				vm.Labels[topology.KubernetesTopologyZoneLabelKey] = zoneName
			}
		}
	}

	if zoneName != "" {
		vm.Status.Zone = zoneName
	}

	return k8serrors.NewAggregate(errs)
}

func getRuntimeHostHostname(
	ctx goctx.Context,
	vcVM *object.VirtualMachine,
	host *types.ManagedObjectReference) (string, error) {

	if host != nil {
		return object.NewHostSystem(vcVM.Client(), *host).ObjectName(ctx)
	}
	return "", nil
}

func getGuestNetworkStatus(
	vmCtx context.VirtualMachineContextA2,
	guestInfo *types.GuestInfo,
) *vmopv1.VirtualMachineNetworkStatus {

	if guestInfo == nil {
		return nil
	}

	status := &vmopv1.VirtualMachineNetworkStatus{}

	if ipAddr := guestInfo.IpAddress; ipAddr != "" {
		// TODO: Filter out local addresses.
		if net.ParseIP(ipAddr).To4() != nil {
			status.PrimaryIP4 = ipAddr
		} else {
			status.PrimaryIP6 = ipAddr
		}
	}

	if len(guestInfo.Net) > 0 {
		var networkInterfaces []vmopv1.VirtualMachineNetworkInterfaceSpec
		if vmCtx.VM.Spec.Network != nil {
			networkInterfaces = vmCtx.VM.Spec.Network.Interfaces
		}

		slices.SortFunc(guestInfo.Net, func(a, b types.GuestNicInfo) int {
			// Sort by the DeviceKey (DeviceConfigId) to order the guest info list by the
			// order in the initial ConfigSpec which is the order of the []networkInterfaces.
			// since it is immutable.
			return int(a.DeviceConfigId - b.DeviceConfigId)
		})

		networkInterfaceIdx := 0
		for i := range guestInfo.Net {
			deviceKey := guestInfo.Net[i].DeviceConfigId

			// Skip pseudo devices.
			if deviceKey < 0 {
				continue
			}

			var name string
			if networkInterfaceIdx < len(networkInterfaces) {
				name = networkInterfaces[networkInterfaceIdx].Name
				networkInterfaceIdx++
			}

			status.Interfaces = append(status.Interfaces, guestNicInfoToInterfaceStatus(name, deviceKey, &guestInfo.Net[i]))
		}
	}

	if len(guestInfo.IpStack) > 0 {
		status.VirtualMachineNetworkIPStackStatus = guestIPStackInfoToIPStackStatus(&guestInfo.IpStack[0])
	}

	return status
}

func guestNicInfoToInterfaceStatus(
	name string,
	deviceKey int32,
	guestNicInfo *types.GuestNicInfo) vmopv1.VirtualMachineNetworkInterfaceStatus {

	status := vmopv1.VirtualMachineNetworkInterfaceStatus{
		Name:      name,
		DeviceKey: deviceKey,
	}

	if guestNicInfo.MacAddress != "" {
		status.IP = &vmopv1.VirtualMachineNetworkInterfaceIPStatus{
			MACAddr: guestNicInfo.MacAddress,
		}
	}

	if guestIPConfig := guestNicInfo.IpConfig; guestIPConfig != nil {
		if status.IP == nil {
			status.IP = &vmopv1.VirtualMachineNetworkInterfaceIPStatus{}
		}

		status.IP.AutoConfigurationEnabled = guestIPConfig.AutoConfigurationEnabled
		status.IP.Addresses = convertNetIPConfigInfoIPAddresses(guestIPConfig.IpAddress)

		if guestIPConfig.Dhcp != nil {
			status.IP.DHCP = convertNetDhcpConfigInfo(guestIPConfig.Dhcp)
		}
	}

	if dnsConfig := guestNicInfo.DnsConfig; dnsConfig != nil {
		status.DNS = convertNetDNSConfigInfo(dnsConfig)
	}

	return status
}

func guestIPStackInfoToIPStackStatus(guestIPStack *types.GuestStackInfo) vmopv1.VirtualMachineNetworkIPStackStatus {
	status := vmopv1.VirtualMachineNetworkIPStackStatus{}

	if dhcpConfig := guestIPStack.DhcpConfig; dhcpConfig != nil {
		status.DHCP = convertNetDhcpConfigInfo(dhcpConfig)
	}

	if dnsConfig := guestIPStack.DnsConfig; dnsConfig != nil {
		status.DNS = convertNetDNSConfigInfo(dnsConfig)
	}

	if ipRouteConfig := guestIPStack.IpRouteConfig; ipRouteConfig != nil {
		status.IPRoutes = convertNetIPRouteConfigInfo(ipRouteConfig)
	}

	status.KernelConfig = convertKeyValueSlice(guestIPStack.IpStackConfig)

	return status
}

func convertPowerState(powerState types.VirtualMachinePowerState) vmopv1.VirtualMachinePowerState {
	switch powerState {
	case types.VirtualMachinePowerStatePoweredOff:
		return vmopv1.VirtualMachinePowerStateOff
	case types.VirtualMachinePowerStatePoweredOn:
		return vmopv1.VirtualMachinePowerStateOn
	case types.VirtualMachinePowerStateSuspended:
		return vmopv1.VirtualMachinePowerStateSuspended
	}
	return ""
}

func convertNetIPConfigInfoIPAddresses(ipAddresses []types.NetIpConfigInfoIpAddress) []vmopv1.VirtualMachineNetworkInterfaceIPAddrStatus {
	if len(ipAddresses) == 0 {
		return nil
	}

	out := make([]vmopv1.VirtualMachineNetworkInterfaceIPAddrStatus, 0, len(ipAddresses))
	for _, guestIPAddr := range ipAddresses {
		ipAddrStatus := vmopv1.VirtualMachineNetworkInterfaceIPAddrStatus{
			Address: guestIPAddr.IpAddress,
			Origin:  guestIPAddr.Origin,
			State:   guestIPAddr.State,
		}
		if guestIPAddr.Lifetime != nil {
			ipAddrStatus.Lifetime = metav1.NewTime(*guestIPAddr.Lifetime)
		}

		out = append(out, ipAddrStatus)
	}
	return out
}

func convertNetDNSConfigInfo(dnsConfig *types.NetDnsConfigInfo) *vmopv1.VirtualMachineNetworkDNSStatus {
	return &vmopv1.VirtualMachineNetworkDNSStatus{
		DHCP:          dnsConfig.Dhcp,
		DomainName:    dnsConfig.DomainName,
		HostName:      dnsConfig.HostName,
		Nameservers:   dnsConfig.IpAddress,
		SearchDomains: dnsConfig.SearchDomain,
	}
}

func convertNetDhcpConfigInfo(dhcpConfig *types.NetDhcpConfigInfo) *vmopv1.VirtualMachineNetworkDHCPStatus {
	if ipv4, ipv6 := dhcpConfig.Ipv4, dhcpConfig.Ipv6; ipv4 != nil || ipv6 != nil {
		status := &vmopv1.VirtualMachineNetworkDHCPStatus{}

		if ipv4 != nil {
			status.IP4.Enabled = ipv4.Enable
			status.IP4.Config = convertKeyValueSlice(ipv4.Config)
		}
		if ipv6 != nil {
			status.IP6.Enabled = ipv6.Enable
			status.IP6.Config = convertKeyValueSlice(ipv6.Config)
		}

		return status
	}

	return nil
}

func convertNetIPRouteConfigInfo(routeConfig *types.NetIpRouteConfigInfo) []vmopv1.VirtualMachineNetworkIPRouteStatus {
	if len(routeConfig.IpRoute) == 0 {
		return nil
	}

	// Try to skip routes that are likely not interesting or useful to external users - especially on
	// TKG nodes - that would otherwise just clutter the Status output.
	skipRoute := func(ipRoute types.NetIpRouteConfigInfoIpRoute) bool {
		network, prefix := ipRoute.Network, ipRoute.PrefixLength

		ip := net.ParseIP(network)
		if ip == nil {
			return true
		}

		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true
		}

		if ip.To4() != nil {
			return prefix == 32
		}

		return ip.To16() == nil || ip.IsInterfaceLocalMulticast() || ip.IsMulticast()
	}

	out := make([]vmopv1.VirtualMachineNetworkIPRouteStatus, 0, 1)
	for _, ipRoute := range routeConfig.IpRoute {
		if skipRoute(ipRoute) {
			continue
		}

		out = append(out, vmopv1.VirtualMachineNetworkIPRouteStatus{
			Gateway: vmopv1.VirtualMachineNetworkIPRouteGatewayStatus{
				Device:  ipRoute.Gateway.Device,
				Address: ipRoute.Gateway.IpAddress,
			},
			NetworkAddress: fmt.Sprintf("%s/%d", ipRoute.Network, ipRoute.PrefixLength),
		})
	}
	return out
}

func convertKeyValueSlice(s []types.KeyValue) []common.KeyValuePair {
	if len(s) == 0 {
		return nil
	}

	out := make([]common.KeyValuePair, 0, len(s))
	for i := range s {
		out = append(out, common.KeyValuePair{Key: s[i].Key, Value: s[i].Value})
	}
	return out
}

func MarkVMToolsRunningStatusCondition(
	vm *vmopv1.VirtualMachine,
	guestInfo *types.GuestInfo) {

	if guestInfo == nil || guestInfo.ToolsRunningStatus == "" {
		conditions.MarkUnknown(vm, vmopv1.VirtualMachineToolsCondition, "NoGuestInfo", "")
		return
	}

	switch guestInfo.ToolsRunningStatus {
	case string(types.VirtualMachineToolsRunningStatusGuestToolsNotRunning):
		msg := "VMware Tools is not running"
		conditions.MarkFalse(vm, vmopv1.VirtualMachineToolsCondition, vmopv1.VirtualMachineToolsNotRunningReason, msg)
	case string(types.VirtualMachineToolsRunningStatusGuestToolsRunning), string(types.VirtualMachineToolsRunningStatusGuestToolsExecutingScripts):
		conditions.MarkTrue(vm, vmopv1.VirtualMachineToolsCondition)
	default:
		msg := "Unexpected VMware Tools running status"
		conditions.MarkUnknown(vm, vmopv1.VirtualMachineToolsCondition, "Unknown", msg)
	}
}

func MarkCustomizationInfoCondition(vm *vmopv1.VirtualMachine, guestInfo *types.GuestInfo) {
	if guestInfo == nil || guestInfo.CustomizationInfo == nil {
		conditions.MarkUnknown(vm, vmopv1.GuestCustomizationCondition, "NoGuestInfo", "")
		return
	}

	switch guestInfo.CustomizationInfo.CustomizationStatus {
	case string(types.GuestInfoCustomizationStatusTOOLSDEPLOYPKG_IDLE), "":
		conditions.MarkTrue(vm, vmopv1.GuestCustomizationCondition)
	case string(types.GuestInfoCustomizationStatusTOOLSDEPLOYPKG_PENDING):
		conditions.MarkFalse(vm, vmopv1.GuestCustomizationCondition, vmopv1.GuestCustomizationPendingReason, "")
	case string(types.GuestInfoCustomizationStatusTOOLSDEPLOYPKG_RUNNING):
		conditions.MarkFalse(vm, vmopv1.GuestCustomizationCondition, vmopv1.GuestCustomizationRunningReason, "")
	case string(types.GuestInfoCustomizationStatusTOOLSDEPLOYPKG_SUCCEEDED):
		conditions.MarkTrue(vm, vmopv1.GuestCustomizationCondition)
	case string(types.GuestInfoCustomizationStatusTOOLSDEPLOYPKG_FAILED):
		errorMsg := guestInfo.CustomizationInfo.ErrorMsg
		if errorMsg == "" {
			errorMsg = "vSphere VM Customization failed due to an unknown error."
		}
		conditions.MarkFalse(vm, vmopv1.GuestCustomizationCondition, vmopv1.GuestCustomizationFailedReason, errorMsg)
	default:
		errorMsg := guestInfo.CustomizationInfo.ErrorMsg
		if errorMsg == "" {
			errorMsg = "Unexpected VM Customization status"
		}
		conditions.MarkFalse(vm, vmopv1.GuestCustomizationCondition, "Unknown", errorMsg)
	}
}

func MarkBootstrapCondition(
	vm *vmopv1.VirtualMachine,
	configInfo *types.VirtualMachineConfigInfo) {

	if configInfo == nil {
		conditions.MarkUnknown(
			vm, vmopv1.GuestBootstrapCondition, "NoConfigInfo", "")
		return
	}

	if len(configInfo.ExtraConfig) == 0 {
		conditions.MarkUnknown(
			vm, vmopv1.GuestBootstrapCondition, "NoExtraConfig", "")
		return
	}

	status, reason, msg, ok := util.GetBootstrapConditionValues(configInfo)
	if !ok {
		conditions.MarkUnknown(
			vm, vmopv1.GuestBootstrapCondition, "NoBootstrapStatus", "")
		return
	}
	if status {
		c := conditions.TrueCondition(vmopv1.GuestBootstrapCondition)
		if reason != "" {
			c.Reason = reason
		}
		c.Message = msg
		conditions.Set(vm, c)
	} else {
		conditions.MarkFalse(vm, vmopv1.GuestBootstrapCondition, reason, msg)
	}
}
