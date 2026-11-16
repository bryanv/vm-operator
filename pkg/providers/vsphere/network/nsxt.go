// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
)

const (
	// VPCInterfaceRestoredAnnotation is the annotation that NSX puts on the VM
	// when an interface is restored. The value of the annotation is the interface
	// names of the restored interfaces, like "eth0, eth2".
	VPCInterfaceRestoredAnnotation = "nsx/reconfigure-nic"
)

type nsxOpaqueBacking struct {
	vimtypes.ManagedObjectReference // Won't be set.
	logicalSwitchUID                string
}

// NSX-T/VPC provides us with the logical switch UID, not the opaque network
// MoRef, so implement a simpler version of govmomi's OpaqueNetwork here.
var _ object.NetworkReference = &nsxOpaqueBacking{}

func newNSXOpaqueNetwork(lsUID string) *nsxOpaqueBacking {
	return &nsxOpaqueBacking{
		logicalSwitchUID: lsUID,
	}
}

func (n nsxOpaqueBacking) GetInventoryPath() string {
	return ""
}

// EthernetCardBackingInfo returns the VirtualDeviceBackingInfo for this Network.
func (n nsxOpaqueBacking) EthernetCardBackingInfo(ctx context.Context) (vimtypes.BaseVirtualDeviceBackingInfo, error) {
	summary, err := n.Summary(ctx)
	if err != nil {
		return nil, err
	}

	backing := &vimtypes.VirtualEthernetCardOpaqueNetworkBackingInfo{
		OpaqueNetworkId:   summary.OpaqueNetworkId,
		OpaqueNetworkType: summary.OpaqueNetworkType,
	}

	return backing, nil
}

func (n nsxOpaqueBacking) Summary(_ context.Context) (*vimtypes.OpaqueNetworkSummary, error) {
	return &vimtypes.OpaqueNetworkSummary{
		OpaqueNetworkId:   n.logicalSwitchUID,
		OpaqueNetworkType: "nsx.LogicalSwitch",
	}, nil
}

var (
	// uuidToDVPGCache contains the cache used look up the CCR's DPVG for a
	// given LogicalSwitchUUID.
	uuidToDVPGCache sync.Map

	clearUUIDToDVPGCache sync.Once
)

func searchNsxtNetworkReference(
	ctx context.Context,
	ccr *object.ClusterComputeResource,
	networkID string) (object.NetworkReference, error) {

	clearUUIDToDVPGCache.Do(func() {
		// Start goroutine to periodically clear the cache.
		go func() {
			logger := ctrl.Log.WithName("nsx-dvpg-cache-clearer")
			ticker := time.NewTicker(time.Minute * 60)
			for range ticker.C {
				logger.Info("Clearing NSX LogicalSwitchUUID to DVPG cache")
				uuidToDVPGCache.Clear()
			}
		}()
	})

	key := ccr.Reference().Value

	if c, ok := uuidToDVPGCache.Load(key); ok {
		cc := c.(map[string]vimtypes.ManagedObjectReference)
		if dvpg, ok := cc[networkID]; ok {
			return object.NewDistributedVirtualPortgroup(ccr.Client(), dvpg), nil
		}
	}

	// On either miss - CCR or UUID not found - try to refresh the DVPGs for the CCR,
	// and always store the latest results in the cache. We could  CompareAndSwap()
	// instead but let's have the latest win.
	uuidsToDPVG, err := getDVPGsForCCR(ctx, ccr)
	if err != nil {
		return nil, err
	}
	uuidToDVPGCache.Store(key, uuidsToDPVG)

	if dvpg, ok := uuidsToDPVG[networkID]; ok {
		return object.NewDistributedVirtualPortgroup(ccr.Client(), dvpg), nil
	}

	return nil, fmt.Errorf("no DVPG with NSX network ID %q found", networkID)
}

func getDVPGsForCCR(
	ctx context.Context,
	ccr *object.ClusterComputeResource) (map[string]vimtypes.ManagedObjectReference, error) {

	var obj mo.ClusterComputeResource
	if err := ccr.Properties(ctx, ccr.Reference(), []string{"network"}, &obj); err != nil {
		return nil, err
	}

	var dvpgsMoRefs []vimtypes.ManagedObjectReference
	for _, n := range obj.Network {
		if n.Type == "DistributedVirtualPortgroup" {
			dvpgsMoRefs = append(dvpgsMoRefs, n.Reference())
		}
	}

	if len(dvpgsMoRefs) == 0 {
		return nil, nil
	}

	var dvpgs []mo.DistributedVirtualPortgroup
	err := property.DefaultCollector(ccr.Client()).Retrieve(ctx, dvpgsMoRefs, []string{"config.logicalSwitchUuid"}, &dvpgs)
	if err != nil {
		return nil, err
	}

	uuidToDPVG := make(map[string]vimtypes.ManagedObjectReference, len(dvpgs))
	for _, dvpg := range dvpgs {
		if uuid := dvpg.Config.LogicalSwitchUuid; uuid != "" {
			uuidToDPVG[uuid] = dvpg.Reference()
		}
	}

	return uuidToDPVG, nil
}

// VPCPostRestoreDeviceChanges returns the VPCPostRestoreBackingFixup device
// changes for the Ready interfaces of a network observation. Each interface
// contributes its provider's pinned MAC address and external ID, and the
// Ethernet card backing resolved for it in cardBackings; an interface without
// a resolved backing is skipped.
//
// TODO(T026): Fold VPC restore into the planner and remove this.
func VPCPostRestoreDeviceChanges(
	vmCtx pkgctx.VirtualMachineContext,
	currentEthCards []vimtypes.BaseVirtualDevice,
	info *Info,
	cardBackings map[string]vimtypes.BaseVirtualDeviceBackingInfo) ([]vimtypes.BaseVirtualDeviceConfigSpec, error) {

	if info == nil {
		return nil, nil
	}

	// VPCPostRestoreBackingFixup still takes its inputs as Devices. This
	// adapter keeps that representation inside this package.
	devices := make([]Device, 0, len(info.Interfaces))
	for _, iface := range info.Interfaces {
		r := iface.Provider
		backing := cardBackings[iface.Spec.Name]
		if r.Phase != ResolutionReady || r.Target == nil || backing == nil {
			continue
		}

		var externalID string
		if r.Target.ExternalID.Op == IntentSet {
			externalID = r.Target.ExternalID.Value
		}

		devices = append(devices, Device{
			ProviderType:  r.Ref.Type,
			InterfaceObj:  r.obj,
			MacAddress:    r.Target.MAC.Pinned,
			ExternalID:    externalID,
			InterfaceName: iface.Spec.Name,
			EthCard: &vimtypes.VirtualEthernetCard{
				VirtualDevice: vimtypes.VirtualDevice{Backing: backing},
			},
		})
	}

	return VPCPostRestoreBackingFixup(vmCtx, currentEthCards, devices)
}

func VPCPostRestoreBackingFixup(
	vmCtx pkgctx.VirtualMachineContext,
	currentEthCards []vimtypes.BaseVirtualDevice,
	devices []Device) ([]vimtypes.BaseVirtualDeviceConfigSpec, error) {

	var deviceChanges []vimtypes.BaseVirtualDeviceConfigSpec

	// Post a VPC restore, the SubnetPort will be updated with a new ExternalID and
	// LogicalSwitchUUID (NetworkID) but the MAC is supposed to stay the same. Use that
	// do an edit on existing device that have changed.
	for _, dev := range devices {
		if dev.MacAddress == "" || dev.ExternalID == "" {
			continue
		}

		// Find device by MAC address.
		for _, curDev := range currentEthCards {
			ethCard := curDev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()

			if !strings.EqualFold(ethCard.MacAddress, dev.MacAddress) {
				continue
			}

			if ethCard.ExternalId != dev.ExternalID {
				vmCtx.Logger.Info(
					"Updating network device ExternalID for restored/failed-over VM",
					"name", dev.InterfaceName,
					"macAddress", dev.MacAddress,
					"oldExternalID", ethCard.ExternalId,
					"newExternalID", dev.ExternalID,
					"oldSubnetID", ethCard.SubnetId)

				ethCard.ExternalId = dev.ExternalID
				ethCard.Backing = dev.EthCard.GetVirtualDevice().Backing
				ethCard.SubnetId = ""

				deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
					Device:    curDev,
					Operation: vimtypes.VirtualDeviceConfigSpecOperationEdit,
				})
			}
			break
		}
	}

	return deviceChanges, nil
}
