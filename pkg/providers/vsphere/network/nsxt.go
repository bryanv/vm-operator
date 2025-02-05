// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
)

// ResolveBackingPostPlacement fixes up the backings where we did not know the CCR until after
// placement. This should be called if CreateAndWaitForNetworkInterfaces() was called with a nil
// clusterMoRef. Returns true if a backing was resolved, so the ConfigSpec needs to be updated.
func ResolveBackingPostPlacement(
	ctx context.Context,
	vimClient *vim25.Client,
	clusterMoRef vimtypes.ManagedObjectReference,
	results *NetworkInterfaceResults) (bool, error) {

	if len(results.Results) == 0 {
		return false, nil
	}

	var typeStr string
	switch pkgcfg.FromContext(ctx).NetworkProviderType {
	case pkgcfg.NetworkProviderTypeNSXT:
		typeStr = "NSX"
	case pkgcfg.NetworkProviderTypeVPC:
		typeStr = "NSX-VPC"
	case "":
		return false, fmt.Errorf("no network provider set")
	default:
		return false, fmt.Errorf("only NSX networks are expected to need post placement backing fixup")
	}

	ccr := object.NewClusterComputeResource(vimClient, clusterMoRef)
	fixedUp := false

	var dvpgs *[]mo.DistributedVirtualPortgroup
	for idx := range results.Results {
		if results.Results[idx].Backing != nil {
			continue
		}

		if dvpgs == nil {
			d, err := getDVGPs(ctx, ccr)
			if err != nil {
				return false, err
			}
			dvpgs = &d
		}

		backing, err := searchNsxtNetworkReference(*dvpgs, ccr.Client(), results.Results[idx].NetworkID)
		if err != nil {
			return false, fmt.Errorf("post placement %s backing fixup failed: %w", typeStr, err)
		}

		fixedUp = true
		results.Results[idx].Backing = backing
	}

	return fixedUp, nil
}

func getDVGPs(
	ctx context.Context,
	ccr *object.ClusterComputeResource,
) ([]mo.DistributedVirtualPortgroup, error) {

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
		return nil, fmt.Errorf("ClusterComputeResource %s has no DVPGs", ccr.Reference().Value)
	}

	var dvpgs []mo.DistributedVirtualPortgroup
	err := property.DefaultCollector(ccr.Client()).Retrieve(ctx, dvpgsMoRefs, []string{"config.logicalSwitchUuid"}, &dvpgs)
	if err != nil {
		return nil, err
	}

	return dvpgs, nil
}

// searchNsxtNetworkReference takes in NSX-T LogicalSwitchUUID and returns the reference of the network.
func searchNsxtNetworkReference(
	dvpgs []mo.DistributedVirtualPortgroup,
	client *vim25.Client,
	networkID string) (object.NetworkReference, error) {

	var dvpgMoRefs []vimtypes.ManagedObjectReference
	for _, dvpg := range dvpgs {
		if dvpg.Config.LogicalSwitchUuid == networkID {
			dvpgMoRefs = append(dvpgMoRefs, dvpg.Reference())
		}
	}

	switch len(dvpgMoRefs) {
	case 1:
		return object.NewDistributedVirtualPortgroup(client, dvpgMoRefs[0]), nil
	case 0:
		return nil, fmt.Errorf("no DVPG with NSX network ID %q found", networkID)
	default:
		// The LogicalSwitchUuid is supposed to be unique per CCR, so this is likely an NCP
		// misconfiguration, and we don't know which one to pick.
		return nil, fmt.Errorf("multiple DVPGs (%d) with NSX network ID %q found", len(dvpgMoRefs), networkID)
	}
}
