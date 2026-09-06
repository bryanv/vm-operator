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

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgmetrics "github.com/vmware-tanzu/vm-operator/pkg/metrics"
)

// fakeLister stands in for the cache-backed reader.
type fakeLister struct {
	items []vmopv1.VirtualMachine
	err   error
}

func (l *fakeLister) List(
	_ context.Context,
	list *vmopv1.VirtualMachineList) error {

	if l.err != nil {
		return l.err
	}
	list.Items = l.items
	return nil
}

var _ = Describe("VMCollector", func() {

	var (
		lister    *fakeLister
		collector *pkgmetrics.VMCollector
	)

	BeforeEach(func() {
		lister = &fakeLister{}
		collector = pkgmetrics.NewVMCollector(lister, logr.Discard())
		collector.MarkReady()
	})

	// gather runs the collector through a registry, exactly as a scrape would,
	// and renders the result as sorted "name{labels} value" lines.
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

	newVM := func(name string) vmopv1.VirtualMachine {
		var vm vmopv1.VirtualMachine
		vm.Namespace, vm.Name = "my-namespace", name
		vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
		vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOn
		return vm
	}

	When("the collector is not ready", func() {
		BeforeEach(func() {
			collector = pkgmetrics.NewVMCollector(lister, logr.Discard())
			lister.items = []vmopv1.VirtualMachine{newVM("vm-1")}
		})

		It("should report nothing", func() {
			Expect(gather()).To(BeEmpty())
		})
	})

	When("there are no VMs", func() {
		It("should report nothing", func() {
			Expect(gather()).To(BeEmpty())
		})
	})

	When("listing the VMs fails", func() {
		BeforeEach(func() {
			lister.items = []vmopv1.VirtualMachine{newVM("vm-1")}
			lister.err = errors.New("cache is on fire")
		})

		It("should report nothing rather than fail the scrape", func() {
			Expect(gather()).To(BeEmpty())
		})
	})

	When("there is a VM", func() {
		BeforeEach(func() {
			vm := newVM("vm-1")
			vm.Status.Network = &vmopv1.VirtualMachineNetworkStatus{
				PrimaryIP4: "10.0.0.1",
			}
			vm.Status.Conditions = []metav1.Condition{
				{
					Type:   vmopv1.VirtualMachineConditionCreated,
					Status: metav1.ConditionTrue,
					Reason: "Created",
				},
			}
			lister.items = []vmopv1.VirtualMachine{vm}
		})

		It("should report every VM metric", func() {
			Expect(gather()).To(ConsistOf(
				`vmservice_vm_status_condition_status{condition_reason="Created",condition_type="VirtualMachineCreated",vm_name="vm-1",vm_namespace="my-namespace"} 1`,
				`vmservice_vm_status_phase{phase="Created",vm_name="vm-1",vm_namespace="my-namespace"} 1`,
				`vmservice_vm_powerstate{spec="PoweredOn",status="PoweredOn",vm_name="vm-1",vm_namespace="my-namespace"} 1`,
				`vmservice_vm_status_ip{vm_name="vm-1",vm_namespace="my-namespace"} 1`,
			))
		})
	})

	When("there are several VMs", func() {
		BeforeEach(func() {
			lister.items = []vmopv1.VirtualMachine{
				newVM("vm-1"), newVM("vm-2"), newVM("vm-3"),
			}
		})

		It("should report each of them", func() {
			Expect(gather()).To(HaveLen(3 * 3)) // no conditions, so no phase
		})
	})

	Context("conditions", func() {
		condStatus := func() []string {
			GinkgoHelper()
			var out []string
			for _, l := range gather() {
				if strings.HasPrefix(l, "vmservice_vm_status_condition_status") {
					out = append(out, l)
				}
			}
			return out
		}

		DescribeTable("condition status to value",
			func(status metav1.ConditionStatus, expected string) {
				vm := newVM("vm-1")
				vm.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: status, Reason: "SomeReason"},
				}
				lister.items = []vmopv1.VirtualMachine{vm}

				Expect(condStatus()).To(ConsistOf(
					`vmservice_vm_status_condition_status{condition_reason="SomeReason",condition_type="Ready",vm_name="vm-1",vm_namespace="my-namespace"} ` + expected,
				))
			},
			Entry("True is 1", metav1.ConditionTrue, "1"),
			Entry("False is 0", metav1.ConditionFalse, "0"),
			Entry("Unknown is -1", metav1.ConditionUnknown, "-1"),
			Entry("an unset status is -1", metav1.ConditionStatus(""), "-1"),
		)

		When("a VM carries two conditions of the same type", func() {
			BeforeEach(func() {
				vm := newVM("vm-1")
				vm.Status.Conditions = []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue, Reason: "First"},
					{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Second"},
				}
				lister.items = []vmopv1.VirtualMachine{vm}
			})

			// Two metrics with the same labels fail the gather, and the
			// metrics server answers a failed gather with a 500 for the whole
			// endpoint, so this must not happen even for a malformed VM.
			It("should report only the first and still gather", func() {
				Expect(condStatus()).To(ConsistOf(
					`vmservice_vm_status_condition_status{condition_reason="First",condition_type="Ready",vm_name="vm-1",vm_namespace="my-namespace"} 1`,
				))
			})
		})
	})

	Context("phase", func() {
		phase := func() []string {
			GinkgoHelper()
			var out []string
			for _, l := range gather() {
				if strings.HasPrefix(l, "vmservice_vm_status_phase") {
					out = append(out, l)
				}
			}
			return out
		}

		DescribeTable("derived from the Created condition",
			func(status metav1.ConditionStatus, expected string) {
				vm := newVM("vm-1")
				vm.Status.Conditions = []metav1.Condition{
					{
						Type:   vmopv1.VirtualMachineConditionCreated,
						Status: status,
						Reason: "SomeReason",
					},
				}
				lister.items = []vmopv1.VirtualMachine{vm}

				Expect(phase()).To(ConsistOf(
					`vmservice_vm_status_phase{phase="` + expected + `",vm_name="vm-1",vm_namespace="my-namespace"} 1`,
				))
			},
			Entry("True is Created", metav1.ConditionTrue, "Created"),
			Entry("False is Creating", metav1.ConditionFalse, "Creating"),
			Entry("Unknown is Unknown", metav1.ConditionUnknown, "Unknown"),
		)

		When("the VM has no Created condition", func() {
			BeforeEach(func() {
				lister.items = []vmopv1.VirtualMachine{newVM("vm-1")}
			})

			It("should report an empty phase", func() {
				Expect(phase()).To(ConsistOf(
					`vmservice_vm_status_phase{phase="",vm_name="vm-1",vm_namespace="my-namespace"} 1`,
				))
			})
		})
	})

	Context("status IP", func() {
		statusIP := func() []string {
			GinkgoHelper()
			var out []string
			for _, l := range gather() {
				if strings.HasPrefix(l, "vmservice_vm_status_ip") {
					out = append(out, l)
				}
			}
			return out
		}

		DescribeTable("presence of an IP",
			func(network *vmopv1.VirtualMachineNetworkStatus, expected string) {
				vm := newVM("vm-1")
				vm.Status.Network = network
				lister.items = []vmopv1.VirtualMachine{vm}

				Expect(statusIP()).To(ConsistOf(
					`vmservice_vm_status_ip{vm_name="vm-1",vm_namespace="my-namespace"} ` + expected,
				))
			},
			Entry("no network status", nil, "0"),
			Entry("empty network status",
				&vmopv1.VirtualMachineNetworkStatus{}, "0"),
			Entry("an IPv4 address",
				&vmopv1.VirtualMachineNetworkStatus{PrimaryIP4: "10.0.0.1"}, "1"),
			Entry("an IPv6 address",
				&vmopv1.VirtualMachineNetworkStatus{PrimaryIP6: "::1"}, "1"),
		)
	})

	Context("power state", func() {
		When("the observed power state is not yet known", func() {
			BeforeEach(func() {
				vm := newVM("vm-1")
				vm.Status.PowerState = ""
				lister.items = []vmopv1.VirtualMachine{vm}
			})

			It("should report an empty status label", func() {
				Expect(gather()).To(ContainElement(
					`vmservice_vm_powerstate{spec="PoweredOn",status="",vm_name="vm-1",vm_namespace="my-namespace"} 1`,
				))
			})
		})
	})
})
