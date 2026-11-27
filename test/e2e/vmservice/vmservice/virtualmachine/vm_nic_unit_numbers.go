// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1a2 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/e2e/framework"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/vcenter"
	"github.com/vmware-tanzu/vm-operator/test/e2e/utils"
	e2eConfig "github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/config"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/consts"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/lib/vmoperator"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/skipper"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/vmservice"
	"github.com/vmware-tanzu/vm-operator/test/e2e/wcpframework"
)

const (
	// nicUnitNumberFirst and nicUnitNumberLast bound the PCI units that
	// Ethernet cards occupy on a virtual machine.
	nicUnitNumberFirst int32 = 7
	nicUnitNumberLast  int32 = 16

	// nicUnitNumberNudgeAnnotation is patched to trigger a reconcile.
	nicUnitNumberNudgeAnnotation = "e2e.vmoperator.vmware.com/nic-unit-numbers-nudge"
)

// VMNICUnitNumbersSpecInput is the input for the NIC unit number E2E spec.
type VMNICUnitNumbersSpecInput struct {
	ClusterProxy     wcpframework.WCPClusterProxyInterface
	Config           *e2eConfig.E2EConfig
	ArtifactFolder   string
	SkipCleanup      bool
	WCPNamespaceName string
}

// nicHardware is the identity of an Ethernet card as observed in vCenter.
type nicHardware struct {
	Key  int32
	MAC  string
	Unit int32
}

// nicUnitNumbersEnv bundles what the NIC unit number helpers need.
type nicUnitNumbersEnv struct {
	input         VMNICUnitNumbersSpecInput
	client        ctrlclient.Client
	propCollector *property.Collector
	vmiName       string
}

// createPoweredOffVM creates a powered off VM with numNICs interfaces, none of
// which carry a unit number, and returns its key and vSphere MoRef.
func (e *nicUnitNumbersEnv) createPoweredOffVM(
	ctx context.Context,
	prefix string,
	numNICs int) (types.NamespacedName, vimtypes.ManagedObjectReference) {

	GinkgoHelper()

	return e.createPoweredOffVMWithUnits(ctx, prefix, make([]*int32, numNICs))
}

// createPoweredOffVMWithUnits creates a powered off VM with one interface per
// entry in units. A non-nil entry is set as that interface's explicit unit
// number, and a nil entry leaves it unset for the backfill.
func (e *nicUnitNumbersEnv) createPoweredOffVMWithUnits(
	ctx context.Context,
	prefix string,
	units []*int32) (types.NamespacedName, vimtypes.ManagedObjectReference) {

	GinkgoHelper()

	numNICs := len(units)

	config := e.input.Config
	resources := config.InfraConfig.ManagementClusterConfig.Resources
	vmName := prefix + capiutil.RandomString(5)
	vm := &vmopv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vmName,
			Namespace: e.input.WCPNamespaceName,
		},
		Spec: vmopv1.VirtualMachineSpec{
			ClassName:    resources.VMClassName,
			ImageName:    e.vmiName,
			StorageClass: resources.StorageClassName,
			PowerState:   vmopv1.VirtualMachinePowerStateOff,
			// LinuxPrep applies the addresses from the network interface CRs
			// (the interfaces do not request DHCP), so a VM that reaches an
			// IP also shows the network customization still works.
			Bootstrap: &vmopv1.VirtualMachineBootstrapSpec{
				LinuxPrep: &vmopv1.VirtualMachineBootstrapLinuxPrepSpec{},
			},
			Network: &vmopv1.VirtualMachineNetworkSpec{},
		},
	}
	for i := range numNICs {
		// The network is left unset so that the default network is applied
		// to every interface at admission.
		vm.Spec.Network.Interfaces = append(
			vm.Spec.Network.Interfaces,
			vmopv1.VirtualMachineNetworkInterfaceSpec{
				Name:       fmt.Sprintf("eth%d", i),
				Type:       vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3,
				UnitNumber: units[i],
			})
	}
	vmoperator.CreateVirtualMachine(ctx, config, e.input.ClusterProxy, vm, e.input.SkipCleanup)

	// Create populates vm with the admitted object, so this checks admission
	// directly without racing a reconcile.
	By("Verifying admission neither assigns nor drops unit numbers on create")
	Expect(vm.Spec.Network).ToNot(BeNil())
	Expect(vm.Spec.Network.Interfaces).To(HaveLen(numNICs))
	for i, iface := range vm.Spec.Network.Interfaces {
		Expect(iface.UnitNumber).To(Equal(units[i]),
			"interface %s should keep exactly the unit number it was created with", iface.Name)
	}

	vmoperator.WaitForVirtualMachineConditionCreated(ctx, config, e.client, vm.Namespace, vm.Name)
	moID := vmoperator.WaitForVirtualMachineMOID(ctx, config, e.client, vm.Namespace, vm.Name)

	key := ctrlclient.ObjectKeyFromObject(vm)
	return key, vimtypes.ManagedObjectReference{Type: "VirtualMachine", Value: moID}
}

// waitForBackfill waits until each of the numNICs interfaces has a unit number
// and returns the VM.
func (e *nicUnitNumbersEnv) waitForBackfill(
	ctx context.Context,
	key types.NamespacedName,
	numNICs int) *vmopv1.VirtualMachine {

	GinkgoHelper()

	By("Waiting for the schema upgrade to backfill the unit numbers")
	var vm *vmopv1.VirtualMachine
	Eventually(func(g Gomega) {
		var err error
		vm, err = utils.GetVirtualMachine(ctx, e.client, key.Namespace, key.Name)
		g.Expect(err).ToNot(HaveOccurred())
		// The webhooks gate on the upgrade annotation, so wait for it too.
		g.Expect(vm.Annotations).To(HaveKey(pkgconst.UpgradedToFeatureVersionAnnotationKey),
			"VM %s has not been upgraded yet", key)
		g.Expect(vm.Spec.Network).ToNot(BeNil())
		g.Expect(vm.Spec.Network.Interfaces).To(HaveLen(numNICs))
		for _, iface := range vm.Spec.Network.Interfaces {
			g.Expect(iface.UnitNumber).ToNot(BeNil(),
				"interface %s has no unit number yet", iface.Name)
		}
	}, e.input.Config.GetIntervals("default", "wait-virtual-machine-creation")...).Should(Succeed(),
		"Timed out waiting for unit numbers to be backfilled on VM %s", key)

	return vm
}

// hardwareNICs returns the Ethernet cards of the VM as seen by vCenter, sorted
// by unit number.
func (e *nicUnitNumbersEnv) hardwareNICs(
	ctx context.Context,
	g Gomega,
	moRef vimtypes.ManagedObjectReference) []nicHardware {

	var vmMO mo.VirtualMachine
	g.Expect(e.propCollector.RetrieveOne(ctx, moRef, []string{"config.hardware.device"}, &vmMO)).To(Succeed(),
		"failed to retrieve device config for VM %s", moRef.Value)
	g.Expect(vmMO.Config).ToNot(BeNil())

	var nics []nicHardware
	for _, dev := range object.VirtualDeviceList(vmMO.Config.Hardware.Device).
		SelectByType((*vimtypes.VirtualEthernetCard)(nil)) {

		vd := dev.GetVirtualDevice()
		g.Expect(vd.UnitNumber).ToNot(BeNil(), "Ethernet card %d has no unit number", vd.Key)

		nic := nicHardware{Key: vd.Key, Unit: *vd.UnitNumber}
		if card, ok := dev.(vimtypes.BaseVirtualEthernetCard); ok {
			nic.MAC = card.GetVirtualEthernetCard().MacAddress
		}
		nics = append(nics, nic)
	}
	slices.SortFunc(nics, func(a, b nicHardware) int { return int(a.Unit - b.Unit) })

	return nics
}

// expectStable asserts that the Ethernet cards still equal baseline and the
// spec unit numbers still equal units for the whole stability window.
//
// The MAC is part of the baseline because a NIC that is removed and re-added
// at the same unit number can get the same key back, but not the same
// generated MAC. That only holds when the MAC is generated, as with VDS
// networking. Providers that pin the MAC through the network interface CR
// (NSX-T, VPC) re-add the same MAC, so there the baseline cannot tell a
// replaced NIC from an untouched one and this check is a weak guard against
// churn.
func (e *nicUnitNumbersEnv) expectStable(
	ctx context.Context,
	key types.NamespacedName,
	moRef vimtypes.ManagedObjectReference,
	baseline []nicHardware,
	units []int32) {

	GinkgoHelper()

	e.nudgeReconcile(ctx, key)

	Consistently(func(g Gomega) {
		g.Expect(e.hardwareNICs(ctx, g, moRef)).To(Equal(baseline),
			"Ethernet cards of VM %s changed identity", key)
		current, err := utils.GetVirtualMachine(ctx, e.client, key.Namespace, key.Name)
		g.Expect(err).ToNot(HaveOccurred(), "failed to get VM %s", key)
		g.Expect(specUnitNumbers(g, current)).To(Equal(units),
			"spec unit numbers of VM %s changed", key)
	}, e.input.Config.GetIntervals("default", "consistent-vm-nic-unit-numbers")...).Should(Succeed())
}

// nudgeReconcile touches a throwaway annotation so that the VM is reconciled
// now instead of sitting idle.
//
// The VM status has no observedGeneration, so there is no positive signal that
// a reconcile has seen a spec change. Callers that assert "nothing changed"
// after a spec change must therefore either wait for a visible effect of the
// change first, or accept that the stability window that follows is the only
// evidence, and that it can only catch a regression that acts within it.
func (e *nicUnitNumbersEnv) nudgeReconcile(ctx context.Context, key types.NamespacedName) {
	GinkgoHelper()

	Eventually(func() error {
		return vmoperator.PatchVirtualMachine(ctx, e.client, key.Namespace, key.Name,
			func(vm *vmopv1.VirtualMachine) error {
				if vm.Annotations == nil {
					vm.Annotations = map[string]string{}
				}
				vm.Annotations[nicUnitNumberNudgeAnnotation] = time.Now().Format(time.RFC3339Nano)
				return nil
			})
	}, e.input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(),
		"failed to nudge reconcile of VM %s", key)
}

// mutateInterfaces applies mutate to the interface list of the VM with a raw
// patch, and returns the VM as admitted, which carries the effect of the
// mutation webhook.
func (e *nicUnitNumbersEnv) mutateInterfaces(
	ctx context.Context,
	key types.NamespacedName,
	mutate func(ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []vmopv1.VirtualMachineNetworkInterfaceSpec) *vmopv1.VirtualMachine {

	GinkgoHelper()

	vm, err := utils.GetVirtualMachine(ctx, e.client, key.Namespace, key.Name)
	Expect(err).ToNot(HaveOccurred())
	patch := ctrlclient.MergeFrom(vm.DeepCopy())
	vm.Spec.Network.Interfaces = mutate(slices.Clone(vm.Spec.Network.Interfaces))
	Expect(e.client.Patch(ctx, vm, patch)).To(Succeed(),
		"failed to change the interfaces of VM %s", key)

	return vm
}

// waitForNICs waits until check passes on the cards of the VM and returns the
// cards that passed.
func (e *nicUnitNumbersEnv) waitForNICs(
	ctx context.Context,
	moRef vimtypes.ManagedObjectReference,
	check func(g Gomega, nics []nicHardware)) []nicHardware {

	GinkgoHelper()

	var nics []nicHardware
	Eventually(func(g Gomega) {
		nics = e.hardwareNICs(ctx, g, moRef)
		check(g, nics)
	}, e.input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())

	return nics
}

// baselineNICs waits until the cards of the VM sit at exactly units and
// returns them.
func (e *nicUnitNumbersEnv) baselineNICs(
	ctx context.Context,
	moRef vimtypes.ManagedObjectReference,
	units []int32) []nicHardware {

	GinkgoHelper()

	return e.waitForNICs(ctx, moRef, func(g Gomega, nics []nicHardware) {
		g.Expect(hardwareUnitNumbers(nics)).To(Equal(units))
	})
}

// nicAt returns the card at unit, failing if there is none.
func nicAt(nics []nicHardware, unit int32) nicHardware {
	GinkgoHelper()

	i := slices.IndexFunc(nics, func(n nicHardware) bool { return n.Unit == unit })
	Expect(i).To(BeNumerically(">=", 0), "no Ethernet card at unit number %d in %v", unit, nics)

	return nics[i]
}

// hasNICKey returns whether a card with the key exists.
func hasNICKey(nics []nicHardware, key int32) bool {
	return slices.ContainsFunc(nics, func(n nicHardware) bool { return n.Key == key })
}

// specUnitsByName maps each interface name to its spec unit number.
func specUnitsByName(g Gomega, vm *vmopv1.VirtualMachine) map[string]int32 {
	GinkgoHelper()

	m := map[string]int32{}
	for _, iface := range vm.Spec.Network.Interfaces {
		g.Expect(iface.UnitNumber).ToNot(BeNil(), "interface %s has no unit number", iface.Name)
		m[iface.Name] = *iface.UnitNumber
	}

	return m
}

// expectStatusUnitNumbers waits for the VM to report an IP and then asserts
// that every status.network.interfaces entry carries the unit number of the
// card it names by device key, and of the spec interface it names by name.
//
// Status entries only exist for NICs that VMware Tools reports, so this does
// not require an entry for every interface, only for at least one. The
// primary NIC is the one guaranteed to be reported.
func (e *nicUnitNumbersEnv) expectStatusUnitNumbers(
	ctx context.Context,
	key types.NamespacedName,
	moRef vimtypes.ManagedObjectReference) {

	GinkgoHelper()

	vmoperator.WaitForVirtualMachineIP(ctx, e.input.Config, e.client, key.Namespace, key.Name)

	By("Verifying status.network.interfaces reports the observed unit numbers")
	Eventually(func(g Gomega) {
		vm, err := utils.GetVirtualMachine(ctx, e.client, key.Namespace, key.Name)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(vm.Status.Network).ToNot(BeNil())
		g.Expect(vm.Status.Network.Interfaces).ToNot(BeEmpty())

		nics := e.hardwareNICs(ctx, g, moRef)
		specUnits := specUnitsByName(g, vm)
		checked := 0
		for _, st := range vm.Status.Network.Interfaces {
			i := slices.IndexFunc(nics, func(n nicHardware) bool { return n.Key == st.DeviceKey })
			if i < 0 {
				// An entry that no card backs, such as one for a device that is
				// being removed, is not what is under test.
				continue
			}
			checked++
			g.Expect(st.UnitNumber).ToNot(BeNil(),
				"status interface %q (key %d) has no unit number", st.Name, st.DeviceKey)
			g.Expect(*st.UnitNumber).To(Equal(nics[i].Unit),
				"status interface %q does not report the unit number of its card", st.Name)
			if st.Name != "" {
				g.Expect(*st.UnitNumber).To(Equal(specUnits[st.Name]),
					"status interface %q does not report its spec unit number", st.Name)
			}
		}
		g.Expect(checked).To(BeNumerically(">=", 1), "no status interface entry is backed by a card")
	}, e.input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())
}

// isVDS returns whether the networking topology is VDS, the only provider
// that generates the MAC address of a newly added card. NSX-T and VPC pin the
// MAC through the network interface CR, so a replaced card keeps its MAC.
func (e *nicUnitNumbersEnv) isVDS() bool {
	return framework.NetworkTopologyIs(e.input.Config.InfraConfig.NetworkingTopology, consts.VDS)
}

// nicsNow reads the cards of the VM once.
func (e *nicUnitNumbersEnv) nicsNow(ctx context.Context, moRef vimtypes.ManagedObjectReference) []nicHardware {
	GinkgoHelper()

	return e.hardwareNICs(ctx, Default, moRef)
}

// nicExtraConfig returns the per-NIC (ethernetN.*) ExtraConfig of the VM as
// seen by vCenter.
func (e *nicUnitNumbersEnv) nicExtraConfig(
	ctx context.Context,
	g Gomega,
	moRef vimtypes.ManagedObjectReference) map[string]string {

	var vmMO mo.VirtualMachine
	g.Expect(e.propCollector.RetrieveOne(ctx, moRef, []string{"config.extraConfig"}, &vmMO)).To(Succeed())
	g.Expect(vmMO.Config).ToNot(BeNil())

	m := map[string]string{}
	for _, ov := range vmMO.Config.ExtraConfig {
		if o := ov.GetOptionValue(); strings.HasPrefix(o.Key, "ethernet") {
			m[o.Key] = fmt.Sprint(o.Value)
		}
	}

	return m
}

// nicExtraConfigNow reads the per-NIC ExtraConfig once.
func (e *nicUnitNumbersEnv) nicExtraConfigNow(
	ctx context.Context,
	moRef vimtypes.ManagedObjectReference) map[string]string {

	GinkgoHelper()

	return e.nicExtraConfig(ctx, Default, moRef)
}

// unitPtr returns a pointer to u.
func unitPtr(u int32) *int32 { return &u }

// highestFreeNICUnitNumber returns the highest unit number in the Ethernet
// card band that is not in use.
func highestFreeNICUnitNumber(used []int32) int32 {
	u := nicUnitNumberLast
	for slices.Contains(used, u) {
		u--
	}
	return u
}

// specUnitNumbers returns the sorted unit numbers of the spec interfaces.
func specUnitNumbers(g Gomega, vm *vmopv1.VirtualMachine) []int32 {
	GinkgoHelper()

	var units []int32
	for _, iface := range vm.Spec.Network.Interfaces {
		g.Expect(iface.UnitNumber).ToNot(BeNil(), "interface %s has no unit number", iface.Name)
		units = append(units, *iface.UnitNumber)
	}
	slices.Sort(units)

	return units
}

// expectValidUnitNumbers asserts the sorted unit numbers are in the Ethernet
// card band and unique.
func expectValidUnitNumbers(units []int32) {
	GinkgoHelper()

	for _, u := range units {
		Expect(u).To(BeNumerically(">=", nicUnitNumberFirst))
		Expect(u).To(BeNumerically("<=", nicUnitNumberLast))
	}
	Expect(slices.Compact(slices.Clone(units))).To(Equal(units),
		"unit numbers must be unique: %v", units)
}

// hardwareUnitNumbers returns the sorted unit numbers of the cards.
func hardwareUnitNumbers(nics []nicHardware) []int32 {
	units := make([]int32, 0, len(nics))
	for _, n := range nics {
		units = append(units, n.Unit)
	}
	slices.Sort(units)

	return units
}

// lowestFreeNICUnitNumber returns the lowest unit number in the Ethernet card
// band that is not in use.
func lowestFreeNICUnitNumber(used []int32) int32 {
	u := nicUnitNumberFirst
	for slices.Contains(used, u) {
		u++
	}
	return u
}

// VMNICUnitNumbersSpec exercises spec.network.interfaces[].unitNumber across
// VM creation, the schema-upgrade backfill that runs after the VM is created,
// and the admission-time assignment for interfaces added afterwards.
//
// It also covers reconcile matching devices by unit number only (reorder,
// removal, renumbering, explicit values), status reporting, and updates from
// older API versions.
//
// Out of scope: admission validation (unit tests), SR-IOV (no testbed), and the
// upgrade-brownfield backfill (the capability cannot flip mid-run).
func VMNICUnitNumbersSpec(ctx context.Context, inputGetter func() VMNICUnitNumbersSpecInput) {
	const specName = "vm-nic-unit-numbers"

	var (
		input VMNICUnitNumbersSpecInput
		env   *nicUnitNumbersEnv
	)

	BeforeEach(func() {
		input = inputGetter()
		Expect(input.Config).ToNot(BeNil(),
			"Invalid argument. input.Config can't be nil when calling %s spec", specName)
		Expect(input.Config.InfraConfig).ToNot(BeNil(),
			"Invalid argument. input.Config.InfraConfig can't be nil when calling %s spec", specName)
		skipper.SkipUnlessInfraIs(input.Config.InfraConfig.InfraName, consts.WCP)

		Expect(input.ClusterProxy).ToNot(BeNil(),
			"Invalid argument. input.ClusterProxy can't be nil when calling %s spec", specName)
		Expect(input.WCPNamespaceName).ToNot(BeEmpty(),
			"Invalid argument. input.WCPNamespaceName can't be empty when calling %s spec", specName)

		skipper.SkipUnlessSupervisorCapabilityEnabled(
			input.Config.SupervisorCapabilities,
			consts.VMNetworkInterfaceUnitNumbersCapabilityName)

		config := input.Config
		svClusterClient := input.ClusterProxy.GetClient()

		cancelPodWatches := framework.WatchPodLogsAndEventsInNamespaces(
			ctx,
			[]string{config.GetVariable("VMOPNamespace")},
			input.ClusterProxy.GetRESTConfig(),
			filepath.Join(input.ArtifactFolder, specName),
		)
		DeferCleanup(cancelPodWatches)

		vCenterClient := vcenter.NewVimClientFromKubeconfig(ctx, input.ClusterProxy.GetKubeconfigPath())
		DeferCleanup(vcenter.LogoutVimClient, vCenterClient)

		linuxImageDisplayName := vmservice.GetDefaultImageDisplayName(
			config.InfraConfig.ManagementClusterConfig.Resources)

		env = &nicUnitNumbersEnv{
			input:         input,
			client:        svClusterClient,
			propCollector: property.DefaultCollector(vCenterClient),
			vmiName: vmoperator.WaitForVirtualMachineImageName(
				ctx, &config.Config, svClusterClient, input.WCPNamespaceName, linuxImageDisplayName),
		}
	})

	It("backfills the unit number of the only interface from the device after the VM is created",
		Label("core-functional", "experimental"), func() {
			key, moRef := env.createPoweredOffVM(ctx, "nic-un-one-", 1)
			units := specUnitNumbers(Default, env.waitForBackfill(ctx, key, 1))
			expectValidUnitNumbers(units)

			By("Verifying the backfilled value is the unit number of the Ethernet card")
			Eventually(func(g Gomega) {
				nics := env.hardwareNICs(ctx, g, moRef)
				g.Expect(nics).To(HaveLen(1))
				g.Expect(hardwareUnitNumbers(nics)).To(Equal(units))
			}, input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())
		})

	It("backfills unique unit numbers that match the devices for a VM with multiple interfaces",
		Label("core-functional", "experimental"), func() {
			key, moRef := env.createPoweredOffVM(ctx, "nic-un-multi-", 3)
			units := specUnitNumbers(Default, env.waitForBackfill(ctx, key, 3))
			expectValidUnitNumbers(units)

			By("Verifying the set of backfilled values matches the Ethernet cards")
			Eventually(func(g Gomega) {
				nics := env.hardwareNICs(ctx, g, moRef)
				g.Expect(nics).To(HaveLen(3))
				g.Expect(hardwareUnitNumbers(nics)).To(Equal(units))
			}, input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())
		})

	It("assigns the next free unit number to an interface added after the backfill",
		Label("core-functional", "experimental"), func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(
				input.Config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

			key, moRef := env.createPoweredOffVM(ctx, "nic-un-add-", 2)

			// The backfill must have completed first: until then, changes to
			// spec.network.interfaces are rejected.
			vm := env.waitForBackfill(ctx, key, 2)
			before := specUnitNumbers(Default, vm)
			expectValidUnitNumbers(before)

			By("Appending an interface without a unit number")
			// A raw patch is used so that vm is populated with the admitted
			// object, which carries the mutation under test.
			patch := ctrlclient.MergeFrom(vm.DeepCopy())
			added := vm.Spec.Network.Interfaces[0].DeepCopy()
			added.Name = "ethadded"
			added.UnitNumber = nil
			added.MACAddr = ""
			vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces, *added)
			Expect(env.client.Patch(ctx, vm, patch)).To(Succeed(),
				"failed to append an interface to VM %s", key)

			By("Verifying admission assigned the lowest free unit number")
			wantNext := lowestFreeNICUnitNumber(before)
			Expect(vm.Spec.Network.Interfaces).To(HaveLen(3))
			Expect(vm.Spec.Network.Interfaces[2].UnitNumber).ToNot(BeNil())
			Expect(*vm.Spec.Network.Interfaces[2].UnitNumber).To(Equal(wantNext))

			By("Verifying the existing interfaces kept their unit numbers")
			current, err := utils.GetVirtualMachine(ctx, env.client, key.Namespace, key.Name)
			Expect(err).ToNot(HaveOccurred())
			Expect(specUnitNumbers(Default, current)).To(ConsistOf(append(slices.Clone(before), wantNext)))
			for i := range 2 {
				Expect(current.Spec.Network.Interfaces[i].UnitNumber).To(Equal(
					vm.Spec.Network.Interfaces[i].UnitNumber),
					"interface %d should keep its unit number", i)
			}

			By("Waiting for the new Ethernet card to be added")
			Eventually(func(g Gomega) {
				g.Expect(env.hardwareNICs(ctx, g, moRef)).To(HaveLen(3))
			}, input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())
		})

	It("keeps unit numbers and devices stable across reconciles and a power cycle",
		Label("extended-functional", "experimental"), func() {
			key, moRef := env.createPoweredOffVM(ctx, "nic-un-stable-", 2)
			units := specUnitNumbers(Default, env.waitForBackfill(ctx, key, 2))
			expectValidUnitNumbers(units)

			var baseline []nicHardware
			Eventually(func(g Gomega) {
				baseline = env.hardwareNICs(ctx, g, moRef)
				g.Expect(baseline).To(HaveLen(2))
				g.Expect(hardwareUnitNumbers(baseline)).To(Equal(units))
			}, input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())

			By("Verifying nothing changes across reconciles of the powered off VM")
			env.expectStable(ctx, key, moRef, baseline, units)

			By("Powering on the VM")
			vmoperator.PowerOnVirtualMachineAndWait(ctx, input.Config, env.client, key.Namespace, key.Name)
			env.expectStable(ctx, key, moRef, baseline, units)
			env.expectStatusUnitNumbers(ctx, key, moRef)

			By("Power cycling the VM")
			powerCycleVM(ctx, env.client, input.Config, key)

			By("Verifying nothing changes after the power cycle")
			env.expectStable(ctx, key, moRef, baseline, units)
			env.expectStatusUnitNumbers(ctx, key, moRef)

			By("Verifying the interface type is still in the spec")
			vm, err := utils.GetVirtualMachine(ctx, env.client, key.Namespace, key.Name)
			Expect(err).ToNot(HaveOccurred())
			for _, iface := range vm.Spec.Network.Interfaces {
				Expect(iface.Type).To(Equal(vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3),
					"interface %s lost its type", iface.Name)
			}
		})

	It("keeps each interface on its own device and ExtraConfig when the interfaces are reordered",
		Label("core-functional", "experimental"), func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(
				input.Config.SupervisorCapabilities, consts.VMServiceClasslessVMCapabilityName)

			resources := input.Config.InfraConfig.ManagementClusterConfig.Resources
			vmName := "nic-un-reorder-" + capiutil.RandomString(5)
			vm := buildNICExtraConfigVM(nicExtraConfigVMOptions{
				Name:         vmName,
				Namespace:    input.WCPNamespaceName,
				ClassName:    resources.VMClassName,
				ImageName:    env.vmiName,
				StorageClass: resources.StorageClassName,
				PowerState:   vmopv1.VirtualMachinePowerStateOff,
				VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{
					CoalescingScheme: ptr.To(vmopv1.CoalescingSchemeAdapt),
				},
			})
			params32 := "32"
			vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces,
				vmopv1.VirtualMachineNetworkInterfaceSpec{
					Name: "eth1",
					Type: vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3,
					VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{
						CoalescingScheme: ptr.To(vmopv1.CoalescingSchemeDisabled),
					},
				},
				vmopv1.VirtualMachineNetworkInterfaceSpec{
					Name: "eth2",
					Type: vmopv1.VirtualMachineNetworkInterfaceTypeVMXNet3,
					VMXNet3: &vmopv1.VirtualMachineNetworkInterfaceVMXNet3Spec{
						CoalescingScheme: ptr.To(vmopv1.CoalescingSchemeStatic),
						CoalescingParams: &params32,
					},
				})
			vmoperator.CreateVirtualMachine(ctx, input.Config, input.ClusterProxy, vm, input.SkipCleanup)
			vmoperator.WaitForVirtualMachineConditionCreated(ctx, input.Config, env.client, vm.Namespace, vm.Name)
			moID := vmoperator.WaitForVirtualMachineMOID(ctx, input.Config, env.client, vm.Namespace, vm.Name)
			moRef := vimtypes.ManagedObjectReference{Type: "VirtualMachine", Value: moID}
			key := ctrlclient.ObjectKeyFromObject(vm)

			backfilled := env.waitForBackfill(ctx, key, 3)
			byName := specUnitsByName(Default, backfilled)
			units := specUnitNumbers(Default, backfilled)
			baseline := env.baselineNICs(ctx, moRef, units)

			By("Waiting for the per-NIC ExtraConfig to be applied to the powered off VM")
			var ecBefore map[string]string
			Eventually(func(g Gomega) {
				ecBefore = env.nicExtraConfig(ctx, g, moRef)
				n := 0
				for k := range ecBefore {
					if strings.HasSuffix(k, ".coalescingScheme") {
						n++
					}
				}
				g.Expect(n).To(Equal(3), "ExtraConfig is %v", ecBefore)
			}, input.Config.GetIntervals("default", "wait-vm-nic-extra-config-synced")...).Should(Succeed())

			By("Swapping eth1 and eth2 in the interface list")
			admitted := env.mutateInterfaces(ctx, key,
				func(ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []vmopv1.VirtualMachineNetworkInterfaceSpec {
					Expect(ifaces[1].Name).To(Equal("eth1"))
					Expect(ifaces[2].Name).To(Equal("eth2"))
					ifaces[1], ifaces[2] = ifaces[2], ifaces[1]
					return ifaces
				})
			Expect(admitted.Spec.Network.Interfaces[1].Name).To(Equal("eth2"))
			Expect(specUnitsByName(Default, admitted)).To(Equal(byName),
				"unit numbers must move with their interfaces")

			By("Verifying no device is replaced and no ExtraConfig moves")
			// There is no positive signal that a reconcile has seen the swap, so
			// this relies on the stability window after the nudge. It would catch
			// positional matching, which acts on the first reconcile.
			env.expectStable(ctx, key, moRef, baseline, units)
			Expect(env.nicExtraConfigNow(ctx, moRef)).To(Equal(ecBefore),
				"per-NIC ExtraConfig changed after the interfaces were reordered")
			for name, unit := range byName {
				Expect(nicAt(env.nicsNow(ctx, moRef), unit)).To(Equal(nicAt(baseline, unit)),
					"interface %s lost its device", name)
			}

			By("Powering on and verifying status still names the same devices")
			vmoperator.PowerOnVirtualMachineAndWait(ctx, input.Config, env.client, key.Namespace, key.Name)
			env.expectStatusUnitNumbers(ctx, key, moRef)
			current, err := utils.GetVirtualMachine(ctx, env.client, key.Namespace, key.Name)
			Expect(err).ToNot(HaveOccurred())
			for _, st := range current.Status.Network.Interfaces {
				if st.Name != "" {
					Expect(st.DeviceKey).To(Equal(nicAt(baseline, byName[st.Name]).Key),
						"status interface %s names a different device than before the reorder", st.Name)
				}
			}
		})

	It("keeps the remaining interfaces on their devices when the first interface is removed",
		Label("core-functional", "experimental"), func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(
				input.Config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

			key, moRef := env.createPoweredOffVM(ctx, "nic-un-remove-", 3)
			backfilled := env.waitForBackfill(ctx, key, 3)
			byName := specUnitsByName(Default, backfilled)
			units := specUnitNumbers(Default, backfilled)
			baseline := env.baselineNICs(ctx, moRef, units)

			// Removing the first interface, not the last, is what tells unit
			// number matching from positional matching: by position, eth1 and
			// eth2 would shift onto the cards of eth0 and eth1.
			By("Removing eth0 from the spec")
			removed := nicAt(baseline, byName["eth0"])
			admitted := env.mutateInterfaces(ctx, key,
				func(ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []vmopv1.VirtualMachineNetworkInterfaceSpec {
					Expect(ifaces[0].Name).To(Equal("eth0"))
					return ifaces[1:]
				})
			Expect(admitted.Spec.Network.Interfaces).To(HaveLen(2))

			By("Waiting for the card of eth0 to be removed")
			remaining := []int32{byName["eth1"], byName["eth2"]}
			slices.Sort(remaining)
			after := env.waitForNICs(ctx, moRef, func(g Gomega, nics []nicHardware) {
				g.Expect(nics).To(HaveLen(2))
				g.Expect(hasNICKey(nics, removed.Key)).To(BeFalse(), "the card of eth0 is still there")
			})

			By("Verifying eth1 and eth2 kept their devices and unit numbers")
			Expect(hardwareUnitNumbers(after)).To(Equal(remaining))
			for _, name := range []string{"eth1", "eth2"} {
				Expect(nicAt(after, byName[name])).To(Equal(nicAt(baseline, byName[name])),
					"interface %s changed device", name)
			}
			current, err := utils.GetVirtualMachine(ctx, env.client, key.Namespace, key.Name)
			Expect(err).ToNot(HaveOccurred())
			Expect(specUnitsByName(Default, current)).To(Equal(
				map[string]int32{"eth1": byName["eth1"], "eth2": byName["eth2"]}))
			env.expectStable(ctx, key, moRef, after, remaining)

			By("Appending an interface and observing which unit number it gets")
			wantNext := lowestFreeNICUnitNumber(remaining)
			admitted = env.mutateInterfaces(ctx, key,
				func(ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []vmopv1.VirtualMachineNetworkInterfaceSpec {
					added := ifaces[0].DeepCopy()
					added.Name = "ethadded"
					added.UnitNumber = nil
					added.MACAddr = ""
					return append(ifaces, *added)
				})
			Expect(admitted.Spec.Network.Interfaces[2].UnitNumber).To(Equal(&wantNext))
			_, _ = fmt.Fprintf(GinkgoWriter,
				"research: freed unit %d, the new interface was assigned %d\n", removed.Unit, wantNext)
			env.waitForNICs(ctx, moRef, func(g Gomega, nics []nicHardware) {
				g.Expect(nics).To(HaveLen(3))
				g.Expect(hasNICKey(nics, nicAt(after, byName["eth1"]).Key)).To(BeTrue())
				g.Expect(hasNICKey(nics, nicAt(after, byName["eth2"]).Key)).To(BeTrue())
				g.Expect(hardwareUnitNumbers(nics)).To(ConsistOf(append(slices.Clone(remaining), wantNext)))
			})

			By("Powering on and verifying status")
			vmoperator.PowerOnVirtualMachineAndWait(ctx, input.Config, env.client, key.Namespace, key.Name)
			env.expectStatusUnitNumbers(ctx, key, moRef)
		})

	It("honors explicit unit numbers on create and backfills the interface without one",
		Label("core-functional", "experimental"), func() {
			// 16 is not where vSphere would place the first card by default, so
			// a create path that dropped the value would be caught.
			key, moRef := env.createPoweredOffVMWithUnits(ctx, "nic-un-explicit-",
				[]*int32{unitPtr(16), nil, unitPtr(9)})
			vm := env.waitForBackfill(ctx, key, 3)

			byName := specUnitsByName(Default, vm)
			Expect(byName["eth0"]).To(Equal(int32(16)))
			Expect(byName["eth2"]).To(Equal(int32(9)))
			units := specUnitNumbers(Default, vm)
			expectValidUnitNumbers(units)

			By("Verifying the cards sit at the spec unit numbers")
			baseline := env.baselineNICs(ctx, moRef, units)

			By("Verifying nothing is replaced by later reconciles")
			// A dropped explicit value would show up as a Remove plus Add with a
			// new generated MAC.
			env.expectStable(ctx, key, moRef, baseline, units)
		})

	It("places interfaces added later at their explicit and assigned unit numbers",
		Label("core-functional", "experimental"), func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(
				input.Config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

			key, moRef := env.createPoweredOffVM(ctx, "nic-un-add2-", 2)
			before := specUnitNumbers(Default, env.waitForBackfill(ctx, key, 2))
			baseline := env.baselineNICs(ctx, moRef, before)

			const explicit int32 = 14
			Expect(before).ToNot(ContainElement(explicit))
			wantAssigned := lowestFreeNICUnitNumber(before)

			By("Appending one interface with an explicit unit number and one without")
			admitted := env.mutateInterfaces(ctx, key,
				func(ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []vmopv1.VirtualMachineNetworkInterfaceSpec {
					for i, u := range []*int32{unitPtr(explicit), nil} {
						added := ifaces[0].DeepCopy()
						added.Name = fmt.Sprintf("ethadded%d", i)
						added.UnitNumber = u
						added.MACAddr = ""
						ifaces = append(ifaces, *added)
					}
					return ifaces
				})
			Expect(admitted.Spec.Network.Interfaces).To(HaveLen(4))
			Expect(admitted.Spec.Network.Interfaces[2].UnitNumber).To(Equal(unitPtr(explicit)))
			Expect(admitted.Spec.Network.Interfaces[3].UnitNumber).To(Equal(unitPtr(wantAssigned)))

			By("Waiting for the new cards at exactly those unit numbers")
			want := append(slices.Clone(before), explicit, wantAssigned)
			slices.Sort(want)
			after := env.baselineNICs(ctx, moRef, want)

			By("Verifying the original cards were not touched")
			for _, n := range baseline {
				Expect(nicAt(after, n.Unit)).To(Equal(n))
			}
			env.expectStable(ctx, key, moRef, after, want)
		})

	It("replaces the device when the unit number of an interface changes",
		Label("extended-functional", "experimental"), func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(
				input.Config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

			key, moRef := env.createPoweredOffVM(ctx, "nic-un-renumber-", 2)
			backfilled := env.waitForBackfill(ctx, key, 2)
			byName := specUnitsByName(Default, backfilled)
			units := specUnitNumbers(Default, backfilled)
			baseline := env.baselineNICs(ctx, moRef, units)
			target := nicAt(baseline, byName["eth0"])
			other := nicAt(baseline, byName["eth1"])

			newUnit := highestFreeNICUnitNumber(units)
			By(fmt.Sprintf("Changing the unit number of eth0 from %d to %d", target.Unit, newUnit))
			env.mutateInterfaces(ctx, key,
				func(ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []vmopv1.VirtualMachineNetworkInterfaceSpec {
					Expect(ifaces[0].Name).To(Equal("eth0"))
					ifaces[0].UnitNumber = unitPtr(newUnit)
					return ifaces
				})

			By("Waiting for the old card to be replaced by one at the new unit number")
			newUnits := []int32{byName["eth1"], newUnit}
			slices.Sort(newUnits)
			after := env.waitForNICs(ctx, moRef, func(g Gomega, nics []nicHardware) {
				g.Expect(hardwareUnitNumbers(nics)).To(Equal(newUnits))
				g.Expect(hasNICKey(nics, target.Key)).To(BeFalse(), "the old card is still there")
			})
			replacement := nicAt(after, newUnit)
			Expect(replacement.Key).ToNot(Equal(target.Key))
			if env.isVDS() {
				// A new Key alone does not prove a new device, since an Edit that
				// only changed the unit number would also get one. A new generated
				// MAC does. NSX-T and VPC pin the MAC, so it cannot tell there.
				Expect(replacement.MAC).ToNot(Equal(target.MAC),
					"the card was edited in place rather than replaced")
			}
			Expect(nicAt(after, other.Unit)).To(Equal(other), "the other card must not be touched")

			current, err := utils.GetVirtualMachine(ctx, env.client, key.Namespace, key.Name)
			Expect(err).ToNot(HaveOccurred())
			Expect(specUnitsByName(Default, current)).To(Equal(
				map[string]int32{"eth0": newUnit, "eth1": byName["eth1"]}))
			env.expectStable(ctx, key, moRef, after, newUnits)

			By("Powering on and verifying status reports the new unit number")
			vmoperator.PowerOnVirtualMachineAndWait(ctx, input.Config, env.client, key.Namespace, key.Name)
			env.expectStatusUnitNumbers(ctx, key, moRef)

			By("Verifying a unit number cannot be changed while the VM is powered on")
			another := highestFreeNICUnitNumber(newUnits)
			vm, err := utils.GetVirtualMachine(ctx, env.client, key.Namespace, key.Name)
			Expect(err).ToNot(HaveOccurred())
			patch := ctrlclient.MergeFrom(vm.DeepCopy())
			vm.Spec.Network.Interfaces[0].UnitNumber = unitPtr(another)
			err = env.client.Patch(ctx, vm, patch)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInvalid(err) || apierrors.IsForbidden(err)).To(BeTrue(),
				"expected an admission rejection, got: %v", err)
			Expect(err.Error()).To(ContainSubstring("unitNumber"))
		})

	It("keeps unit numbers when the VM is updated through an older API version",
		Label("core-functional", "experimental"), func() {
			// The values are explicit and not the lowest free ones (7, 8). If the
			// v1alpha2 update lost them, the update mutator would reassign the
			// lowest free values, which would otherwise look like preserved
			// ones.
			key, moRef := env.createPoweredOffVMWithUnits(ctx, "nic-un-legacy-",
				[]*int32{unitPtr(16), unitPtr(12)})
			backfilled := env.waitForBackfill(ctx, key, 2)
			byName := specUnitsByName(Default, backfilled)
			units := specUnitNumbers(Default, backfilled)
			baseline := env.baselineNICs(ctx, moRef, units)

			By("Updating an unrelated field as a v1alpha2 client")
			// A full Update, not a Patch: only a PUT of the older object sends the
			// whole spec through the conversion chain and could drop unitNumber.
			const label = "e2e.vmoperator.vmware.com/legacy-client"
			Eventually(func(g Gomega) {
				old := &vmopv1a2.VirtualMachine{}
				g.Expect(env.client.Get(ctx, key, old)).To(Succeed())
				if old.Labels == nil {
					old.Labels = map[string]string{}
				}
				old.Labels[label] = "true"
				g.Expect(env.client.Update(ctx, old)).To(Succeed())
			}, input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())

			By("Reading the VM back as v1alpha6")
			current, err := utils.GetVirtualMachine(ctx, env.client, key.Namespace, key.Name)
			Expect(err).ToNot(HaveOccurred())
			Expect(current.Labels).To(HaveKeyWithValue(label, "true"))
			Expect(specUnitsByName(Default, current)).To(Equal(
				map[string]int32{"eth0": 16, "eth1": 12}))
			Expect(specUnitsByName(Default, current)).To(Equal(byName))
			env.expectStable(ctx, key, moRef, baseline, units)
		})

	It("adds an interface to a powered on VM only after the next power cycle",
		Label("extended-functional", "experimental"), func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(
				input.Config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

			key, moRef := env.createPoweredOffVM(ctx, "nic-un-hotadd-", 2)
			before := specUnitNumbers(Default, env.waitForBackfill(ctx, key, 2))
			baseline := env.baselineNICs(ctx, moRef, before)
			vmoperator.PowerOnVirtualMachineAndWait(ctx, input.Config, env.client, key.Namespace, key.Name)

			By("Appending an interface without a unit number to the powered on VM")
			wantAssigned := lowestFreeNICUnitNumber(before)
			admitted := env.mutateInterfaces(ctx, key,
				func(ifaces []vmopv1.VirtualMachineNetworkInterfaceSpec) []vmopv1.VirtualMachineNetworkInterfaceSpec {
					added := ifaces[0].DeepCopy()
					added.Name = "ethadded"
					added.UnitNumber = nil
					added.MACAddr = ""
					return append(ifaces, *added)
				})
			Expect(admitted.Spec.Network.Interfaces[2].UnitNumber).To(Equal(unitPtr(wantAssigned)))

			By("Verifying the card is not created while the VM is powered on")
			withAssigned := append(slices.Clone(before), wantAssigned)
			slices.Sort(withAssigned)
			env.expectStable(ctx, key, moRef, baseline, withAssigned)

			By("Power cycling the VM")
			powerCycleVM(ctx, env.client, input.Config, key)

			By("Verifying the card now exists at the assigned unit number")
			after := env.waitForNICs(ctx, moRef, func(g Gomega, nics []nicHardware) {
				g.Expect(hardwareUnitNumbers(nics)).To(Equal(withAssigned))
			})
			for _, n := range baseline {
				Expect(nicAt(after, n.Unit)).To(Equal(n), "an original card changed")
			}
			env.expectStatusUnitNumbers(ctx, key, moRef)
		})
}
