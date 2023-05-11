// Copyright (c) 2022-2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vsphere

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/vim25/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	conditions "github.com/vmware-tanzu/vm-operator/pkg/conditions2"
	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/util"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/instancestorage"
)

func errToConditionReasonAndMessage(err error) (string, string) {
	if apierrors.IsNotFound(err) {
		return "NotFound", err.Error()
	}

	return "GetError", err.Error()
}

func GetVirtualMachineClass(
	vmCtx context.VirtualMachineContextA2,
	k8sClient ctrlclient.Client) (*vmopv1.VirtualMachineClass, error) {

	key := ctrlclient.ObjectKey{Name: vmCtx.VM.Spec.ClassName, Namespace: vmCtx.VM.Namespace}
	vmClass := &vmopv1.VirtualMachineClass{}
	if err := k8sClient.Get(vmCtx, key, vmClass); err != nil {
		reason, msg := errToConditionReasonAndMessage(err)
		conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionClassReady, reason, msg)
		return nil, err
	}

	if !vmClass.Status.Ready {
		conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionClassReady,
			"NotReady", "VirtualMachineClass is not marked as Ready")
		return nil, fmt.Errorf("VirtualMachineClass is not Ready")
	}

	conditions.MarkTrue(vmCtx.VM, vmopv1.VirtualMachineConditionClassReady)

	return vmClass, nil
}

func GetVirtualMachineImageSpecAndStatus(
	vmCtx context.VirtualMachineContextA2,
	k8sClient ctrlclient.Client) (ctrlclient.Object, *vmopv1.VirtualMachineImageSpec, *vmopv1.VirtualMachineImageStatus, error) {

	var obj ctrlclient.Object
	var spec *vmopv1.VirtualMachineImageSpec
	var status *vmopv1.VirtualMachineImageStatus

	key := ctrlclient.ObjectKey{Name: vmCtx.VM.Spec.ImageName, Namespace: vmCtx.VM.Namespace}
	vmImage := &vmopv1.VirtualMachineImage{}
	if err := k8sClient.Get(vmCtx, key, vmImage); err != nil {
		clusterVMImage := &vmopv1.ClusterVirtualMachineImage{}

		if apierrors.IsNotFound(err) {
			key.Namespace = ""
			err = k8sClient.Get(vmCtx, key, clusterVMImage)
		}

		if err != nil {
			reason, msg := errToConditionReasonAndMessage(err)
			conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionImageReady, reason, msg)
			return nil, nil, nil, err
		}

		obj, spec, status = clusterVMImage, &clusterVMImage.Spec, &clusterVMImage.Status
	} else {
		obj, spec, status = vmImage, &vmImage.Spec, &vmImage.Status
	}

	// TODO: Fix the image conditions so it just has a single Ready instead of bleeding the CL stuff.
	if !conditions.IsTrueFromConditions(status.Conditions, vmopv1.VirtualMachineImageSyncedCondition) {
		conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionImageReady,
			"NotReady", "VirtualMachineImage is not marked as Ready")
		return nil, nil, nil, fmt.Errorf("VirtualMachineImage is not Ready")
	}

	conditions.MarkTrue(vmCtx.VM, vmopv1.VirtualMachineConditionImageReady)

	return obj, spec, status, nil
}

func getSecretData(
	vmCtx context.VirtualMachineContextA2,
	name string,
	cmFallback bool,
	k8sClient ctrlclient.Client) (map[string]string, error) {

	var data map[string]string

	key := ctrlclient.ObjectKey{Name: name, Namespace: vmCtx.VM.Namespace}
	secret := &corev1.Secret{}
	if err := k8sClient.Get(vmCtx, key, secret); err != nil {
		configMap := &corev1.ConfigMap{}

		// For backwards compat if we cannot find the Secret, fallback to a ConfigMap. In v1a1, both
		// Secrets and ConfigMaps were supported for metadata (bootstrap), but v1a2 only supports Secrets.
		if cmFallback && apierrors.IsNotFound(err) {
			err = k8sClient.Get(vmCtx, key, configMap)
		}

		if err != nil {
			reason, msg := errToConditionReasonAndMessage(err)
			conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionBootstrapReady, reason, msg)
			return nil, err
		}

		data = configMap.Data
	} else {
		data = make(map[string]string, len(secret.Data))

		for k, v := range secret.Data {
			data[k] = string(v)
		}
	}

	return data, nil
}

func GetVirtualMachineBootstrap(
	vmCtx context.VirtualMachineContextA2,
	k8sClient ctrlclient.Client) (map[string]string, map[string]string, map[string]map[string]string, error) {

	bootstrapSpec := &vmCtx.VM.Spec.Bootstrap
	var secretName string
	var data, vAppData map[string]string
	var vAppExData map[string]map[string]string

	if cloudInit := bootstrapSpec.CloudInit; cloudInit != nil {
		secretName = cloudInit.RawCloudConfig.Name
	} else if sysPrep := bootstrapSpec.Sysprep; sysPrep != nil {
		secretName = sysPrep.RawSysprep.Name
	}

	if secretName != "" {
		var err error

		data, err = getSecretData(vmCtx, secretName, true, k8sClient)
		if err != nil {
			reason, msg := errToConditionReasonAndMessage(err)
			conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionBootstrapReady, reason, msg)
			return nil, nil, nil, err
		}
	}

	// vApp bootstrap can be used alongside LinuxPrep/Sysprep.
	if vApp := bootstrapSpec.VAppConfig; vApp != nil {
		var err error

		if vApp.RawProperties != "" {
			vAppData, err = getSecretData(vmCtx, vApp.RawProperties, true, k8sClient)
			if err != nil {
				reason, msg := errToConditionReasonAndMessage(err)
				conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionBootstrapReady, reason, msg)
				return nil, nil, nil, err
			}

		} else {
			for _, p := range vApp.Properties {
				from := p.Value.From
				if from == nil {
					continue
				}

				if _, ok := vAppExData[from.Name]; !ok {
					// Do the easy thing here and carry along each Secret's entire data. We could instead
					// shoehorn this in the vAppData with a concat key using an invalid k8s name delimiter.
					// TODO: Check that key exists, and/or deal with from.Optional. Too many options.
					fromData, err := getSecretData(vmCtx, from.Name, false, k8sClient)
					if err != nil {
						reason, msg := errToConditionReasonAndMessage(err)
						conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionBootstrapReady, reason, msg)
						return nil, nil, nil, err
					}

					if vAppExData == nil {
						vAppExData = make(map[string]map[string]string)
					}
					vAppExData[from.Name] = fromData
				}
			}
		}
	}

	conditions.MarkTrue(vmCtx.VM, vmopv1.VirtualMachineConditionBootstrapReady)

	return data, vAppData, vAppExData, nil
}

func GetVMSetResourcePolicy(
	vmCtx context.VirtualMachineContextA2,
	k8sClient ctrlclient.Client) (*vmopv1.VirtualMachineSetResourcePolicy, error) {

	rpName := vmCtx.VM.Spec.Reserved.ResourcePolicyName
	if rpName == "" {
		conditions.Delete(vmCtx.VM, vmopv1.VirtualMachineConditionVMSetResourcePolicyReady)
		return nil, nil
	}

	key := ctrlclient.ObjectKey{Name: rpName, Namespace: vmCtx.VM.Namespace}
	resourcePolicy := &vmopv1.VirtualMachineSetResourcePolicy{}
	if err := k8sClient.Get(vmCtx, key, resourcePolicy); err != nil {
		reason, msg := errToConditionReasonAndMessage(err)
		conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionVMSetResourcePolicyReady, reason, msg)
		return nil, err
	}

	// The VirtualMachineSetResourcePolicy doesn't have a Ready condition or field but don't
	// allow a VM to use a policy that's being deleted.
	if !resourcePolicy.DeletionTimestamp.IsZero() {
		err := fmt.Errorf("VirtualMachineSetResourcePolicy is being deleted")
		conditions.MarkFalse(vmCtx.VM, vmopv1.VirtualMachineConditionVMSetResourcePolicyReady,
			"NotReady", err.Error())
		return nil, err
	}

	conditions.MarkTrue(vmCtx.VM, vmopv1.VirtualMachineConditionVMSetResourcePolicyReady)

	return resourcePolicy, nil
}

// AddInstanceStorageVolumes checks if VM class is configured with instance storage volumes and appends the
// volumes to the VM's Spec if not already done. Return true if the VM had or now has instance volumes.
func AddInstanceStorageVolumes(
	vmCtx context.VirtualMachineContextA2,
	vmClass *vmopv1.VirtualMachineClass) bool {

	if instancestorage.IsPresent(vmCtx.VM) {
		// Instance storage disks are copied from the class to the VM only once, regardless
		// if the class changes.
		return true
	}

	is := vmClass.Spec.Hardware.InstanceStorage
	if len(is.Volumes) == 0 {
		return false
	}

	volumes := make([]vmopv1.VirtualMachineVolume, 0, len(is.Volumes))

	for _, isv := range is.Volumes {
		name := constants.InstanceStoragePVCNamePrefix + uuid.NewString()

		vmv := vmopv1.VirtualMachineVolume{
			Name: name,
			VirtualMachineVolumeSource: vmopv1.VirtualMachineVolumeSource{
				PersistentVolumeClaim: &vmopv1.PersistentVolumeClaimVolumeSource{
					PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: name,
						ReadOnly:  false,
					},
					InstanceVolumeClaim: &vmopv1.InstanceVolumeClaimVolumeSource{
						StorageClass: is.StorageClass,
						Size:         isv.Size,
					},
				},
			},
		}
		volumes = append(volumes, vmv)
	}

	vmCtx.VM.Spec.Volumes = append(vmCtx.VM.Spec.Volumes, volumes...)
	return true
}

func GetVMClassConfigSpec(raw json.RawMessage) (*types.VirtualMachineConfigSpec, error) {
	classConfigSpec, err := util.UnmarshalConfigSpecFromJSON(raw)
	if err != nil {
		return nil, err
	}
	util.SanitizeVMClassConfigSpec(classConfigSpec)

	return classConfigSpec, nil
}
