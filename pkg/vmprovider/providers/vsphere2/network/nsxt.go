// Copyright (c) 2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	goctx "context"
	"fmt"

	"github.com/vmware-tanzu/vm-operator/pkg/lib"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
)

// ResolveNCPBackingPostPlacement fixes up the results backing for NSX-T networks where we did not
// know the CCR until after placement. This needs to be called if CreateAndWaitForNetworkInterfaces()
// was called with a nil clusterMoRef.
func ResolveNCPBackingPostPlacement(
	ctx goctx.Context,
	vimClient *vim25.Client,
	clusterMoRef vimtypes.ManagedObjectReference,
	results *NetworkInterfaceResults) error {

	if networkType := lib.GetNetworkProviderType(); networkType == "" {
		return fmt.Errorf("no network provider set")
	} else if networkType != lib.NetworkProviderTypeNSXT {
		return nil
	} else if !results.NeedCCRBacking {
		return nil
	}

	ccr := object.NewClusterComputeResource(vimClient, clusterMoRef)

	for idx := range results.Results {
		if results.Results[idx].Backing != nil {
			continue
		}

		backing, err := searchNsxtNetworkReference(ctx, ccr, results.Results[idx].NetworkID)
		if err != nil {
			return fmt.Errorf("post placement NSX-T backing fixup failed: %w", err)
		}

		results.Results[idx].Backing = backing
	}

	results.NeedCCRBacking = false
	return nil
}

// searchNsxtNetworkReference takes in NSX-T LogicalSwitchUUID and returns the reference of the network.
func searchNsxtNetworkReference(
	ctx goctx.Context,
	ccr *object.ClusterComputeResource,
	networkID string) (object.NetworkReference, error) {

	// This is more or less how the old code did it. We could save repeated work by moving this
	// into the callers since it will always be for the same CCR, but the common case is one NIC,
	// or at most a handful, so that's for later.
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

	var dvpgMoRefs []vimtypes.ManagedObjectReference
	for _, dvpg := range dvpgs {
		if dvpg.Config.LogicalSwitchUuid == networkID {
			dvpgMoRefs = append(dvpgMoRefs, dvpg.Reference())
		}
	}

	switch len(dvpgMoRefs) {
	case 1:
		return object.NewDistributedVirtualPortgroup(ccr.Client(), dvpgMoRefs[0]), nil
	case 0:
		return nil, fmt.Errorf("no DVPG with NSX-T network ID %q found", networkID)
	default:
		// The LogicalSwitchUuid is supposed to be unique per CCR, so this is likely an NCP
		// misconfiguration, and we don't know which one to pick.
		return nil, fmt.Errorf("multiple DVPGs (%d) with NSX-T network ID %q found", len(dvpgMoRefs), networkID)
	}
}
