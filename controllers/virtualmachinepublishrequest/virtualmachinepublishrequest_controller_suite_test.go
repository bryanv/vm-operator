// Copyright (c) 2022 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachinepublishrequest_test

import (
	"testing"

	. "github.com/onsi/ginkgo"

	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachinepublishrequest"
	ctrlContext "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/lib"
	providerfake "github.com/vmware-tanzu/vm-operator/pkg/vmprovider/fake"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var intgFakeVMProvider = providerfake.NewVMProviderA2()

var suite = builder.NewTestSuiteForControllerWithFSS(
	virtualmachinepublishrequest.AddToManager,
	func(ctx *ctrlContext.ControllerManagerContext, _ ctrlmgr.Manager) error {
		ctx.VMProviderA2 = intgFakeVMProvider
		return nil
	},
	map[string]bool{lib.VMImageRegistryFSS: true},
)

func TestVirtualMachinePublishRequest(t *testing.T) {
	suite.Register(t, "VirtualMachinePublishRequest controller suite", intgTests, unitTests)
}

var _ = BeforeSuite(suite.BeforeSuite)

var _ = AfterSuite(suite.AfterSuite)
