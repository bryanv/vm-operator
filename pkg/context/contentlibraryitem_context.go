// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package context

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	imgregv1a1 "github.com/vmware-tanzu/image-registry-operator-api/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha3"
)

// ContentLibraryItemContext is the context used for ContentLibraryItem controller.
type ContentLibraryItemContext struct {
	context.Context
	Logger       logr.Logger
	CLItem       *imgregv1a1.ContentLibraryItem
	VMI          *vmopv1.VirtualMachineImage
	ImageObjName string
}

func (c *ContentLibraryItemContext) String() string {
	return fmt.Sprintf("%s %s/%s", c.CLItem.GroupVersionKind(), c.CLItem.Namespace, c.CLItem.Name)
}
