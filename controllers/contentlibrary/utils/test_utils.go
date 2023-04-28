// Copyright (c) 2022-2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crtlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/api/v1alpha2/common"
	imgregv1a1 "github.com/vmware-tanzu/vm-operator/external/image-registry/api/v1alpha1"
)

// ContentLibraryServiceTypeLabelKey is used to differentiate a TKG resource from a VM service resource.
const ContentLibraryServiceTypeLabelKey = "type.services.vmware.com/tkg"

func DummyClusterContentLibraryItem(name string) *imgregv1a1.ClusterContentLibraryItem {
	cclItem := &imgregv1a1.ClusterContentLibraryItem{
		TypeMeta: metav1.TypeMeta{
			Kind:       ClusterContentLibraryItemKind,
			APIVersion: imgregv1a1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				ContentLibraryServiceTypeLabelKey: "",
				"dummy-not-service-label":         "",
			},
		},
		Spec: imgregv1a1.ContentLibraryItemSpec{
			UUID: "dummy-ccl-item-uuid",
		},
		Status: imgregv1a1.ContentLibraryItemStatus{
			Type:           imgregv1a1.ContentLibraryItemTypeOvf,
			Name:           "dummy-image-name",
			ContentVersion: "dummy-content-version",
			ContentLibraryRef: &imgregv1a1.NameAndKindRef{
				Kind: ClusterContentLibraryKind,
				Name: "dummy-ccl-name",
			},
			Conditions: []imgregv1a1.Condition{
				{
					Type:   imgregv1a1.ReadyCondition,
					Status: corev1.ConditionTrue,
				},
			},
			SecurityCompliance: &[]bool{true}[0],
		},
	}

	return cclItem
}

func DummyContentLibraryItem(name, namespace string) *imgregv1a1.ContentLibraryItem {
	clItem := &imgregv1a1.ContentLibraryItem{
		TypeMeta: metav1.TypeMeta{
			Kind:       ContentLibraryItemKind,
			APIVersion: imgregv1a1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: imgregv1a1.ContentLibraryItemSpec{
			UUID: "dummy-cl-item-uuid",
		},
		Status: imgregv1a1.ContentLibraryItemStatus{
			Type:           imgregv1a1.ContentLibraryItemTypeOvf,
			Name:           "dummy-image-name",
			ContentVersion: "dummy-content-version",
			ContentLibraryRef: &imgregv1a1.NameAndKindRef{
				Kind: ContentLibraryKind,
				Name: "cl-dummy",
			},
			Conditions: []imgregv1a1.Condition{
				{
					Type:   imgregv1a1.ReadyCondition,
					Status: corev1.ConditionTrue,
				},
			},
			SecurityCompliance: &[]bool{true}[0],
		},
	}

	return clItem
}

func GetTestVMINameFrom(clItemName string) string {
	return strings.Replace(clItemName, ItemFieldNamePrefix, ImageFieldNamePrefix, 1)
}

func GetServiceTypeLabels(labels map[string]string) map[string]string {
	generatedLabels := make(map[string]string)

	// Only watch for service type labels
	for label := range labels {
		if strings.HasPrefix(label, "type.services.vmware.com/") {
			generatedLabels[label] = ""
		}
	}
	return generatedLabels
}

func GetExpectedCVMIFrom(cclItem imgregv1a1.ClusterContentLibraryItem,
	providerFunc func(context.Context, crtlclient.Object, crtlclient.Object) error) *vmopv1.ClusterVirtualMachineImage {

	cvmi := &vmopv1.ClusterVirtualMachineImage{
		ObjectMeta: metav1.ObjectMeta{
			Name:   GetTestVMINameFrom(cclItem.Name),
			Labels: GetServiceTypeLabels(cclItem.Labels),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         cclItem.APIVersion,
					Kind:               cclItem.Kind,
					Name:               cclItem.Name,
					Controller:         &[]bool{true}[0],
					BlockOwnerDeletion: &[]bool{true}[0],
				},
			},
		},
		Spec: vmopv1.VirtualMachineImageSpec{
			ProviderRef: common.LocalObjectRef{
				APIVersion: cclItem.APIVersion,
				Kind:       cclItem.Kind,
				Name:       cclItem.Name,
			},
		},
		Status: vmopv1.VirtualMachineImageStatus{
			Name:                   cclItem.Status.Name,
			ProviderItemID:         string(cclItem.Spec.UUID),
			ProviderContentVersion: cclItem.Status.ContentVersion,
			Conditions: []metav1.Condition{
				{
					Type:   vmopv1.VirtualMachineImageProviderReadyCondition,
					Status: metav1.ConditionTrue,
					Reason: vmopv1.VirtualMachineImageProviderReadyCondition,
				},
				{
					Type:   vmopv1.VirtualMachineImageProviderSecurityComplianceCondition,
					Status: metav1.ConditionTrue,
					Reason: vmopv1.VirtualMachineImageProviderSecurityComplianceCondition,
				},
				{
					Type:   vmopv1.VirtualMachineImageSyncedCondition,
					Status: metav1.ConditionTrue,
					Reason: vmopv1.VirtualMachineImageSyncedCondition,
				},
			},
		},
	}

	if providerFunc != nil {
		_ = providerFunc(nil, nil, cvmi)
	}

	return cvmi
}

func GetExpectedVMIFrom(clItem imgregv1a1.ContentLibraryItem,
	providerFunc func(context.Context, crtlclient.Object, crtlclient.Object) error) *vmopv1.VirtualMachineImage {

	vmi := &vmopv1.VirtualMachineImage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetTestVMINameFrom(clItem.Name),
			Namespace: clItem.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         clItem.APIVersion,
					Kind:               clItem.Kind,
					Name:               clItem.Name,
					Controller:         &[]bool{true}[0],
					BlockOwnerDeletion: &[]bool{true}[0],
				},
			},
		},
		Spec: vmopv1.VirtualMachineImageSpec{
			ProviderRef: common.LocalObjectRef{
				APIVersion: clItem.APIVersion,
				Kind:       clItem.Kind,
				Name:       clItem.Name,
			},
		},
		Status: vmopv1.VirtualMachineImageStatus{
			Name:                   clItem.Status.Name,
			ProviderItemID:         string(clItem.Spec.UUID),
			ProviderContentVersion: clItem.Status.ContentVersion,
			Conditions: []metav1.Condition{
				{
					Type:   vmopv1.VirtualMachineImageProviderReadyCondition,
					Status: metav1.ConditionTrue,
				},
				{
					Type:   vmopv1.VirtualMachineImageProviderSecurityComplianceCondition,
					Status: metav1.ConditionTrue,
				},
				{
					Type:   vmopv1.VirtualMachineImageSyncedCondition,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	if providerFunc != nil {
		_ = providerFunc(nil, nil, vmi)
	}

	return vmi
}

func PopulateRuntimeFieldsTo(vmi, appliedVMI crtlclient.Object) {
	status := &vmopv1.VirtualMachineImageStatus{}
	appliedStatus := &vmopv1.VirtualMachineImageStatus{}

	switch vmi := vmi.(type) {
	case *vmopv1.ClusterVirtualMachineImage:
		status = &vmi.Status
		appliedStatus = &appliedVMI.(*vmopv1.ClusterVirtualMachineImage).Status
	case *vmopv1.VirtualMachineImage:
		status = &vmi.Status
		appliedStatus = &appliedVMI.(*vmopv1.VirtualMachineImage).Status
	}

	// Populate condition LastTransitionTime.
	if appliedStatus.Conditions != nil {
		transactionTimeMap := map[string]metav1.Time{}
		for _, condition := range appliedStatus.Conditions {
			transactionTimeMap[condition.Type] = condition.LastTransitionTime
		}
		updatedConditions := []metav1.Condition{}
		for _, condition := range status.Conditions {
			if transactionTime, ok := transactionTimeMap[condition.Type]; ok {
				condition.LastTransitionTime = transactionTime
			}
			updatedConditions = append(updatedConditions, condition)
		}
		status.Conditions = updatedConditions
	}

	// Populate owner reference UID.
	appliedOwnerReferences := appliedVMI.GetOwnerReferences()
	ownerReferences := vmi.GetOwnerReferences()
	if appliedOwnerReferences != nil {
		uidMap := map[string]types.UID{}
		for _, appliedOwnerReference := range appliedOwnerReferences {
			uidMap[appliedOwnerReference.Name] = appliedOwnerReference.UID
		}
		updatedOwnerReferences := []metav1.OwnerReference{}
		for _, ownerReference := range ownerReferences {
			if uid, ok := uidMap[ownerReference.Name]; ok {
				ownerReference.UID = uid
			}
			updatedOwnerReferences = append(updatedOwnerReferences, ownerReference)
		}
		ownerReferences = updatedOwnerReferences
		vmi.SetOwnerReferences(ownerReferences)
	}
}
