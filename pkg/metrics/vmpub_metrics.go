// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmgr "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/conditions"
)

// The VirtualMachinePublishRequest publish-request metric has two independent
// implementations, selected by Config.Features.ScrapeMetrics. See the
// package-level comment on VMMetrics in vmservice_metrics.go for how the two
// relate and why registering both would panic.
//
// Unlike the VirtualMachine metrics, this one really is a straightforward
// port: PublishResult is computed entirely from the persisted Complete
// condition (see checkIsComplete in the VirtualMachinePublishRequest
// controller), with one narrow exception. A bare reconcile error that never
// touches the Complete condition -- a transient failure such as a timed-out
// Get -- used to report PublishFailed for that one reconcile and then
// PublishInProgress again on the next. VMPublishCollector reports
// PublishInProgress the whole time instead, since nothing about that failure
// is persisted for it to read back. A gauge that is one attempt away from
// flipping back to InProgress anyway is not a value worth reconstructing at
// the cost of reading the object's state on every reconcile; scrape-based
// collection is the more accurate description of what this metric has always
// claimed to measure -- the request's outcome, not its reconcile history.

var (
	vmPubMetricsOnce sync.Once
	vmPubMetrics     *VMPublishMetrics
)

type PublishResult int

const (
	PublishFailed     PublishResult = -1
	PublishInProgress PublishResult = 0
	PublishSucceeded  PublishResult = 1
)

// VMPublishMetrics updates the VM publish request metric from a GaugeVec
// written to on every reconcile. See the package-level comment for why this
// is one of two implementations.
type VMPublishMetrics struct {
	// scrapeEnabled mirrors Config.Features.ScrapeMetrics as of when this
	// VMPublishMetrics was constructed. When true, VMPublishCollector owns
	// this metric instead, so RegisterVMPublishRequest and DeleteMetrics
	// no-op and the GaugeVec below is left unregistered and unused.
	scrapeEnabled bool

	vmPubRequest *prometheus.GaugeVec
}

// NewVMPublishMetrics initializes the singleton VMPublishMetrics and, if
// scrapeEnabled is false, registers its GaugeVec. Like NewVMMetrics, this
// only happens once per process regardless of how many times, or with what
// value of scrapeEnabled, it is called.
func NewVMPublishMetrics(scrapeEnabled bool) *VMPublishMetrics {
	vmPubMetricsOnce.Do(func() {
		vmPubMetrics = &VMPublishMetrics{
			scrapeEnabled: scrapeEnabled,

			vmPubRequest: prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Subsystem: "vm",
				Name:      "publish_request",
				Help:      "VirtualMachine publish request result",
			}, []string{
				vmPubNameLabel,
				vmPubNamespaceLabel,
			}),
		}

		if !scrapeEnabled {
			metrics.Registry.MustRegister(
				vmPubMetrics.vmPubRequest,
			)
		}
	})

	return vmPubMetrics
}

// RegisterVMPublishRequest registers VM publish request metrics with the
// given value. It no-ops when scraping is enabled; see the package-level
// comment.
func (m *VMPublishMetrics) RegisterVMPublishRequest(logger logr.Logger, reqName, ns string, val PublishResult) {
	if m.scrapeEnabled {
		return
	}

	labels := getVMPubRequestLabels(reqName, ns)
	m.vmPubRequest.With(labels).Set(float64(val))

	logger.V(5).WithValues("labels", labels, "result", val).Info("Set metrics for VM publish request")
}

// DeleteMetrics deletes all the related VM publish request metrics from the
// given name and namespace. It no-ops when scraping is enabled; see the
// package-level comment.
func (m *VMPublishMetrics) DeleteMetrics(logger logr.Logger, reqName, ns string) {
	if m.scrapeEnabled {
		return
	}

	labels := getVMPubRequestLabels(reqName, ns)
	deleted := m.vmPubRequest.Delete(labels)

	logger.V(5).WithValues("labels", labels, "deleted", deleted).Info("Delete VM publish request metrics")
}

func getVMPubRequestLabels(name, ns string) prometheus.Labels {
	return prometheus.Labels{
		vmPubNameLabel:      name,
		vmPubNamespaceLabel: ns,
	}
}

// vmPubLister is the subset of ctrlclient.Reader the collector needs.
type vmPubLister interface {
	List(ctx context.Context, list *vmopv1.VirtualMachinePublishRequestList) error
}

var _ prometheus.Collector = &VMPublishCollector{}

// VMPublishCollector reports the VM publish request metric by walking the
// informer cache when Prometheus scrapes. It is the scrapeEnabled counterpart
// to VMPublishMetrics; see the package-level comments for how the two relate.
type VMPublishCollector struct {
	lister vmPubLister
	logger logr.Logger

	// ready reports whether the cache backing the lister has synced.
	ready atomic.Bool

	publishRequest *prometheus.Desc
}

// NewVMPublishCollector returns a collector that reports the publish-request
// metric for the VirtualMachinePublishRequests returned by lister.
func NewVMPublishCollector(lister vmPubLister, logger logr.Logger) *VMPublishCollector {
	return &VMPublishCollector{
		lister: lister,
		logger: logger,

		publishRequest: prometheus.NewDesc(
			prometheus.BuildFQName(metricsNamespace, "vm", "publish_request"),
			"VirtualMachine publish request result",
			[]string{vmPubNameLabel, vmPubNamespaceLabel},
			nil,
		),
	}
}

// MarkReady tells the collector its lister is usable. Until it is called,
// Collect reports nothing.
func (c *VMPublishCollector) MarkReady() {
	c.ready.Store(true)
}

// Describe implements prometheus.Collector.
func (c *VMPublishCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.publishRequest
}

// Collect implements prometheus.Collector.
func (c *VMPublishCollector) Collect(ch chan<- prometheus.Metric) {
	if !c.ready.Load() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()

	var list vmopv1.VirtualMachinePublishRequestList
	if err := c.lister.List(ctx, &list); err != nil {
		// Report nothing rather than returning an error; see VMCollector's
		// Collect for why.
		c.logger.Error(err, "Failed to list VirtualMachinePublishRequests for metrics")
		return
	}

	for i := range list.Items {
		vmPub := &list.Items[i]
		ch <- prometheus.MustNewConstMetric(
			c.publishRequest,
			prometheus.GaugeValue,
			float64(publishResultFor(vmPub)),
			vmPub.Name, vmPub.Namespace)
	}
}

// publishResultFor derives the publish result entirely from the persisted
// Complete condition; see the package-level comment for the one case this
// cannot reconstruct.
func publishResultFor(vmPub *vmopv1.VirtualMachinePublishRequest) PublishResult {
	c := conditions.Get(vmPub, vmopv1.VirtualMachinePublishRequestConditionComplete)
	switch {
	case c == nil:
		return PublishInProgress
	case c.Status == metav1.ConditionTrue:
		return PublishSucceeded
	case c.Reason == vmopv1.FatalReason:
		return PublishFailed
	default:
		return PublishInProgress
	}
}

// AddVMPublishCollectorToManager registers the VM publish request metrics
// collector with the controller-runtime metrics registry, reading from the
// manager's cache. The caller is responsible for only calling this when
// scraping is enabled; see the package-level comment.
func AddVMPublishCollectorToManager(mgr ctrlmgr.Manager) error {
	c := NewVMPublishCollector(
		cachedVMPublishLister{reader: mgr.GetClient()},
		mgr.GetLogger().WithName("vmpublishmetrics"))

	return mgr.Add(ctrlmgr.RunnableFunc(func(ctx context.Context) error {
		if err := metrics.Registry.Register(c); err != nil {
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

// cachedVMPublishLister lists VirtualMachinePublishRequests out of the
// controller-runtime cache without copying them.
type cachedVMPublishLister struct {
	reader ctrlclient.Reader
}

func (l cachedVMPublishLister) List(
	ctx context.Context,
	list *vmopv1.VirtualMachinePublishRequestList) error {

	return l.reader.List(ctx, list, ctrlclient.UnsafeDisableDeepCopy)
}
