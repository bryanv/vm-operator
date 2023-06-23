// Copyright (c) 2022 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package context

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"

	imgregv1a1 "github.com/vmware-tanzu/vm-operator/external/image-registry/api/v1alpha1"
)

// VirtualMachinePublishRequestContext is the context used for VirtualMachinePublishRequestControllers.
type VirtualMachinePublishRequestContext struct {
	context.Context
	Logger           logr.Logger
	VMPublishRequest *vmopv1.VirtualMachinePublishRequest
	VM               *vmopv1.VirtualMachine
	ContentLibrary   *imgregv1a1.ContentLibrary
	ItemID           string
	// SkipPatch indicates whether we should skip patching the object after reconcile
	// because Status is updated separately in the publishing case due to CL API limitations.
	SkipPatch bool
}

func (v *VirtualMachinePublishRequestContext) String() string {
	return fmt.Sprintf("%s %s/%s", v.VMPublishRequest.GroupVersionKind(), v.VMPublishRequest.Namespace, v.VMPublishRequest.Name)
}
