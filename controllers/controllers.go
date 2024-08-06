// Copyright (c) 2019-2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/vm-operator/controllers/contentlibrary"
	"github.com/vmware-tanzu/vm-operator/controllers/infra"
	spq "github.com/vmware-tanzu/vm-operator/controllers/storagepolicyquota"
	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachine"
	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachineclass"
	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachinepublishrequest"
	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachinereplicaset"
	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachineservice"
	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachinesetresourcepolicy"
	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachinewebconsolerequest"
	"github.com/vmware-tanzu/vm-operator/controllers/volume"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
)

// AddToManager adds all controllers to the provided manager.
func AddToManager(ctx *pkgctx.ControllerManagerContext, mgr manager.Manager) error {
	if err := contentlibrary.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize ContentLibrary controllers: %w", err)
	}
	if err := infra.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize Infra controllers: %w", err)
	}
	if pkgcfg.FromContext(ctx).Features.UnifiedStorageQuota {
		if err := spq.AddToManager(ctx, mgr); err != nil {
			return fmt.Errorf("failed to initialize StoragePolicyQuota controller: %w", err)
		}
	}
	if err := virtualmachine.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize VirtualMachine controller: %w", err)
	}
	if err := virtualmachineclass.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize VirtualMachineClass controller: %w", err)
	}
	if err := virtualmachineservice.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize VirtualMachineService controller: %w", err)
	}
	if err := virtualmachinesetresourcepolicy.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize VirtualMachineSetResourcePolicy controller: %w", err)
	}
	if err := virtualmachinewebconsolerequest.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize VirtualMachineWebConsoleRequest controller: %w", err)
	}
	if err := volume.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize Volume controller: %w", err)
	}
	if err := virtualmachinepublishrequest.AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to initialize VirtualMachinePublishRequest controller: %w", err)
	}

	if pkgcfg.FromContext(ctx).Features.K8sWorkloadMgmtAPI {
		if err := virtualmachinereplicaset.AddToManager(ctx, mgr); err != nil {
			return fmt.Errorf("failed to initialize VirtualMachineReplicaSet controller: %w", err)
		}
	}

	return nil
}
