// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"errors"
	"fmt"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	pkgerr "github.com/vmware-tanzu/vm-operator/pkg/errors"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/vmlifecycle"
)

// networkState returns the network reconcile state carried by vmCtx. When
// vmCtx carries none, for example when the session is driven directly, it
// allocates one and returns a context that carries it. The state allocated
// here is not visible to the caller of UpdateVirtualMachine, so a caller that
// gates on network readiness, such as the power-on gate, still sees no state
// and does not power on the VM.
func networkState(
	vmCtx pkgctx.VirtualMachineContext) (pkgctx.VirtualMachineContext, *network.ReconcileState) {

	if st, ok := network.FromContext(vmCtx); ok {
		return vmCtx, st
	}

	st := &network.ReconcileState{Policy: network.PolicyFromContext(vmCtx)}
	vmCtx.Context = network.WithContext(vmCtx.Context, st)
	return vmCtx, st
}

// setEmptyNetworkPlan gives a reconfigure that does not reconcile the network,
// e.g. of a suspended, paused, or task-busy VM, a plan without device changes
// so the contributors in doReconfigure always find a plan. The empty plan
// describes no observation, so it never makes the network ready.
func setEmptyNetworkPlan(st *network.ReconcileState) {
	st.Plan = &network.Plan{}
}

// reconcileNetwork prepares the VM's network provider resources, plans the
// network device changes, and stores the observation and the plan on st. It
// never waits for a provider resource: an interface whose provider resource
// is not ready, or whose provider reported a failure, is recorded as data on
// the observation and the plan, and the plan defers its hardware change.
//
// It returns only the genuine I/O failures, joined, after the plan is stored,
// so the caller can still apply the independent changes of the pass. It also
// records on st the bounded retry the pass should end with.
func (s *Session) reconcileNetwork(
	vmCtx pkgctx.VirtualMachineContext,
	st *network.ReconcileState) error {

	vmCtx.Logger.V(4).Info("Reconciling network interfaces")

	// This collection follows schema upgrade and snapshot revert, so it
	// replaces the observation made before status. A previous plan never
	// carries over.
	st.Plan = nil

	info, prepareErr := network.Prepare(vmCtx, network.PrepareInput{
		Client:       s.K8sClient,
		VM:           vmCtx.VM,
		MoVM:         vmCtx.MoVM,
		VimClient:    s.Client.VimClient(),
		Finder:       s.Finder,
		ClusterMoRef: &s.ClusterMoRef,
	})
	st.Observed = info

	backings, backingErr := network.EthernetCardBackings(vmCtx, vmCtx.VM, info)

	st.Plan = network.BuildPlan(info, network.PlanInput{
		Policy:     st.Policy,
		PowerState: vmCtx.MoVM.Runtime.PowerState,
		// Both powered-off dispatch paths, update and resize, apply the same
		// planned changes, regardless of VMResizeCPUMemory and of
		// MutableNetworks.
		ApplyChanges:    true,
		MutableNetworks: pkgcfg.FromContext(vmCtx).Features.MutableNetworks,
		UnitNumbers:     pkgcfg.FromContext(vmCtx).Features.VMNetworkUnitNumbers,
		CardBackings:    backings,
	})

	if d := network.PendingRetryAfter(info, st.Plan); d > st.RequeueAfter {
		st.RequeueAfter = d
	}

	if l := vmCtx.Logger.V(4); l.Enabled() {
		for _, a := range st.Plan.Actions {
			l.Info("Planned network action",
				"interface", a.Interface,
				"adapter", a.Adapter,
				"kind", a.Kind,
				"proposed", a.Proposed,
				"reason", a.Reason,
				"message", a.Message)
		}
	}

	var errs []error
	if prepareErr != nil {
		errs = append(errs, fmt.Errorf("failed to prepare network interfaces: %w", prepareErr))
	}
	if backingErr != nil {
		errs = append(errs, fmt.Errorf("failed to resolve network backings: %w", backingErr))
	}

	err := errors.Join(errs...)
	if err != nil && pkgerr.IsNoRequeueError(err) {
		// A NoRequeueError would end the pass before power state, which is
		// what treating network waits as data prevents, so keep only its
		// message.
		err = fmt.Errorf("failed to reconcile network interfaces: %s", err.Error())
	}
	return err
}

// addNetworkChanges appends the planned network device changes to configSpec.
// It is the one way each dispatch site, i.e. poweredOnReconfigure,
// getConfigSpecForPoweredOffVM, and resizeVMWhenPoweredStateOff, applies the
// plan, so all of them apply the same plan. The specs refer to the plan's own
// devices, so a later contributor to the same reconfigure that edits a device
// through Plan.DeviceChanges edits the device that is submitted.
//
// When it appends any change, it marks the changes as submitted, because the
// reconfigure that follows makes the observed hardware stale.
func addNetworkChanges(
	vmCtx pkgctx.VirtualMachineContext,
	configSpec *vimtypes.VirtualMachineConfigSpec) {

	st, ok := network.FromContext(vmCtx)
	if !ok || st.Plan == nil {
		return
	}

	specs := st.Plan.DeviceChanges().Specs()
	if len(specs) == 0 {
		return
	}

	vmCtx.Logger.Info("Applying network device changes",
		"numDeviceChanges", len(specs))

	configSpec.DeviceChange = append(configSpec.DeviceChange, specs...)
	st.Submitted = true
}

// setNetworkReadyCondition writes the VM's VirtualMachineNetworkReady
// condition from the derived network readiness, through the same helper the
// create path uses.
func setNetworkReadyCondition(
	vmCtx pkgctx.VirtualMachineContext,
	st *network.ReconcileState) {

	if st.Observed == nil {
		return
	}

	hasInterfaces := st.Observed.Enabled && len(st.Observed.Interfaces) > 0
	readiness := st.Readiness(vmCtx)

	var pending []network.PendingInterface
	if !readiness.Ready() {
		pending = readiness.Pending
	}

	network.SetReadyCondition(vmCtx.VM, hasInterfaces, pending, nil)
}

// finishNetwork runs when the pass submitted no reconfigure task. It deletes
// the provider resources that no desired interface references any longer,
// and then bootstraps the guest, but only when the network is ready.
//
// Bootstrap is built from the observation: each interface's provider
// resource, its spec, and the MAC address of its observed adapter. While the
// network is not ready, e.g. because an interface's provider resource is
// pending or because a change to a running VM's adapters is deferred until
// the VM is powered off, bootstrap does not run at all. In particular, a
// running Cloud-Init VM's guestinfo is not rewritten to describe an adapter
// that does not exist yet, and other Cloud-Init input changes made in that
// window are held back with it until the network is ready.
func (s *Session) finishNetwork(
	vmCtx pkgctx.VirtualMachineContext,
	vcVM *object.VirtualMachine,
	st *network.ReconcileState,
	bootstrapData vmlifecycle.BootstrapData) error {

	readiness := st.Readiness(vmCtx)
	if !readiness.Ready() {
		vmCtx.Logger.Info("Skipping bootstrap because the network is not ready",
			"pending", readiness.PendingNames())
		return nil
	}

	s.deleteOrphanedNetworkInterfaces(vmCtx, st)

	bootstraps, err := network.BootstrapsFromInfo(vmCtx, vmCtx.VM, st.Observed)
	if err != nil {
		return err
	}

	// Get the information required to bootstrap/customize the VM. This is
	// retrieved outside of the customize/DoBootstrap call path in order to use
	// the information to update the VM object's status with the resolved,
	// intended network configuration.
	bootstrapArgs, err := vmlifecycle.GetBootstrapArgs(
		vmCtx,
		s.K8sClient,
		bootstraps,
		bootstrapData)
	if err != nil {
		return err
	}

	// Update the Kubernetes VM object's status with the resolved, intended
	// network configuration.
	vmlifecycle.UpdateNetworkStatusConfig(vmCtx.VM, bootstrapArgs)

	return vmlifecycle.DoBootstrap(
		vmCtx,
		vcVM,
		vmCtx.MoVM.Config,
		bootstrapArgs)
}

// deleteOrphanedNetworkInterfaces deletes the VM-owned provider resources that
// no desired interface references, as the update path has done after a pass
// that changed nothing. It runs only with MutableNetworks while the VM is
// powered off and the network is ready, so no adapter can still use such a
// resource. Failures are logged, as before.
//
// TODO(T021): Replace this with RetireProviderResources, which deletes with
// UID preconditions and retries failures.
func (s *Session) deleteOrphanedNetworkInterfaces(
	vmCtx pkgctx.VirtualMachineContext,
	st *network.ReconcileState) {

	if !pkgcfg.FromContext(vmCtx).Features.MutableNetworks ||
		vmCtx.MoVM.Runtime.PowerState != vimtypes.VirtualMachinePowerStatePoweredOff {
		return
	}

	for _, obj := range network.OrphanedNetworkInterfaces(st.Observed) {
		if err := s.K8sClient.Delete(vmCtx, obj); ctrlclient.IgnoreNotFound(err) != nil {
			vmCtx.Logger.Error(err, "failed to delete orphaned network interface",
				"name", obj.GetName())
		}
	}
}

// joinNetworkErr returns err joined with the network I/O error of the pass. A
// NoRequeueError, such as the reconfigure barrier, is returned as it is so it
// keeps its meaning, and a persistent network error recurs on the next pass.
func joinNetworkErr(
	vmCtx pkgctx.VirtualMachineContext,
	err, networkErr error) error {

	if networkErr == nil {
		return err
	}
	if err != nil && pkgerr.IsNoRequeueError(err) {
		vmCtx.Logger.Error(networkErr, "Network reconciliation error")
		return err
	}
	return errors.Join(err, networkErr)
}
