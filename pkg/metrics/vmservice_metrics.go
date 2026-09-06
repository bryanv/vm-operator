// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/conditions"
)

// collectTimeout bounds how long a single scrape may spend listing
// VirtualMachines out of the cache.
const collectTimeout = 30 * time.Second

// vmLister is the subset of ctrlclient.Reader the collector needs.
type vmLister interface {
	List(ctx context.Context, list *vmopv1.VirtualMachineList) error
}

var _ prometheus.Collector = &VMCollector{}

// VMCollector reports the VirtualMachine metrics by walking the informer cache
// when Prometheus scrapes.
//
// The metrics used to be gauges written from the VirtualMachine reconcile loop.
// That made every reconcile pay for them, and it made the cost of a single
// update grow with the number of VMs in the cluster: because the mutable state
// -- a condition's reason, the power states, the phase -- is carried in labels
// rather than in values, each update had to sweep the series it orphaned with
// DeletePartialMatch, which locks the whole vector and walks every entry in it.
//
// Collecting on scrape instead means the reconcile loop pays nothing, the cost
// is bounded by the scrape interval rather than by the reconcile rate, and a VM
// that no longer exists cannot be reported, because it is simply not in the
// cache. It also means there is no gauge state to keep, and so no finalizer and
// no delete bookkeeping.
type VMCollector struct {
	lister vmLister
	logger logr.Logger

	// ready reports whether the cache backing the lister has synced. The
	// cache-backed reader blocks until it has, and a scrape would block with
	// it, so nothing is collected before then.
	ready atomic.Bool

	conditionStatus *prometheus.Desc
	phase           *prometheus.Desc
	powerState      *prometheus.Desc
	statusIP        *prometheus.Desc
}

// NewVMCollector returns a collector that reports the VirtualMachine metrics
// for the VMs returned by lister.
func NewVMCollector(lister vmLister, logger logr.Logger) *VMCollector {
	return &VMCollector{
		lister: lister,
		logger: logger,

		conditionStatus: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, "", "vm_status_condition_status"),
			"True/False/Unknown status of a specific condition on a VM resource",
			[]string{vmNameLabel, vmNamespaceLabel, conditionTypeLabel, conditionReasonLabel},
			nil,
		),
		phase: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, "", "vm_status_phase"),
			"True/False/Unknown status of a creating/created/unknown on a VM resource",
			[]string{vmNameLabel, vmNamespaceLabel, phaseLabel},
			nil,
		),
		powerState: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, "", "vm_powerstate"),
			"Desired and current power state on a VM resource",
			[]string{vmNameLabel, vmNamespaceLabel, specLabel, statusLabel},
			nil,
		),
		statusIP: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, "", "vm_status_ip"),
			"IP address assignment status of a VM resource",
			[]string{vmNameLabel, vmNamespaceLabel},
			nil,
		),
	}
}

// MarkReady tells the collector its lister is usable. Until it is called,
// Collect reports nothing.
func (c *VMCollector) MarkReady() {
	c.ready.Store(true)
}

// Describe implements prometheus.Collector.
func (c *VMCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.conditionStatus
	ch <- c.phase
	ch <- c.powerState
	ch <- c.statusIP
}

// Collect implements prometheus.Collector.
func (c *VMCollector) Collect(ch chan<- prometheus.Metric) {
	if !c.ready.Load() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()

	var list vmopv1.VirtualMachineList
	if err := c.lister.List(ctx, &list); err != nil {
		// Report nothing rather than returning an error: the metrics server
		// answers a failed gather with a 500 for the entire endpoint, so a
		// failure here would take every other metric down with it.
		c.logger.Error(err, "Failed to list VirtualMachines for metrics")
		return
	}

	for i := range list.Items {
		c.collectVM(ch, &list.Items[i])
	}
}

func (c *VMCollector) collectVM(
	ch chan<- prometheus.Metric,
	vm *vmopv1.VirtualMachine) {

	c.collectConditions(ch, vm)

	ch <- prometheus.MustNewConstMetric(
		c.phase,
		prometheus.GaugeValue,
		1,
		vm.Name, vm.Namespace, vmCreationPhase(vm))

	ch <- prometheus.MustNewConstMetric(
		c.powerState,
		prometheus.GaugeValue,
		1,
		vm.Name, vm.Namespace,
		string(vm.Spec.PowerState), string(vm.Status.PowerState))

	var hasIP float64
	if n := vm.Status.Network; n != nil && (n.PrimaryIP4 != "" || n.PrimaryIP6 != "") {
		hasIP = 1
	}

	ch <- prometheus.MustNewConstMetric(
		c.statusIP,
		prometheus.GaugeValue,
		hasIP,
		vm.Name, vm.Namespace)
}

func (c *VMCollector) collectConditions(
	ch chan<- prometheus.Metric,
	vm *vmopv1.VirtualMachine) {

	conds := vm.Status.Conditions

	for i := range conds {
		// Nothing structurally prevents a VM from carrying two conditions of
		// the same type, and two metrics with identical labels fail the
		// gather -- and with it the whole endpoint -- so report only the
		// first of a type.
		if indexOfConditionType(conds, conds[i].Type) != i {
			continue
		}

		ch <- prometheus.MustNewConstMetric(
			c.conditionStatus,
			prometheus.GaugeValue,
			conditionStatusToValue(conds[i].Status),
			vm.Name, vm.Namespace, conds[i].Type, conds[i].Reason)
	}
}

// AddVMCollectorToManager registers the VirtualMachine metrics collector with
// the controller-runtime metrics registry, reading from the manager's cache.
//
// The collector is held back until the manager reports its cache synced, and it
// is registered only while this manager is the leader, which matches the
// behavior of the gauges it replaced: a pod that is not reconciling VMs did not
// report on them either.
func AddVMCollectorToManager(mgr ctrlmgr.Manager) error {
	c := NewVMCollector(
		cachedVMLister{reader: mgr.GetClient()},
		mgr.GetLogger().WithName("vmmetrics"))

	return mgr.Add(ctrlmgr.RunnableFunc(func(ctx context.Context) error {
		if err := metrics.Registry.Register(c); err != nil {
			// Another manager in this process already registered an
			// equivalent collector. Leave it be rather than reporting the
			// same series twice, which would fail every scrape.
			are := prometheus.AlreadyRegisteredError{}
			if !errors.As(err, &are) {
				return err
			}
			return nil
		}
		defer metrics.Registry.Unregister(c)

		if mgr.GetCache().WaitForCacheSync(ctx) {
			c.MarkReady()
		}

		<-ctx.Done()
		return nil
	}))
}

// cachedVMLister lists VirtualMachines out of the controller-runtime cache
// without copying them.
type cachedVMLister struct {
	reader ctrlclient.Reader
}

func (l cachedVMLister) List(
	ctx context.Context,
	list *vmopv1.VirtualMachineList) error {

	// The VMs are read and discarded within Collect, never retained or
	// mutated, so skip the deep copy the cache would otherwise make of every
	// VM on every scrape.
	return l.reader.List(ctx, list, ctrlclient.UnsafeDisableDeepCopy)
}

// vmCreationPhase reports the creation phase of a VM.
//
// v1a2 dropped the Phase field. In practice, the only phases we'd typically see
// are Creating and Created, so use the Created condition to determine it.
func vmCreationPhase(vm *vmopv1.VirtualMachine) string {
	c := conditions.Get(vm, vmopv1.VirtualMachineConditionCreated)
	if c == nil {
		return ""
	}

	switch c.Status {
	case metav1.ConditionTrue:
		return "Created"
	case metav1.ConditionFalse:
		return "Creating"
	default:
		return "Unknown"
	}
}

func conditionStatusToValue(status metav1.ConditionStatus) float64 {
	switch status {
	case metav1.ConditionTrue:
		return 1
	case metav1.ConditionFalse:
		return 0
	default:
		return -1
	}
}

// indexOfConditionType returns the index of the first condition with the given
// type, or -1 if there is no such condition.
func indexOfConditionType(c []metav1.Condition, condType string) int {
	for i := range c {
		if c[i].Type == condType {
			return i
		}
	}
	return -1
}
