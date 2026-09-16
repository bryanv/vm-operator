// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"errors"
	"sync"
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
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
)

// VirtualMachine metrics have two independent implementations, selected by
// Config.Features.ScrapeMetrics:
//
//   - VMMetrics is a set of GaugeVecs updated from the VirtualMachine
//     reconcile loop. This is the original implementation.
//
//   - VMCollector reads the VirtualMachines out of the informer cache when
//     Prometheus scrapes, rather than updating on every reconcile. See its
//     doc comment for why.
//
// Both report the same series, labels and values. Exactly one of them is
// ever registered in a given process: VMMetrics only calls MustRegister when
// scraping is disabled, and AddVMCollectorToManager is only called when it is
// enabled (see controllers/virtualmachine/controllers.go). Registering both
// would panic -- prometheus.Registry.MustRegister rejects a second collector
// whose descriptor has the same fully-qualified name and label set as one
// already registered -- so that mutual exclusion must be preserved by
// whatever calls these going forward.
//
// When scraping is enabled, RegisterVMCreateOrUpdateMetrics and DeleteMetrics
// become no-ops rather than being removed from their call sites. This keeps
// the VirtualMachine reconciler's diff to the single line that threads the
// flag through, instead of every call site needing its own conditional.

var (
	vmMetricsOnce sync.Once
	vmMetrics     *VMMetrics
)

// VMMetrics updates the VirtualMachine metrics from GaugeVecs written to on
// every reconcile. See the package-level comment for why this is one of two
// implementations.
type VMMetrics struct {
	// scrapeEnabled mirrors Config.Features.ScrapeMetrics as of when this
	// VMMetrics was constructed. When true, VMCollector owns these metrics
	// instead, so RegisterVMCreateOrUpdateMetrics and DeleteMetrics no-op and
	// the GaugeVecs below are left unregistered and unused.
	scrapeEnabled bool

	statusConditionStatus *prometheus.GaugeVec
	statusPhase           *prometheus.GaugeVec
	powerState            *prometheus.GaugeVec
	statusIP              *prometheus.GaugeVec
}

// NewVMMetrics initializes the singleton VMMetrics and, if scrapeEnabled is
// false, registers its GaugeVecs. NewVMMetrics only does this once per
// process regardless of how many times, or with what value of scrapeEnabled,
// it is called -- by the time a second caller could observe a mismatch, the
// first caller has already decided, for the whole process, whether VMMetrics
// or VMCollector owns these metrics.
func NewVMMetrics(scrapeEnabled bool) *VMMetrics {
	vmMetricsOnce.Do(func() {
		vmMetrics = &VMMetrics{
			scrapeEnabled: scrapeEnabled,

			statusConditionStatus: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricsNamespace,
					Name:      "vm_status_condition_status",
					Help:      "True/False/Unknown status of a specific condition on a VM resource"},
				[]string{vmNameLabel, vmNamespaceLabel, conditionTypeLabel, conditionReasonLabel},
			),
			statusPhase: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricsNamespace,
					Name:      "vm_status_phase",
					Help:      "True/False/Unknown status of a creating/created/unknown on a VM resource"},
				[]string{vmNameLabel, vmNamespaceLabel, phaseLabel},
			),
			powerState: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricsNamespace,
					Name:      "vm_powerstate",
					Help:      "Desired and current power state on a VM resource"},
				[]string{vmNameLabel, vmNamespaceLabel, specLabel, statusLabel},
			),

			statusIP: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricsNamespace,
					Name:      "vm_status_ip",
					Help:      "IP address assignment status of a VM resource"},
				[]string{vmNameLabel, vmNamespaceLabel},
			),
		}

		if !scrapeEnabled {
			metrics.Registry.MustRegister(
				vmMetrics.statusConditionStatus,
				vmMetrics.statusPhase,
				vmMetrics.powerState,
				vmMetrics.statusIP,
			)
		}
	})

	return vmMetrics
}

// RegisterVMCreateOrUpdateMetrics updates the VirtualMachine metrics for vm.
// It no-ops when scraping is enabled; see the package-level comment.
func (vmm *VMMetrics) RegisterVMCreateOrUpdateMetrics(vmCtx *pkgctx.VirtualMachineContext) {
	if vmm.scrapeEnabled {
		return
	}

	vmm.registerVMStatusConditions(vmCtx)
	vmm.registerVMStatusCreationPhase(vmCtx)
	vmm.registerVMPowerState(vmCtx)
	vmm.registerVMStatusIP(vmCtx)
}

// DeleteMetrics deletes metrics for a specific VM post deletion reconcile.
// It is critical to stop reporting metrics for a deleted VM resource.
// It no-ops when scraping is enabled; see the package-level comment.
func (vmm *VMMetrics) DeleteMetrics(vmCtx *pkgctx.VirtualMachineContext) {
	if vmm.scrapeEnabled {
		return
	}

	vm := vmCtx.VM
	vmCtx.Logger.V(5).Info("Deleting metrics for VM")

	labels := prometheus.Labels{
		vmNameLabel:      vm.Name,
		vmNamespaceLabel: vm.Namespace,
	}

	// Delete the 'vm.status.condition' metrics.
	vmm.statusConditionStatus.DeletePartialMatch(labels)

	// Delete the 'vm.status.phase' metrics.
	vmm.statusPhase.DeletePartialMatch(labels)

	// Delete the 'vm.spec.powerState' metrics.
	vmm.powerState.DeletePartialMatch(labels)

	// Delete the 'vm.status.ip' metrics.
	vmm.statusIP.DeletePartialMatch(labels)
}

func (vmm *VMMetrics) registerVMStatusConditions(vmCtx *pkgctx.VirtualMachineContext) {
	vm := vmCtx.VM
	vmCtx.Logger.V(5).Info("Adding metrics for VM condition")

	// Delete the previous metrics to address any VM condition reason update.
	labels := prometheus.Labels{
		vmNameLabel:      vm.Name,
		vmNamespaceLabel: vm.Namespace,
	}
	vmm.statusConditionStatus.DeletePartialMatch(labels)

	for _, condition := range vm.Status.Conditions {
		labels := prometheus.Labels{
			vmNameLabel:          vm.Name,
			vmNamespaceLabel:     vm.Namespace,
			conditionTypeLabel:   condition.Type,
			conditionReasonLabel: condition.Reason,
		}
		vmm.statusConditionStatus.With(labels).Set(conditionStatusToValue(condition.Status))
	}
}

func (vmm *VMMetrics) registerVMStatusCreationPhase(vmCtx *pkgctx.VirtualMachineContext) {
	vmCtx.Logger.V(5).Info("Adding metrics for VM status creation phase")
	vm := vmCtx.VM

	// Delete the previous metrics to address any VM status phase update.
	labels := prometheus.Labels{
		vmNameLabel:      vm.Name,
		vmNamespaceLabel: vm.Namespace,
	}
	vmm.statusPhase.DeletePartialMatch(labels)

	newLabels := prometheus.Labels{
		vmNameLabel:      vm.Name,
		vmNamespaceLabel: vm.Namespace,
		phaseLabel:       vmCreationPhase(vm),
	}
	vmm.statusPhase.With(newLabels).Set(1)
}

func (vmm *VMMetrics) registerVMPowerState(vmCtx *pkgctx.VirtualMachineContext) {
	vm := vmCtx.VM
	vmCtx.Logger.V(5).Info("Adding metrics for VM power state")

	// Delete the existing power state metrics to address any VM's power state change.
	labels := prometheus.Labels{
		vmNameLabel:      vm.Name,
		vmNamespaceLabel: vm.Namespace,
	}
	vmm.powerState.DeletePartialMatch(labels)

	newLabels := prometheus.Labels{
		vmNameLabel:      vm.Name,
		vmNamespaceLabel: vm.Namespace,
		specLabel:        string(vm.Spec.PowerState),
		statusLabel:      string(vm.Status.PowerState),
	}
	vmm.powerState.With(newLabels).Set(1)
}

func (vmm *VMMetrics) registerVMStatusIP(vmCtx *pkgctx.VirtualMachineContext) {
	vm := vmCtx.VM
	vmCtx.Logger.V(5).Info("Adding metrics for VM IP address assignment status")

	labels := prometheus.Labels{
		vmNameLabel:      vm.Name,
		vmNamespaceLabel: vm.Namespace,
	}

	var hasIP float64
	if n := vm.Status.Network; n != nil && (n.PrimaryIP4 != "" || n.PrimaryIP6 != "") {
		hasIP = 1
	}
	vmm.statusIP.With(labels).Set(hasIP)
}

// collectTimeout bounds how long a single scrape may spend listing
// VirtualMachines out of the cache.
const collectTimeout = 30 * time.Second

// vmLister is the subset of ctrlclient.Reader the collector needs.
type vmLister interface {
	List(ctx context.Context, list *vmopv1.VirtualMachineList) error
}

var _ prometheus.Collector = &VMCollector{}

// VMCollector reports the VirtualMachine metrics by walking the informer cache
// when Prometheus scrapes. It is the scrapeEnabled counterpart to VMMetrics;
// see the package-level comment for how the two relate.
//
// Updating gauges from the reconcile loop makes every reconcile pay for the
// metrics, and makes the cost of a single update grow with the number of VMs
// in the cluster: because the mutable state -- a condition's reason, the
// power states, the phase -- is carried in labels rather than in values,
// each update has to sweep the series it orphans with DeletePartialMatch,
// which locks the whole vector and walks every entry in it.
//
// Collecting on scrape instead means the reconcile loop pays nothing, the
// cost is bounded by the scrape interval rather than by the reconcile rate,
// and a VM that no longer exists cannot be reported, because it is simply
// not in the cache. It also means there is no gauge state to keep, and so no
// finalizer and no delete bookkeeping.
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
// The caller is responsible for only calling this when scraping is enabled;
// see the package-level comment.
//
// The collector is held back until the manager reports its cache synced, and it
// is registered only while this manager is the leader, which matches the
// behavior of the gauges it replaces: a pod that is not reconciling VMs did not
// report on them either.
func AddVMCollectorToManager(mgr ctrlmgr.Manager) error {
	c := NewVMCollector(
		cachedVMLister{reader: mgr.GetClient()},
		mgr.GetLogger().WithName("vmmetrics"))

	return mgr.Add(ctrlmgr.RunnableFunc(func(ctx context.Context) error {
		if err := metrics.Registry.Register(c); err != nil {
			// A second manager in this process has already registered an
			// identical collector -- same name, labels and help string on
			// every Desc, which is what makes this specific error type fire
			// rather than the plain "already registered" error a mismatched
			// collector would produce. Leave the existing registration in
			// place rather than reporting the same series twice, which would
			// fail every scrape.
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
