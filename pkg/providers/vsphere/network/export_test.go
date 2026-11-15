// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"k8s.io/apimachinery/pkg/types"
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

// NewObservedAdapterForTest normalizes dev the way Observe does.
func NewObservedAdapterForTest(dev vimtypes.BaseVirtualDevice) ObservedAdapter {
	a, _ := observeAdapter(dev)
	return a
}

// SetInfoOwnedObjectsForTest sets the raw owned provider resources on i.
func SetInfoOwnedObjectsForTest(i *Info, objs []ctrlclient.Object) {
	i.owned = objs
}

// SetResolutionObjectForTest sets the raw provider resource retained on r.
func SetResolutionObjectForTest(r *Resolution, obj ctrlclient.Object) {
	r.obj = obj
}

// SetResolutionBackingForTest sets the resolved network backing retained on
// r.
func SetResolutionBackingForTest(r *Resolution, backing object.NetworkReference) {
	r.backing = backing
}

// SetAdapterRecordInterfaceForTest replaces the operation record lookup and
// returns a function that restores it.
func SetAdapterRecordInterfaceForTest(
	fn func(a ObservedAdapter, vmUID types.UID) (string, bool)) func() {

	saved := adapterRecordInterface
	adapterRecordInterface = fn
	return func() { adapterRecordInterface = saved }
}
