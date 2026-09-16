// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package virtualmachinepublishrequest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"

	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/vmware-tanzu/vm-operator/controllers/virtualmachinepublishrequest"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	providerfake "github.com/vmware-tanzu/vm-operator/pkg/providers/fake"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

var intgFakeVMProvider = providerfake.NewVMProvider()

var suite = builder.NewTestSuiteForControllerWithContext(
	// NewVMPublishMetrics is a process-wide singleton: only the first call in
	// this test binary's process decides whether it registers the legacy
	// gauge or no-ops in favor of the scrape-based collector. Pin it to the
	// legacy path so this suite's reconciler-driven specs are what exercises
	// it end to end; the scrape-based path has its own dedicated tests in
	// pkg/metrics.
	pkgcfg.UpdateContext(pkgcfg.NewContextWithDefaultConfig(), func(config *pkgcfg.Config) {
		config.Features.ScrapeMetrics = false
	}),
	virtualmachinepublishrequest.AddToManager,
	func(ctx *pkgctx.ControllerManagerContext, _ ctrlmgr.Manager) error {
		ctx.VMProvider = intgFakeVMProvider
		return nil
	})

func TestVirtualMachinePublishRequest(t *testing.T) {
	suite.Register(t, "VirtualMachinePublishRequest controller suite", intgTests, unitTests)
}

var _ = BeforeSuite(suite.BeforeSuite)

var _ = AfterSuite(suite.AfterSuite)
