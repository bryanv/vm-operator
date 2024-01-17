// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package util

import "fmt"

// CNSAttachmentNameForVolume returns the name of the CnsNodeVmAttachment based
// on the VM and Volume name.
// This matches the naming used in previous code but there are situations where
// we may get a collision between VMs and Volume names. I'm not sure if there is
// an absolute way to avoid that: the same situation can happen with the
// claimName.
// Ideally, we would use GenerateName, but we lack the back-linkage to match
// Volumes and CnsNodeVmAttachment up.
// The VM webhook validate that this result will be a valid k8s name.
func CNSAttachmentNameForVolume(vmName, volumeName string) string {
	return vmName + "-" + volumeName
}

// CNSStoragePolicyQuotaName returns the name of the StorageQuotaPolicy CR based
// on the name of the storage class.
func CNSStoragePolicyQuotaName(storageClassName string) string {
	return fmt.Sprintf("%s-storagepolicyquota", storageClassName)
}
