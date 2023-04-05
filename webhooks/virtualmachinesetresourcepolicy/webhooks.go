// Copyright (c) 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachinesetresourcepolicy

import (
	"github.com/pkg/errors"

	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/webhooks/virtualmachinesetresourcepolicy/v1alpha1"
	"github.com/vmware-tanzu/vm-operator/webhooks/virtualmachinesetresourcepolicy/v1alpha2"
)

func AddToManager(ctx *context.ControllerManagerContext, mgr ctrlmgr.Manager) error {
	if err := v1alpha1.AddToManager(ctx, mgr); err != nil {
		return errors.Wrap(err, "failed to initialize v1alpha1 webhooks")
	}
	if err := v1alpha2.AddToManager(ctx, mgr); err != nil {
		return errors.Wrap(err, "failed to initialize v1alpha2 webhooks")
	}

	return nil
}
