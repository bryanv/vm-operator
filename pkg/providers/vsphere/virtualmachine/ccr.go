// Copyright (c) 2022 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/object"
)

// GetVMResourcePoolAndCCR returns the VM's ResourcePool and ClusterComputeResource.
func GetVMResourcePoolAndCCR(
	ctx context.Context,
	vcVM *object.VirtualMachine) (*object.ResourcePool, *object.ClusterComputeResource, error) {

	rp, err := vcVM.ResourcePool(ctx)
	if err != nil {
		return nil, nil, err
	}

	ccrRef, err := rp.Owner(ctx)
	if err != nil {
		return nil, nil, err
	}

	cluster, ok := ccrRef.(*object.ClusterComputeResource)
	if !ok {
		return nil, nil, fmt.Errorf("VM Owner is not a ClusterComputeResource but %T", ccrRef)
	}

	return rp, cluster, nil
}
