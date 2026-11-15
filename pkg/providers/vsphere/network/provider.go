// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	pkgerr "github.com/vmware-tanzu/vm-operator/pkg/errors"
	pkglog "github.com/vmware-tanzu/vm-operator/pkg/log"
)

// Stable reasons for a Resolution whose phase is not Ready.
const (
	// ReasonProviderResourceNotFound is used when the provider resource for
	// an interface does not exist yet.
	ReasonProviderResourceNotFound = "ProviderResourceNotFound"

	// ReasonProviderNotReady is used when the provider resource is not ready.
	ReasonProviderNotReady = "ProviderNotReady"

	// ReasonProviderStatusIncomplete is used when the provider resource is
	// ready but its status lacks data required to attach the interface.
	ReasonProviderStatusIncomplete = "ProviderStatusIncomplete"

	// ReasonProviderFailed is used when the provider reports a failure.
	ReasonProviderFailed = "ProviderFailed"

	// ReasonInvalidInterfaceSpec is used when the interface spec cannot be
	// mapped to a supported provider resource.
	ReasonInvalidInterfaceSpec = "InvalidInterfaceSpec"

	// ReasonNoNetworkProvider is used when no network provider is configured.
	ReasonNoNetworkProvider = "NoNetworkProvider"

	// ReasonBackingNotSupported is used when the provider resource names a
	// network backing that is not supported.
	ReasonBackingNotSupported = "BackingNotSupported"

	// ReasonReadFailed is used when reading the provider resource failed.
	ReasonReadFailed = "ReadFailed"

	// ReasonEnsureFailed is used when creating or patching the provider
	// resource failed.
	ReasonEnsureFailed = "EnsureFailed"

	// ReasonBackingResolutionFailed is used when resolving the vSphere
	// network backing of a ready target failed.
	ReasonBackingResolutionFailed = "BackingResolutionFailed"
)

// ObserveInput is the input to Observe.
type ObserveInput struct {
	// Client is used to read the provider resources.
	Client ctrlclient.Client

	// VM is the VirtualMachine being reconciled.
	VM *vmopv1.VirtualMachine

	// MoVM is the vSphere VM already fetched by the update path.
	MoVM mo.VirtualMachine
}

// PrepareInput is the input to Prepare.
type PrepareInput struct {
	// Client is used to read, create, and patch the provider resources.
	Client ctrlclient.Client

	// VM is the VirtualMachine being reconciled.
	VM *vmopv1.VirtualMachine

	// MoVM is the vSphere VM already fetched by the update path.
	MoVM mo.VirtualMachine

	// VimClient is used to construct the network backing references.
	VimClient *vim25.Client

	// Finder is used to find named networks on the named-network test path.
	Finder *find.Finder

	// ClusterMoRef is the VM's cluster. When set, an NSX logical switch is
	// resolved to that cluster's distributed portgroup; otherwise an opaque
	// network backing is used.
	ClusterMoRef *vimtypes.ManagedObjectReference
}

// Observe reads the VM's network provider resources and virtual ethernet
// cards and returns a normalized snapshot. It performs no writes and no
// vCenter I/O: it lists the VM-owned provider resources, gets each desired
// interface's provider resource by its expected name without creating it,
// normalizes the adapters from MoVM, and matches them with MatchInterfaces. A
// read failure sets Info.Complete to
// false and that interface's phase to Unknown; it is never returned as an
// error so that status and shutdown still run.
func Observe(ctx context.Context, in ObserveInput) *Info {
	c := collector{
		client: in.Client,
		vm:     in.VM,
		moVM:   in.MoVM,
	}
	info, _ := c.collect(ctx)
	return info
}

// Prepare ensures the provider resource for every desired interface exists
// and is up to date, and returns a snapshot built by the same collection and
// normalization code as Observe. It consumes the objects returned by the
// create or patch calls directly, and resolves the vSphere network backing
// of each Ready target. Its snapshot is matched with MatchInterfaces like
// Observe's. It never waits for a provider resource to become
// ready: a resource that is not ready yields a Pending interface.
//
// Per-interface outcomes, including failures, are retained on the returned
// Info. The returned error joins only the I/O failures, i.e. the Unknown
// interfaces and a failure to list the VM-owned provider resources. The
// returned Info is never nil.
func Prepare(ctx context.Context, in PrepareInput) (*Info, error) {
	ctx = pkgctx.WithVCOpID(ctx, in.VM, "prepareNetwork")

	c := collector{
		client:       in.Client,
		vm:           in.VM,
		moVM:         in.MoVM,
		ensure:       true,
		vimClient:    in.VimClient,
		finder:       in.Finder,
		clusterMoRef: in.ClusterMoRef,
	}
	info, errs := c.collect(ctx)
	return info, errors.Join(errs...)
}

// collector holds the inputs shared by Observe and Prepare. When ensure is
// false, it only reads; when true, it also creates or patches the provider
// resources and resolves the backings of Ready targets.
type collector struct {
	client ctrlclient.Client
	vm     *vmopv1.VirtualMachine
	moVM   mo.VirtualMachine

	ensure       bool
	vimClient    *vim25.Client
	finder       *find.Finder
	clusterMoRef *vimtypes.ManagedObjectReference
}

func (c collector) collect(ctx context.Context) (*Info, []error) {
	var errs []error

	info := &Info{
		Identity: observationIdentity(c.vm, c.moVM),
		Complete: true,
	}

	adapters, ok := observeAdapters(c.moVM)
	if !ok {
		info.Complete = false
	}
	info.Adapters = adapters

	named, providerErr := c.providerMode(ctx)

	if networkSpec := c.vm.Spec.Network; networkSpec != nil && !networkSpec.Disabled {
		info.Enabled = true
		info.Interfaces = make([]InterfaceState, 0, len(networkSpec.Interfaces))

		for i := range networkSpec.Interfaces {
			spec := networkSpec.Interfaces[i].DeepCopy()

			var r Resolution
			switch {
			case providerErr != nil:
				r = failed(ProviderRef{Namespace: c.vm.Namespace},
					ReasonNoNetworkProvider, providerErr)
			case named:
				r = c.namedResolution(ctx, *spec)
			default:
				r = c.providerResolution(ctx, *spec)
			}

			if r.Phase == ResolutionUnknown {
				info.Complete = false
				errs = append(errs,
					fmt.Errorf("network interface %q: %w", spec.Name, r.Err))
			}

			info.Interfaces = append(info.Interfaces, InterfaceState{
				Spec:             *spec,
				Provider:         r,
				Match:            Match{Kind: MatchNone},
				AttachmentChange: AttachmentChangeUnknown,
			})
		}
	}

	// List the VM-owned provider resources after any were ensured so the
	// snapshot includes the resources created by Prepare.
	if !named && providerErr == nil {
		owned, err := c.listOwned(ctx)
		if err != nil {
			info.Complete = false
			errs = append(errs,
				fmt.Errorf("failed to list network interfaces: %w", err))
		} else {
			info.owned = owned
			info.Owned = make([]ProviderRef, 0, len(owned))
			for _, obj := range owned {
				info.Owned = append(info.Owned, refFromObject(providerTypeOf(obj), obj))
			}
		}
	}

	// Match after the owned resources are listed because orphaned resources
	// are matching evidence.
	MatchInterfaces(ctx, info)

	return info, errs
}

// providerMode mirrors how today's code selects between the named-network
// test path and the provider resource path.
func (c collector) providerMode(ctx context.Context) (bool, error) {
	if pkgcfg.FromContext(ctx).Features.PerNamespaceNetworkProvider {
		return false, nil
	}
	switch pkgcfg.FromContext(ctx).NetworkProviderType {
	case pkgcfg.NetworkProviderTypeNamed:
		return true, nil
	case "":
		return false, errors.New("no network provider set")
	default:
		return false, nil
	}
}

func (c collector) listOwned(ctx context.Context) ([]ctrlclient.Object, error) {
	vmCtx := pkgctx.VirtualMachineContext{
		Context: ctx,
		Logger:  pkglog.FromContextOrDefault(ctx),
		VM:      c.vm,
	}
	return ListNetworkInterfaces(vmCtx, c.client)
}

func (c collector) providerResolution(
	ctx context.Context,
	spec vmopv1.VirtualMachineNetworkInterfaceSpec) Resolution {

	group, err := getNetworkInterfaceAPIGroup(spec)
	if err != nil {
		return failed(ProviderRef{Namespace: c.vm.Namespace},
			ReasonInvalidInterfaceSpec, err)
	}

	switch group {
	case netopv1alpha1.SchemeGroupVersion.Group:
		return c.netOPResolution(ctx, spec)
	case ncpv1alpha1.SchemeGroupVersion.Group:
		return c.ncpResolution(ctx, spec)
	case vpcv1alpha1.SchemeGroupVersion.Group:
		return c.vpcResolution(ctx, spec)
	default:
		return failed(ProviderRef{Namespace: c.vm.Namespace},
			ReasonInvalidInterfaceSpec,
			fmt.Errorf("unsupported network API Group: %q", group))
	}
}

func (c collector) netOPResolution(
	ctx context.Context,
	spec vmopv1.VirtualMachineNetworkInterfaceSpec) Resolution {

	networkName := spec.Network.Name
	ref := ProviderRef{
		Type:      pkgcfg.NetworkProviderTypeVDS,
		Namespace: c.vm.Namespace,
		Name:      NetOPCRName(c.vm.Name, networkName, spec.Name, false),
	}

	var netIf *netopv1alpha1.NetworkInterface
	if c.ensure {
		obj, err := createNetOPNetworkInterface(ctx, c.vm, c.client, spec)
		if err != nil {
			if obj != nil {
				ref = ensureFailedRef(ref, obj)
			}
			return ensureFailed(ref, err)
		}
		netIf = obj
	} else {
		obj := &netopv1alpha1.NetworkInterface{}
		found, err := c.getFirst(ctx, obj,
			NetOPCRName(c.vm.Name, networkName, spec.Name, true),
			ref.Name)
		if err != nil {
			return readFailed(ref, "NetworkInterface", err)
		}
		if !found {
			return notFound(ref, "NetworkInterface")
		}
		netIf = obj
	}

	r := decodeNetOP(ctx, netIf, spec)
	return c.resolveBacking(ctx, r, true, false)
}

func (c collector) ncpResolution(
	ctx context.Context,
	spec vmopv1.VirtualMachineNetworkInterfaceSpec) Resolution {

	networkName := spec.Network.Name
	ref := ProviderRef{
		Type:      pkgcfg.NetworkProviderTypeNSXT,
		Namespace: c.vm.Namespace,
		Name:      NCPCRName(c.vm.Name, networkName, spec.Name, false),
	}

	var vnetIf *ncpv1alpha1.VirtualNetworkInterface
	if c.ensure {
		obj, err := createNCPNetworkInterface(ctx, c.vm, c.client, spec)
		if err != nil {
			if obj != nil {
				ref = ensureFailedRef(ref, obj)
			}
			return ensureFailed(ref, err)
		}
		vnetIf = obj
	} else {
		obj := &ncpv1alpha1.VirtualNetworkInterface{}
		found, err := c.getFirst(ctx, obj,
			NCPCRName(c.vm.Name, networkName, spec.Name, true),
			ref.Name)
		if err != nil {
			return readFailed(ref, "VirtualNetworkInterface", err)
		}
		if !found {
			return notFound(ref, "VirtualNetworkInterface")
		}
		vnetIf = obj
	}

	r := decodeNCP(ctx, vnetIf)
	return c.resolveBacking(ctx, r, false, true)
}

func (c collector) vpcResolution(
	ctx context.Context,
	spec vmopv1.VirtualMachineNetworkInterfaceSpec) Resolution {

	networkName := spec.Network.Name
	ref := ProviderRef{
		Type:      pkgcfg.NetworkProviderTypeVPC,
		Namespace: c.vm.Namespace,
		Name:      VPCCRName(c.vm.Name, networkName, spec.Name),
	}

	// This mirrors the check createVPCNetworkInterface performs, so Observe
	// reports the same terminal failure Prepare does.
	switch kind := spec.Network.Kind; kind {
	case "SubnetSet", "Subnet", "":
	default:
		return failed(ref, ReasonInvalidInterfaceSpec,
			pkgerr.NoRequeueErrorf("network kind %q is not supported for VPC", kind))
	}

	var subnetPort *vpcv1alpha1.SubnetPort
	if c.ensure {
		obj, err := createVPCNetworkInterface(ctx, c.vm, c.client, spec)
		if err != nil {
			if obj != nil {
				ref = ensureFailedRef(ref, obj)
			}
			return ensureFailed(ref, err)
		}
		subnetPort = obj
	} else {
		obj := &vpcv1alpha1.SubnetPort{}
		found, err := c.getFirst(ctx, obj, ref.Name)
		if err != nil {
			return readFailed(ref, "SubnetPort", err)
		}
		if !found {
			return notFound(ref, "SubnetPort")
		}
		subnetPort = obj
	}

	r := decodeVPC(ctx, subnetPort)
	return c.resolveBacking(ctx, r, false, true)
}

func (c collector) namedResolution(
	ctx context.Context,
	spec vmopv1.VirtualMachineNetworkInterfaceSpec) Resolution {

	var networkName string
	ref := ProviderRef{Type: pkgcfg.NetworkProviderTypeNamed}

	if netRef := spec.Network; netRef != nil {
		networkName = netRef.Name
		ref.Name = networkName

		if netRef.Kind != "" || netRef.APIVersion != "" {
			return failed(ref, ReasonInvalidInterfaceSpec,
				fmt.Errorf("network TypeMeta not supported for name network: %v",
					netRef.TypeMeta))
		}
	}

	if networkName == "" {
		return failed(ref, ReasonInvalidInterfaceSpec,
			errors.New("network name is required"))
	}

	r := Resolution{
		Phase: ResolutionReady,
		Ref:   ref,
		Target: &Attachment{
			Backing: BackingID{
				Kind: BackingKindNetwork,
				ID:   networkName,
			},
			ExternalID: externalIDIntent(""),
			MAC:        MACPolicy{Pinned: strings.ToLower(spec.MACAddr)},
		},
	}

	if c.ensure {
		backing, err := c.finder.Network(ctx, networkName)
		if err != nil {
			return unknown(ref, ReasonBackingResolutionFailed,
				fmt.Errorf("unable to find named network %q: %w", networkName, err))
		}
		r.backing = backing
	}

	return r
}

// getFirst gets obj by each name in order and stops at the first one that
// exists. It returns false when none of the names exist.
func (c collector) getFirst(
	ctx context.Context,
	obj ctrlclient.Object,
	names ...string) (bool, error) {

	for _, name := range names {
		key := types.NamespacedName{Namespace: c.vm.Namespace, Name: name}
		if err := c.client.Get(ctx, key, obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// resolveBacking resolves the vSphere network backing of a Ready target when
// preparing. Observe does no backing resolution.
func (c collector) resolveBacking(
	ctx context.Context,
	r Resolution,
	supportMoID, supportLSUUID bool) Resolution {

	if !c.ensure || r.Phase != ResolutionReady {
		return r
	}

	backing, err := getNetworkInterfaceBacking(
		ctx,
		c.vimClient,
		c.clusterMoRef,
		r.Target.Backing.ID,
		supportMoID,
		supportLSUUID)
	if err != nil {
		if errors.Is(err, ErrNetworkInterfaceBackingNotSupported) {
			return failedWith(r, ReasonBackingNotSupported, err)
		}
		r.Phase = ResolutionUnknown
		r.Reason = ReasonBackingResolutionFailed
		r.Message = err.Error()
		r.Err = err
		r.Target = nil
		return r
	}

	r.backing = backing
	return r
}

// decodeNetOP decodes the status of a NetOP NetworkInterface. It mirrors
// getNetOPNetworkInterfaceDevice without the vCenter I/O.
func decodeNetOP(
	ctx context.Context,
	netIf *netopv1alpha1.NetworkInterface,
	spec vmopv1.VirtualMachineNetworkInterfaceSpec) Resolution {

	r := Resolution{
		Ref: refFromObject(pkgcfg.NetworkProviderTypeVDS, netIf),
		obj: netIf,
	}

	readyCond := findNetOPCondition(netIf, netopv1alpha1.NetworkInterfaceReady)
	if readyCond == nil || readyCond.Status != corev1.ConditionTrue {
		failCond := findNetOPCondition(netIf, netopv1alpha1.NetworkInterfaceFailure)
		if failCond != nil && failCond.Status == corev1.ConditionTrue {
			return failedWith(r, ReasonProviderFailed,
				fmt.Errorf("%w: failure - %s: %s", ErrNetworkInterfaceNotReady,
					failCond.Reason, failCond.Message))
		}

		var detail string
		if readyCond != nil {
			detail = reasonAndMessage(string(readyCond.Reason), readyCond.Message)
		}

		r.Phase = ResolutionPending
		r.Reason = ReasonProviderNotReady
		r.Message = notReadyMessage(r.Ref, "NetworkInterface", detail)
		return r
	}

	networkID := netIf.Status.NetworkID
	backingID, err := normalizeBackingID(ctx, networkID, true, false)
	if err != nil {
		return failedWith(r, ReasonBackingNotSupported, err)
	}

	// The NetworkInterface does not have a MAC address in its Spec, nor does
	// NetOP generate one in Status. A MAC address in the interface spec takes
	// precedence.
	macAddress := netIf.Status.MacAddress
	if spec.MACAddr != "" {
		macAddress = spec.MACAddr
	}

	r.Phase = ResolutionReady
	r.Target = &Attachment{
		Backing:    backingID,
		ExternalID: externalIDIntent(netIf.Status.ExternalID),
		MAC:        MACPolicy{Pinned: strings.ToLower(macAddress)},
	}
	return r
}

// decodeNCP decodes the status of an NCP VirtualNetworkInterface. It mirrors
// getNCPNetworkInterfaceDevice without the vCenter I/O.
func decodeNCP(
	ctx context.Context,
	vnetIf *ncpv1alpha1.VirtualNetworkInterface) Resolution {

	r := Resolution{
		Ref: refFromObject(pkgcfg.NetworkProviderTypeNSXT, vnetIf),
		obj: vnetIf,
	}

	var ready bool
	for _, cond := range vnetIf.Status.Conditions {
		// NOTE: For whatever reason, the contributed NCP code used Contains.
		if strings.Contains(cond.Type, "Ready") && strings.Contains(cond.Status, "True") {
			ready = true
		}
	}

	if !ready {
		var detail string
		for _, cond := range vnetIf.Status.Conditions {
			if strings.Contains(cond.Type, "Ready") && !strings.Contains(cond.Status, "True") {
				detail = reasonAndMessage(cond.Reason, cond.Message)
				break
			}
		}

		r.Phase = ResolutionPending
		r.Reason = ReasonProviderNotReady
		r.Message = notReadyMessage(r.Ref, "VirtualNetworkInterface", detail)
		return r
	}

	if vnetIf.Status.ProviderStatus == nil {
		r.Phase = ResolutionPending
		r.Reason = ReasonProviderStatusIncomplete
		r.Message = fmt.Sprintf(
			"VirtualNetworkInterface %s/%s is waiting for its provider status",
			r.Ref.Namespace, r.Ref.Name)
		return r
	}

	networkID := vnetIf.Status.ProviderStatus.NsxLogicalSwitchID
	backingID, err := normalizeBackingID(ctx, networkID, false, true)
	if err != nil {
		return failedWith(r, ReasonBackingNotSupported, err)
	}

	r.Phase = ResolutionReady
	r.Target = &Attachment{
		Backing:    backingID,
		ExternalID: externalIDIntent(vnetIf.Status.InterfaceID),
		// A MAC address in the interface spec is not supported for NCP.
		MAC: MACPolicy{Pinned: strings.ToLower(vnetIf.Status.MacAddress)},
	}
	return r
}

// decodeVPC decodes the status of a VPC SubnetPort. It mirrors
// getVPCSubnetPortDevice without the vCenter I/O.
func decodeVPC(
	ctx context.Context,
	subnetPort *vpcv1alpha1.SubnetPort) Resolution {

	r := Resolution{
		Ref: refFromObject(pkgcfg.NetworkProviderTypeVPC, subnetPort),
		obj: subnetPort,
	}

	var readyCond *vpcv1alpha1.Condition
	for i := range subnetPort.Status.Conditions {
		if subnetPort.Status.Conditions[i].Type == vpcv1alpha1.Ready {
			readyCond = &subnetPort.Status.Conditions[i]
			break
		}
	}

	if readyCond == nil || readyCond.Status != corev1.ConditionTrue {
		var detail string
		if readyCond != nil {
			detail = reasonAndMessage(readyCond.Reason, readyCond.Message)
		}

		r.Phase = ResolutionPending
		r.Reason = ReasonProviderNotReady
		r.Message = notReadyMessage(r.Ref, "SubnetPort", detail)
		return r
	}

	networkID := subnetPort.Status.NetworkInterfaceConfig.LogicalSwitchUUID
	if networkID == "" {
		r.Phase = ResolutionPending
		r.Reason = ReasonProviderStatusIncomplete
		r.Message = fmt.Sprintf(
			"SubnetPort %s/%s is waiting for its logical switch ID to be assigned",
			r.Ref.Namespace, r.Ref.Name)
		return r
	}

	backingID, err := normalizeBackingID(ctx, networkID, false, true)
	if err != nil {
		return failedWith(r, ReasonBackingNotSupported, err)
	}

	// A MAC address in the interface spec will have been set in the
	// SubnetPort Spec, so if set it is expected to be reflected in the
	// Status. An all zeros MAC address is ignored.
	macAddress := strings.ToLower(subnetPort.Status.NetworkInterfaceConfig.MACAddress)
	if macAddress == vpcIgnoreMacAddr {
		macAddress = ""
	}

	r.Phase = ResolutionReady
	r.Target = &Attachment{
		Backing:    backingID,
		ExternalID: externalIDIntent(subnetPort.Status.Attachment.ID),
		MAC:        MACPolicy{Pinned: macAddress},
	}
	return r
}

// normalizeBackingID classifies a provider network ID the same way
// getNetworkInterfaceBacking does, without any vCenter I/O.
func normalizeBackingID(
	ctx context.Context,
	networkID string,
	supportMoID, supportLSUUID bool) (BackingID, error) {

	if supportMoID {
		switch {
		case strings.HasPrefix(networkID, "dvportgroup-"):
			return BackingID{Kind: BackingKindDVPortgroup, ID: networkID}, nil
		case strings.HasPrefix(networkID, "network-"):
			if !pkgcfg.FromContext(ctx).Features.PerNamespaceNetworkProvider {
				return BackingID{}, ErrNetworkInterfaceBackingNotSupported
			}
			return BackingID{Kind: BackingKindNetwork, ID: networkID}, nil
		}
	}

	if supportLSUUID {
		if _, err := uuid.Parse(networkID); err == nil {
			return BackingID{Kind: BackingKindNSXSegment, ID: networkID}, nil
		}
	}

	return BackingID{}, fmt.Errorf("%w: %s", ErrNetworkInterfaceBackingNotSupported, networkID)
}

// observeAdapters normalizes the VM's virtual ethernet cards. It returns
// false when the VM's hardware configuration is not available.
func observeAdapters(moVM mo.VirtualMachine) ([]ObservedAdapter, bool) {
	if moVM.Config == nil {
		return nil, false
	}

	devices := object.VirtualDeviceList(moVM.Config.Hardware.Device)
	ethCards := devices.SelectByType((*vimtypes.VirtualEthernetCard)(nil))

	adapters := make([]ObservedAdapter, 0, len(ethCards))
	for _, dev := range ethCards {
		if a, ok := observeAdapter(dev); ok {
			adapters = append(adapters, a)
		}
	}

	return adapters, true
}

// observeAdapter normalizes one virtual ethernet card. It returns false when
// the device is not a virtual ethernet card.
func observeAdapter(dev vimtypes.BaseVirtualDevice) (ObservedAdapter, bool) {
	bEthCard, ok := dev.(vimtypes.BaseVirtualEthernetCard)
	if !ok {
		return ObservedAdapter{}, false
	}
	ethCard := bEthCard.GetVirtualEthernetCard()

	a := ObservedAdapter{
		Key:         ethCard.Key,
		Type:        reflect.TypeOf(dev).Elem().Name(),
		MAC:         strings.ToLower(ethCard.MacAddress),
		AddressType: ethCard.AddressType,
		Attachment:  observedAttachment(ethCard),
		device:      dev,
	}

	if ethCard.UnitNumber != nil {
		unitNumber := *ethCard.UnitNumber
		a.UnitNumber = &unitNumber
	}

	if c := ethCard.Connectable; c != nil {
		a.Connect = ConnectionState{
			Present:           true,
			Connected:         c.Connected,
			StartConnected:    c.StartConnected,
			AllowGuestControl: c.AllowGuestControl,
		}
	}

	return a, true
}

func observedAttachment(ethCard *vimtypes.VirtualEthernetCard) Attachment {
	var a Attachment

	switch b := ethCard.Backing.(type) {
	case *vimtypes.VirtualEthernetCardNetworkBackingInfo:
		a.Backing.Kind = BackingKindNetwork
		// The server resolves and reports back the Network MoRef for a
		// standard portgroup backing.
		if b.Network != nil {
			a.Backing.ID = b.Network.Value
		} else {
			a.Backing.ID = b.DeviceName
		}
	case *vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo:
		a.Backing.Kind = BackingKindDVPortgroup
		a.Backing.ID = b.Port.PortgroupKey
		a.Backing.SwitchUUID = b.Port.SwitchUuid
	case *vimtypes.VirtualEthernetCardOpaqueNetworkBackingInfo:
		a.Backing.Kind = BackingKindOpaque
		a.Backing.ID = b.OpaqueNetworkId
	}

	if ethCard.ExternalId != "" {
		a.ExternalID = FieldIntent{Op: IntentSet, Value: ethCard.ExternalId}
	} else {
		a.ExternalID = FieldIntent{Op: IntentClear}
	}

	if ethCard.AddressType == string(vimtypes.VirtualEthernetCardMacTypeManual) {
		a.MAC.Pinned = strings.ToLower(ethCard.MacAddress)
	}

	return a
}

func observationIdentity(
	vm *vmopv1.VirtualMachine,
	moVM mo.VirtualMachine) ObservationIdentity {

	id := ObservationIdentity{
		VMUID:      vm.UID,
		Generation: vm.Generation,
	}

	if vm.Spec.Network != nil {
		if data, err := json.Marshal(vm.Spec.Network.Interfaces); err == nil {
			sum := sha256.Sum256(data)
			id.NetworkHash = hex.EncodeToString(sum[:])
		}
	}

	if moVM.Config != nil {
		id.ChangeVersion = moVM.Config.ChangeVersion
	}

	return id
}

// externalIDIntent returns the target intent for a provider external ID. An
// empty value keeps the adapter's external ID. Clear is never produced here.
func externalIDIntent(id string) FieldIntent {
	if id == "" {
		return FieldIntent{Op: IntentKeep}
	}
	return FieldIntent{Op: IntentSet, Value: id}
}

func providerTypeOf(obj ctrlclient.Object) pkgcfg.NetworkProviderType {
	switch obj.(type) {
	case *netopv1alpha1.NetworkInterface:
		return pkgcfg.NetworkProviderTypeVDS
	case *ncpv1alpha1.VirtualNetworkInterface:
		return pkgcfg.NetworkProviderTypeNSXT
	case *vpcv1alpha1.SubnetPort:
		return pkgcfg.NetworkProviderTypeVPC
	}
	return ""
}

func refFromObject(
	providerType pkgcfg.NetworkProviderType,
	obj ctrlclient.Object) ProviderRef {

	return ProviderRef{
		Type:      providerType,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
		UID:       obj.GetUID(),
	}
}

// ensureFailedRef returns the reference for a provider resource whose create
// or patch failed. The object's name is used when set because it may be the
// legacy name, but its UID is used only when the object was read from the
// server.
func ensureFailedRef(ref ProviderRef, obj ctrlclient.Object) ProviderRef {
	if name := obj.GetName(); name != "" {
		ref.Name = name
	}
	if obj.GetResourceVersion() != "" {
		ref.UID = obj.GetUID()
	}
	return ref
}

func notFound(ref ProviderRef, kind string) Resolution {
	return Resolution{
		Phase:  ResolutionPending,
		Reason: ReasonProviderResourceNotFound,
		Message: fmt.Sprintf("%s %s/%s does not exist yet",
			kind, ref.Namespace, ref.Name),
		Ref: ref,
	}
}

func readFailed(ref ProviderRef, kind string, err error) Resolution {
	return unknown(ref, ReasonReadFailed,
		fmt.Errorf("failed to get %s %s/%s: %w", kind, ref.Namespace, ref.Name, err))
}

// ensureFailed classifies a create or patch failure. The errors that are
// terminal today remain terminal; any other failure leaves the provider
// outcome unknown.
func ensureFailed(ref ProviderRef, err error) Resolution {
	if pkgerr.IsNoRequeueError(err) {
		return failed(ref, ReasonInvalidInterfaceSpec, err)
	}
	return unknown(ref, ReasonEnsureFailed,
		fmt.Errorf("failed to create or patch %s/%s: %w", ref.Namespace, ref.Name, err))
}

func failed(ref ProviderRef, reason string, err error) Resolution {
	return failedWith(Resolution{Ref: ref}, reason, err)
}

func failedWith(r Resolution, reason string, err error) Resolution {
	r.Phase = ResolutionFailed
	r.Reason = reason
	r.Message = err.Error()
	r.Err = err
	r.Target = nil
	return r
}

func unknown(ref ProviderRef, reason string, err error) Resolution {
	return Resolution{
		Phase:   ResolutionUnknown,
		Reason:  reason,
		Message: err.Error(),
		Ref:     ref,
		Err:     err,
	}
}

func notReadyMessage(ref ProviderRef, kind, detail string) string {
	msg := fmt.Sprintf("%s %s/%s is not ready", kind, ref.Namespace, ref.Name)
	if detail != "" {
		msg += ": " + detail
	}
	return msg
}

func reasonAndMessage(reason, message string) string {
	switch {
	case reason != "" && message != "":
		return reason + " - " + message
	case reason != "":
		return reason
	default:
		return message
	}
}
