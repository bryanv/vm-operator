// Copyright (c) 2019-2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"

	pkgconfig "github.com/vmware-tanzu/vm-operator/pkg/config"
	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/webhooks/virtualmachine/v1alpha2"
)

func AddToManager(ctx *context.ControllerManagerContext, mgr ctrlmgr.Manager) error {
	if pkgconfig.FromContext(ctx).Features.VMOpV1Alpha2 {
		return v1alpha2.AddToManager(ctx, mgr)
	}
	return nil
}
