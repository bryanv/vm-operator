// Copyright (c) 2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle

import (
	"github.com/vmware/govmomi/object"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/vm-operator/pkg/context"
)

type UpdateArgs struct {
}

func UpdateVirtualMachine(
	vmCtx context.VirtualMachineContextA2,
	client ctrlclient.Client,
	vcVM *object.VirtualMachine) error {

	return nil
}
