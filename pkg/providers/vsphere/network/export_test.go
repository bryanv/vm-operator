// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ResolutionObjectForTest returns the raw provider resource retained on r.
func ResolutionObjectForTest(r Resolution) ctrlclient.Object {
	return r.obj
}

// ResolutionBackingForTest returns the resolved network backing retained on r.
func ResolutionBackingForTest(r Resolution) object.NetworkReference {
	return r.backing
}

// ObservedAdapterDeviceForTest returns the observed device retained on a.
func ObservedAdapterDeviceForTest(a ObservedAdapter) vimtypes.BaseVirtualDevice {
	return a.device
}

// InfoOwnedObjectsForTest returns the raw owned provider resources retained
// on i.
func InfoOwnedObjectsForTest(i *Info) []ctrlclient.Object {
	return i.owned
}
