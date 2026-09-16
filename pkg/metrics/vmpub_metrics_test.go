// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package metrics_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgmetrics "github.com/vmware-tanzu/vm-operator/pkg/metrics"
)

// fakeVMPubLister stands in for the cache-backed reader.
type fakeVMPubLister struct {
	items []vmopv1.VirtualMachinePublishRequest
	err   error
}

func (l *fakeVMPubLister) List(
	_ context.Context,
	list *vmopv1.VirtualMachinePublishRequestList) error {

	if l.err != nil {
		return l.err
	}
	list.Items = l.items
	return nil
}

func newDummyVMPub(name string) vmopv1.VirtualMachinePublishRequest {
	var vmPub vmopv1.VirtualMachinePublishRequest
	vmPub.Namespace, vmPub.Name = "my-namespace", name
	return vmPub
}

var _ = Describe("VMPublishCollector", func() {
	var (
		lister    *fakeVMPubLister
		collector *pkgmetrics.VMPublishCollector
	)

	BeforeEach(func() {
		lister = &fakeVMPubLister{}
		collector = pkgmetrics.NewVMPublishCollector(lister, logr.Discard())
		collector.MarkReady()
	})

	gather := func() []string {
		GinkgoHelper()

		reg := prometheus.NewPedanticRegistry()
		Expect(reg.Register(collector)).To(Succeed())

		mfs, err := reg.Gather()
		Expect(err).ToNot(HaveOccurred())

		var out []string
		for _, mf := range mfs {
			for _, m := range mf.GetMetric() {
				var lbls []string
				for _, lp := range m.GetLabel() {
					lbls = append(lbls,
						fmt.Sprintf("%s=%q", lp.GetName(), lp.GetValue()))
				}
				sort.Strings(lbls)
				out = append(out, fmt.Sprintf("%s{%s} %v",
					mf.GetName(), strings.Join(lbls, ","),
					m.GetGauge().GetValue()))
			}
		}
		sort.Strings(out)
		return out
	}

	When("the collector is not ready", func() {
		BeforeEach(func() {
			collector = pkgmetrics.NewVMPublishCollector(lister, logr.Discard())
			lister.items = []vmopv1.VirtualMachinePublishRequest{newDummyVMPub("vmpub-1")}
		})

		It("should report nothing", func() {
			Expect(gather()).To(BeEmpty())
		})
	})

	When("there are no VirtualMachinePublishRequests", func() {
		It("should report nothing", func() {
			Expect(gather()).To(BeEmpty())
		})
	})

	When("listing fails", func() {
		BeforeEach(func() {
			lister.items = []vmopv1.VirtualMachinePublishRequest{newDummyVMPub("vmpub-1")}
			lister.err = errors.New("cache is on fire")
		})

		It("should report nothing rather than fail the scrape", func() {
			Expect(gather()).To(BeEmpty())
		})
	})

	DescribeTable("publish result derived from the Complete condition",
		func(cond *metav1.Condition, expected string) {
			vmPub := newDummyVMPub("vmpub-1")
			if cond != nil {
				vmPub.Status.Conditions = []metav1.Condition{*cond}
			}
			lister.items = []vmopv1.VirtualMachinePublishRequest{vmPub}

			Expect(gather()).To(ConsistOf(
				`vmservice_vm_publish_request{name="vmpub-1",namespace="my-namespace"} ` + expected,
			))
		},
		Entry("no Complete condition at all", nil, "0"),
		Entry("Complete is True",
			&metav1.Condition{
				Type:   vmopv1.VirtualMachinePublishRequestConditionComplete,
				Status: metav1.ConditionTrue,
			}, "1"),
		Entry("Complete is False with the Fatal reason",
			&metav1.Condition{
				Type:   vmopv1.VirtualMachinePublishRequestConditionComplete,
				Status: metav1.ConditionFalse,
				Reason: vmopv1.FatalReason,
			}, "-1"),
		Entry("Complete is False with a non-fatal reason",
			&metav1.Condition{
				Type:   vmopv1.VirtualMachinePublishRequestConditionComplete,
				Status: metav1.ConditionFalse,
				Reason: vmopv1.HasNotBeenUploadedReason,
			}, "0"),
	)
})

// VMPublishMetrics is a process-wide singleton (see its doc comment), so
// only the first call to NewVMPublishMetrics across this whole test binary
// has any effect. This is the only call in this package's tests, and it
// deliberately passes true, to verify the no-op side of the flag; the legacy
// GaugeVec-writing path is exercised end to end, through the reconciler, by
// controllers/virtualmachinepublishrequest's own tests instead.
var _ = Describe("VMPublishMetrics", func() {
	var vpm *pkgmetrics.VMPublishMetrics

	BeforeEach(func() {
		vpm = pkgmetrics.NewVMPublishMetrics(true)
	})

	vmPubSeriesNames := func() []string {
		mfs, err := ctrlmetrics.Registry.Gather()
		Expect(err).ToNot(HaveOccurred())

		var names []string
		for _, mf := range mfs {
			if strings.HasPrefix(mf.GetName(), "vmservice_vm_publish_request") {
				names = append(names, mf.GetName())
			}
		}
		return names
	}

	When("scraping is enabled", func() {
		It("should not register the publish-request gauge", func() {
			Expect(vmPubSeriesNames()).To(BeEmpty())
		})

		It("RegisterVMPublishRequest should no-op", func() {
			Expect(func() {
				vpm.RegisterVMPublishRequest(logr.Discard(), "vmpub-1", "ns", pkgmetrics.PublishSucceeded)
			}).ToNot(Panic())
			Expect(vmPubSeriesNames()).To(BeEmpty())
		})

		It("DeleteMetrics should no-op", func() {
			Expect(func() {
				vpm.DeleteMetrics(logr.Discard(), "vmpub-1", "ns")
			}).ToNot(Panic())
			Expect(vmPubSeriesNames()).To(BeEmpty())
		})
	})
})
