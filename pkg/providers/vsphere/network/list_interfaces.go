// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	netsetutil "github.com/vmware-tanzu/vm-operator/pkg/util/kube/networksettings"
)

// ListNetworkInterfaces lists the network interfaces that VM Operator created
// and owns for the VM. External network interfaces are never included, even
// when the VM owns them, such as after an interface was switched from managed
// to external, because VM Operator never deletes those.
func ListNetworkInterfaces(
	vmCtx pkgctx.VirtualMachineContext,
	client ctrlclient.Client,
) ([]ctrlclient.Object, error) {

	networkTypes, err := netsetutil.GetSupportedProviderTypes(vmCtx, client, vmCtx.VM.Namespace)
	if err != nil {
		return nil, err
	}

	objs, err := listVMNetworkInterfaces(vmCtx, client, vmCtx.VM, networkTypes, false)
	if err != nil {
		return nil, err
	}

	var managed []ctrlclient.Object
	for _, o := range objs {
		if !IsExternalNetworkInterface(o.obj) && isOwnedBy(vmCtx.VM, o.obj) {
			managed = append(managed, o.obj)
		}
	}

	return managed, nil
}

// RetireOutcome is the outcome of retiring one provider resource.
type RetireOutcome string

const (
	// RetireOutcomeDeleted means the provider resource was deleted, or was
	// already gone.
	RetireOutcomeDeleted RetireOutcome = "Deleted"

	// RetireOutcomeReleased means the external network interface was
	// released: its VM association metadata was removed, or it was already
	// gone. VM Operator never deletes an external network interface.
	RetireOutcomeReleased RetireOutcome = "Released"

	// RetireOutcomeRetained means the provider resource was deliberately kept
	// on this pass. A later pass that observes a change re-evaluates it.
	RetireOutcomeRetained RetireOutcome = "Retained"

	// RetireOutcomeFailed means the provider resource should have been
	// deleted, but a read or the delete failed. It is retried after a bounded
	// delay.
	RetireOutcomeFailed RetireOutcome = "Failed"
)

// Reasons that RetireProviderResources reports.
const (
	// RetireReasonDeleted is used when the provider resource was deleted or
	// was already gone.
	RetireReasonDeleted = "Deleted"

	// RetireReasonReleased is used when the external network interface was
	// released or was already gone.
	RetireReasonReleased = "Released"

	// RetireReasonObservationIncomplete is used when a read needed to observe
	// the network failed, so no resource is known to be unused.
	RetireReasonObservationIncomplete = "ObservationIncomplete"

	// RetireReasonDesiredUnknown is used when a desired interface has no
	// provider resource name, so the desired set is not known.
	RetireReasonDesiredUnknown = "DesiredResourcesUnknown"

	// RetireReasonHardwareChangeSubmitted is used when network device changes
	// were submitted on this pass, so the observed hardware is stale.
	RetireReasonHardwareChangeSubmitted = "HardwareChangeSubmitted"

	// RetireReasonAttachmentInUse is used when an adapter carries the
	// attachment the provider resource last reported.
	RetireReasonAttachmentInUse = "AttachmentInUse"

	// RetireReasonAttachmentAmbiguous is used when an adapter shares the
	// provider resource's network backing, but no identity evidence, such as
	// an external ID or a MAC address, shows whether the adapter uses this
	// resource's allocation.
	RetireReasonAttachmentAmbiguous = "AttachmentAmbiguous"

	// RetireReasonAttachmentUnknown is used when the provider resource
	// reports no attachment while some adapter is not claimed by a desired
	// interface.
	RetireReasonAttachmentUnknown = "AttachmentUnknown"

	// RetireReasonDesiredChanged is used when the VirtualMachine changed
	// after the network was observed, so retirement waits for the pass that
	// the change triggers.
	RetireReasonDesiredChanged = "DesiredChanged"

	// RetireReasonRecheckFailed is used when the VirtualMachine could not be
	// read again just before the delete.
	RetireReasonRecheckFailed = "RecheckFailed"

	// RetireReasonDeleteFailed is used when the delete failed, including when
	// its preconditions did not hold.
	RetireReasonDeleteFailed = "DeleteFailed"

	// RetireReasonReleaseFailed is used when releasing an external network
	// interface failed, including when it changed since it was observed.
	RetireReasonReleaseFailed = "ReleaseFailed"
)

// RetireResult is the outcome of retiring one VM-owned provider resource or
// associated external network interface that no desired interface
// references.
type RetireResult struct {
	// Ref identifies the provider resource.
	Ref ProviderRef

	// Outcome is what happened to the provider resource.
	Outcome RetireOutcome

	// Reason is a stable reason.
	Reason string

	// Message is a human-readable message.
	Message string

	// Adapter is the device key of the adapter that retains the resource. It
	// is zero when no single adapter does.
	Adapter int32

	// Err is set only when Outcome is Failed.
	Err error
}

// RetireInput holds the inputs of RetireProviderResources.
type RetireInput struct {
	// Client reads the VirtualMachine again, deletes provider resources, and
	// releases external network interfaces.
	Client ctrlclient.Client

	// VM is the VirtualMachine the observation was made for.
	VM *vmopv1.VirtualMachine

	// Observed is the pass's current network observation. It must have been
	// made after the pass's schema upgrade and snapshot revert, from fresh
	// hardware.
	Observed *Info

	// Submitted is true when network device changes were submitted on this
	// pass.
	Submitted bool
}

// RetireProviderResources deletes the VM-owned provider resources, and
// releases the external network interfaces associated with the VM, that no
// desired interface references and that no adapter may still use, and
// returns one result per such resource, in listing order. It never returns
// an error: a failure is a Failed result, which RetireRequeueAfter turns into
// a bounded retry.
//
// The desired set holds the provider resource of every interface in the
// spec, including the Pending, Failed, and Unknown ones, keyed by provider
// type and name. A resource outside it is a candidate, and is deleted only
// when all of these hold:
//
//   - The observation is complete, no network device change was submitted on
//     this pass, and every desired interface has a provider resource name.
//     Otherwise every candidate is retained.
//   - No adapter carries the attachment the candidate last reported, judged
//     by provider-specific identity evidence: the external ID, and for a
//     NetOP NetworkInterface without one, the backing together with the MAC
//     address. An adapter that only shares the candidate's backing retains
//     it as ambiguous. This holds whatever the adapter's disposition, so a
//     claimed adapter whose attachment is pending replacement, and a
//     protected or retiring adapter, retain the resource they use.
//   - A candidate that reports no attachment is retained while any adapter is
//     not claimed by a desired interface.
//   - Just before the delete, the VirtualMachine is read again and still has
//     the UID and generation the observation was made for. A spec change made
//     in the meantime, such as a change back to the old network, cancels the
//     retirement until the pass that change triggers.
//
// The delete is conditioned on the observed UID and resource version, so a
// newer object that reused the name is never deleted. A NotFound delete is a
// success.
//
// An external network interface is subject to the same rules, but it is
// released instead of deleted: the VM association labels and annotations are
// removed with an optimistic lock on the observed resource version, so the
// release never strips the association of another VM that has since claimed
// it. A NotFound release is a success.
func RetireProviderResources(
	ctx context.Context,
	in RetireInput) []RetireResult {

	info := in.Observed
	if info == nil {
		return nil
	}

	candidates := retirementCandidates(info)
	if len(candidates) == 0 {
		return nil
	}

	results := make([]RetireResult, 0, len(candidates))

	retainAll := func(reason, message string) []RetireResult {
		for _, obj := range candidates {
			results = append(results, RetireResult{
				Ref:     refFromObject(providerTypeOf(obj), obj),
				Outcome: RetireOutcomeRetained,
				Reason:  reason,
				Message: message,
			})
		}
		return results
	}

	switch {
	case !info.Complete:
		return retainAll(RetireReasonObservationIncomplete,
			"the network observation is incomplete")
	case in.Submitted:
		return retainAll(RetireReasonHardwareChangeSubmitted,
			"network device changes were submitted and are not observed yet")
	}
	for _, iface := range info.Interfaces {
		if iface.Provider.Ref.Name == "" {
			return retainAll(RetireReasonDesiredUnknown, fmt.Sprintf(
				"network interface %q has no provider resource", iface.Spec.Name))
		}
	}

	for _, obj := range candidates {
		ref := refFromObject(providerTypeOf(obj), obj)

		if r, retained := retainedByHardware(info, ref, obj); retained {
			results = append(results, r)
			continue
		}

		results = append(results, retire(ctx, in, ref, obj))
	}

	return results
}

// RetireRequeueAfter returns the bounded retry interval that the results ask
// for, or zero. Only a failed retirement needs one. A retained resource is
// re-evaluated on the pass that the hardware, spec, or provider change that
// releases it triggers, so it does not poll.
func RetireRequeueAfter(results []RetireResult) time.Duration {
	for _, r := range results {
		if r.Outcome == RetireOutcomeFailed {
			return PendingRequeueAfter
		}
	}
	return 0
}

// retirementCandidates returns the VM-owned provider resources and the
// associated external network interfaces that no desired interface
// references by provider type and name.
func retirementCandidates(info *Info) []ctrlclient.Object {
	return orphanedObjects(info)
}

// retainedByHardware returns the Retained result for a candidate that an
// observed adapter may still use.
func retainedByHardware(
	info *Info,
	ref ProviderRef,
	obj ctrlclient.Object) (RetireResult, bool) {

	var (
		ambiguous    *ObservedAdapter
		reportsNone  bool
		unclaimedKey int32
	)

	for i := range info.Adapters {
		a := info.Adapters[i]
		switch attachmentEvidence(obj, a) {
		case evidenceInUse:
			return RetireResult{
				Ref:     ref,
				Outcome: RetireOutcomeRetained,
				Reason:  RetireReasonAttachmentInUse,
				Message: fmt.Sprintf("adapter %d uses the attachment of %s/%s",
					a.Key, ref.Namespace, ref.Name),
				Adapter: a.Key,
			}, true
		case evidenceAmbiguous:
			if ambiguous == nil {
				ambiguous = &a
			}
		case evidenceUnknown:
			reportsNone = true
			if unclaimedKey == 0 && info.Disposition[a.Key] != AdapterDispositionClaimed {
				unclaimedKey = a.Key
			}
		case evidenceNone:
		}
	}

	switch {
	case ambiguous != nil:
		return RetireResult{
			Ref:     ref,
			Outcome: RetireOutcomeRetained,
			Reason:  RetireReasonAttachmentAmbiguous,
			Message: fmt.Sprintf(
				"adapter %d shares the network backing of %s/%s and no identity "+
					"evidence shows whether it uses this resource's allocation",
				ambiguous.Key, ref.Namespace, ref.Name),
			Adapter: ambiguous.Key,
		}, true
	case reportsNone && unclaimedKey != 0:
		return RetireResult{
			Ref:     ref,
			Outcome: RetireOutcomeRetained,
			Reason:  RetireReasonAttachmentUnknown,
			Message: fmt.Sprintf(
				"%s/%s reports no attachment and adapter %d is not claimed "+
					"by a network interface",
				ref.Namespace, ref.Name, unclaimedKey),
			Adapter: unclaimedKey,
		}, true
	}

	return RetireResult{}, false
}

// attachmentEvidenceKind is what an adapter shows about whether it uses a
// provider resource's attachment.
type attachmentEvidenceKind uint8

const (
	// evidenceNone means the adapter does not use the attachment.
	evidenceNone attachmentEvidenceKind = iota

	// evidenceInUse means identity evidence ties the adapter to the
	// attachment.
	evidenceInUse

	// evidenceAmbiguous means the adapter shares the attachment's backing,
	// but no identity evidence decides whether it uses the attachment.
	evidenceAmbiguous

	// evidenceUnknown means the provider resource reports no attachment.
	evidenceUnknown
)

// attachmentEvidence compares the attachment a provider resource last
// reported with an observed adapter.
func attachmentEvidence(
	obj ctrlclient.Object,
	a ObservedAdapter) attachmentEvidenceKind {

	adapterExternalID := ""
	if a.Attachment.ExternalID.Op == IntentSet {
		adapterExternalID = a.Attachment.ExternalID.Value
	}

	byExternalID := func(id string) attachmentEvidenceKind {
		switch id {
		case "":
			return evidenceUnknown
		case adapterExternalID:
			return evidenceInUse
		default:
			return evidenceNone
		}
	}

	switch o := obj.(type) {
	case *netopv1alpha1.NetworkInterface:
		st := o.Status
		switch {
		case st.ExternalID != "":
			return byExternalID(st.ExternalID)
		case st.NetworkID == "":
			return evidenceUnknown
		case adapterExternalID != "":
			// NetOP sets the external ID only on newly created resources, so
			// an adapter with one belongs to another resource.
			return evidenceNone
		}

		b := a.Attachment.Backing
		if (b.Kind != BackingKindDVPortgroup && b.Kind != BackingKindNetwork) ||
			b.ID != st.NetworkID {
			return evidenceNone
		}
		if st.MacAddress != "" {
			if strings.EqualFold(st.MacAddress, a.MAC) {
				return evidenceInUse
			}
			return evidenceNone
		}
		return evidenceAmbiguous

	case *ncpv1alpha1.VirtualNetworkInterface:
		return byExternalID(o.Status.InterfaceID)

	case *vpcv1alpha1.SubnetPort:
		return byExternalID(o.Status.Attachment.ID)
	}

	return evidenceUnknown
}

// retire re-checks the VirtualMachine and deletes one candidate, or releases
// it when it is an external network interface.
func retire(
	ctx context.Context,
	in RetireInput,
	ref ProviderRef,
	obj ctrlclient.Object) RetireResult {

	result := RetireResult{Ref: ref}

	retained := func(reason, message string) RetireResult {
		result.Outcome = RetireOutcomeRetained
		result.Reason = reason
		result.Message = message
		return result
	}
	failed := func(reason string, err error) RetireResult {
		result.Outcome = RetireOutcomeFailed
		result.Reason = reason
		result.Message = err.Error()
		result.Err = err
		return result
	}

	// Re-check the desired identity just before the delete. The observation
	// was made from the VirtualMachine this pass started with; a spec change
	// made since then may reference this resource again.
	current := &vmopv1.VirtualMachine{}
	if err := in.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(in.VM), current); err != nil {
		if apierrors.IsNotFound(err) {
			// The VirtualMachine is gone. Garbage collection deletes the
			// resources it owns, and the delete path releases the external
			// network interfaces.
			return retained(RetireReasonDesiredChanged,
				"the VirtualMachine no longer exists")
		}
		return failed(RetireReasonRecheckFailed, fmt.Errorf(
			"failed to get VirtualMachine before deleting %s/%s: %w",
			ref.Namespace, ref.Name, err))
	}
	if current.UID != in.Observed.Identity.VMUID ||
		current.Generation != in.Observed.Identity.Generation {

		return retained(RetireReasonDesiredChanged,
			"the VirtualMachine changed after the network was observed")
	}

	if IsExternalNetworkInterface(obj) {
		if err := ReleaseExternalNetworkInterface(ctx, in.Client, obj); err != nil {
			return failed(RetireReasonReleaseFailed, err)
		}

		result.Outcome = RetireOutcomeReleased
		result.Reason = RetireReasonReleased
		result.Message = fmt.Sprintf("released %s/%s", ref.Namespace, ref.Name)
		return result
	}

	uid, rv := obj.GetUID(), obj.GetResourceVersion()
	opts := &ctrlclient.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	}
	if rv != "" {
		opts.Preconditions.ResourceVersion = &rv
	}

	if err := in.Client.Delete(ctx, obj, opts); err != nil && !apierrors.IsNotFound(err) {
		return failed(RetireReasonDeleteFailed, fmt.Errorf(
			"failed to delete %s/%s: %w", ref.Namespace, ref.Name, err))
	}

	result.Outcome = RetireOutcomeDeleted
	result.Reason = RetireReasonDeleted
	result.Message = fmt.Sprintf("deleted %s/%s", ref.Namespace, ref.Name)
	return result
}

// qualifiedInterfaceKey returns a unique key for a network interface CR by
// combining its provider type and name. This ensures that CRs with the same
// name from different network providers are treated as distinct items.
func qualifiedInterfaceKey(providerType pkgcfg.NetworkProviderType, name string) string {
	return string(providerType) + "/" + name
}

// listExternalNetworkInterfaces lists all the external network interfaces
// associated with the VM, for the network providers enabled in the cluster.
// The cluster's provider types are used instead of the VM namespace's
// NetworkSettings so that this works while the namespace is being deleted.
// When the cluster's provider types are not known, every provider type is
// listed, skipping any whose CRD is not installed.
func listExternalNetworkInterfaces(
	ctx context.Context,
	client ctrlclient.Client,
	vm *vmopv1.VirtualMachine,
) ([]ctrlclient.Object, error) {

	networkTypes := netsetutil.GetClusterSupportedProviderTypesFromConfig(ctx)
	if len(networkTypes) == 0 {
		networkTypes = []pkgcfg.NetworkProviderType{
			pkgcfg.NetworkProviderTypeVDS,
			pkgcfg.NetworkProviderTypeNSXT,
			pkgcfg.NetworkProviderTypeVPC,
		}
	}

	objs, err := listVMNetworkInterfaces(ctx, client, vm, networkTypes, true)
	if err != nil {
		return nil, err
	}

	var external []ctrlclient.Object
	for _, o := range objs {
		if IsExternalNetworkInterface(o.obj) {
			external = append(external, o.obj)
		}
	}

	return external, nil
}

type providerNetworkInterface struct {
	providerType pkgcfg.NetworkProviderType
	obj          ctrlclient.Object
}

// listVMNetworkInterfaces lists the network interface CRs labeled with the
// VM's name for each of the given network provider types. When ignoreNoMatch
// is true, a provider whose CRD is not installed is skipped.
func listVMNetworkInterfaces(
	ctx context.Context,
	client ctrlclient.Client,
	vm *vmopv1.VirtualMachine,
	networkTypes []pkgcfg.NetworkProviderType,
	ignoreNoMatch bool,
) ([]providerNetworkInterface, error) {

	// TODO: Anything v1a2 or later will have the label. Maybe check the created at annotation
	// value and do a more exhaustive list if this is an old VM.
	listOpts := []ctrlclient.ListOption{
		ctrlclient.InNamespace(vm.Namespace),
		ctrlclient.MatchingLabels{pkgconst.NetworkInterfaceVMNameLabel: vm.Name},
	}

	list := func(l ctrlclient.ObjectList) (bool, error) {
		if err := client.List(ctx, l, listOpts...); err != nil {
			if ignoreNoMatch && meta.IsNoMatchError(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	var objs []providerNetworkInterface

	if slices.Contains(networkTypes, pkgcfg.NetworkProviderTypeVDS) {
		var l netopv1alpha1.NetworkInterfaceList
		if ok, err := list(&l); err != nil {
			return nil, err
		} else if ok {
			for i := range l.Items {
				objs = append(objs, providerNetworkInterface{pkgcfg.NetworkProviderTypeVDS, &l.Items[i]})
			}
		}
	}

	if slices.Contains(networkTypes, pkgcfg.NetworkProviderTypeNSXT) {
		var l ncpv1alpha1.VirtualNetworkInterfaceList
		if ok, err := list(&l); err != nil {
			return nil, err
		} else if ok {
			for i := range l.Items {
				objs = append(objs, providerNetworkInterface{pkgcfg.NetworkProviderTypeNSXT, &l.Items[i]})
			}
		}
	}

	if slices.Contains(networkTypes, pkgcfg.NetworkProviderTypeVPC) {
		var l vpcv1alpha1.SubnetPortList
		if ok, err := list(&l); err != nil {
			return nil, err
		} else if ok {
			for i := range l.Items {
				objs = append(objs, providerNetworkInterface{pkgcfg.NetworkProviderTypeVPC, &l.Items[i]})
			}
		}
	}

	return objs, nil
}

func isOwnedBy(vm *vmopv1.VirtualMachine, object ctrlclient.Object) bool {
	for _, ref := range object.GetOwnerReferences() {
		if ref.UID == vm.UID {
			return true
		}
	}
	return false
}
