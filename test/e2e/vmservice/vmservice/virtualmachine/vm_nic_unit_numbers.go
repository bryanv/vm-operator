// Copyright (c) 2025 Broadcom. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"

	appputil "github.com/vmware-tanzu/vm-operator/test/e2e/appple2e/util"
	e2essh "github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/ssh"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/testbed"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/vcenter"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/wcp"
	"github.com/vmware-tanzu/vm-operator/test/e2e/manifestbuilders"
	"github.com/vmware-tanzu/vm-operator/test/e2e/utils"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/common"
	e2eConfig "github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/config"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/consts"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/lib/vmoperator"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/skipper"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/vmservice"
	"github.com/vmware-tanzu/vm-operator/test/e2e/wcpframework"
)

// VMNICUnitNumbersSpec exercises the VMNetworkUnitNumbers feature end to end:
// spec.network.interfaces[i].unitNumber placement (explicit and
// admission-assigned), the schema-upgrade backfill, unit-number-based device
// identity during reconcile (exact-only matching and replace-not-Edit
// convergence), status reporting, and the annotation-based conversion restore.
//
// FLAG COMBINATION (plan.md I16): scenarios asserting a device change
// (explicit placement, renumber/replace, removal) only hold under a flag
// combination that reaches a converging reconcile path:
//
//   - getConfigSpecForPoweredOffVM computes NIC device changes
//     unconditionally (VMResize off), and
//   - resizeVMWhenPoweredStateOff only computes them when MutableNetworks is
//     on, and
//   - the powered-off→on path with VMResize on computes none at all.
//
// These scenarios create powered-off VMs and reconcile them there, so they
// hold on a default (VMResize-off) deployment. The BeforeEach detects the
// VMResize FSS and the MutableNetworks capability on the testbed, and the
// device-change-asserting Its SKIP (rather than fail) when the environment
// cannot reach a converging path (VMResize FSS on and MutableNetworks off).
// Backfill/status-only scenarios are ungated by this. The
// admitted-but-not-applied scenario documents — rather than fights — the
// powered-on behavior (no NIC device changes are computed while powered on).
//
// NOT covered here, deliberately (see tasks.md T022):
//   - admission-validation scenarios (duplicate, out-of-range, powered-on
//     change rules): pure webhook logic, exhaustively covered by the
//     nicUnitNumberTests() unit tests (T013);
//   - brownfield backfill: the deployment-level capability cannot be toggled
//     mid-run on a shared cluster; covered by vcsim integration tests (T015);
//   - snapshot-revert interplay: same state-control limitation; covered by
//     vcsim integration tests (T031).
type VMNICUnitNumbersSpecInput struct {
	Config           *e2eConfig.E2EConfig
	ClusterProxy     wcpframework.WCPClusterProxyInterface
	WCPClient        wcp.WorkloadManagementAPI
	ArtifactFolder   string
	WCPNamespaceName string
}

// nicDevSnapshot captures the identity-bearing properties of one observed
// ethernet device. MAC — not Key — is the reliable "did this device change"
// signal: on at least one real-VC build an ethernet card's Key is derived
// deterministically from its unit number (T001 E06), so a same-slot
// Remove+Add reproduces the same Key.
type nicDevSnapshot struct {
	Key         int32
	Unit        *int32
	MAC         string
	AddressType string
	ExternalID  string
}

func VMNICUnitNumbersSpec(ctx context.Context, inputGetter func() VMNICUnitNumbersSpecInput) {
	const specName = "vm-nic-unit-numbers"

	var (
		input            VMNICUnitNumbersSpecInput
		config           *e2eConfig.E2EConfig
		clusterProxy     *common.VMServiceClusterProxy
		svClusterClient  ctrlclient.Client
		clusterResources *e2eConfig.Resources
		vmNamespaceName  string

		vimClient     *vim25.Client
		propCollector *property.Collector

		linuxImageDisplayName string
		linuxVMIName          string

		// Testbed flag/capability detection for the I16 skip (set in
		// BeforeEach).
		isVMMutableNetworksCapEnabled bool
		isVMResizeFSSEnabled          bool
	)

	BeforeEach(func() {
		input = inputGetter()
		Expect(input.Config).ToNot(BeNil(), "Invalid argument. input.Config can't be nil when calling %s spec", specName)
		Expect(input.Config.InfraConfig).ToNot(BeNil(), "Invalid argument. input.Config.InfraConfig can't be nil when calling %s spec", specName)
		skipper.SkipUnlessInfraIs(input.Config.InfraConfig.InfraName, consts.WCP)

		Expect(input.ClusterProxy).ToNot(BeNil(), "Invalid argument. input.ClusterProxy can't be nil when calling %s spec", specName)
		Expect(input.WCPNamespaceName).ToNot(BeEmpty(), "Invalid argument. input.WCPNamespaceName can't be empty when calling %s spec", specName)
		Expect(os.MkdirAll(input.ArtifactFolder, 0755)).To(Succeed(), "Invalid argument. input.ArtifactFolder can't be created for %s spec", specName)

		config = input.Config
		clusterProxy = input.ClusterProxy.(*common.VMServiceClusterProxy)
		svClusterClient = clusterProxy.GetClient()

		skipper.SkipUnlessSupervisorCapabilityEnabled(ctx, clusterProxy,
			consts.VMNetworkUnitNumbersCapabilityName)
		clusterResources = config.InfraConfig.ManagementClusterConfig.Resources
		vmNamespaceName = input.WCPNamespaceName

		linuxImageDisplayName = vmservice.GetDefaultImageDisplayName(clusterResources)
		linuxVMIName = vmoperator.WaitForVirtualMachineImageName(
			ctx, &config.Config, svClusterClient, vmNamespaceName, linuxImageDisplayName)

		vimClient = vcenter.NewVimClientFromKubeconfig(ctx, clusterProxy.GetKubeconfigPath())
		DeferCleanup(vcenter.LogoutVimClient, vimClient)
		propCollector = property.DefaultCollector(vimClient)

		// Detect the testbed's flag combination for the I16 skip, mirroring
		// vm_networking.go's BeforeEach detection.
		sshCommandRunner, _ := e2essh.NewSSHCommandRunner(
			vcenter.GetVCPNIDFromKubeconfigFile(ctx, clusterProxy.GetKubeconfigPath()),
			vcenter.VCSSHPort, testbed.RootUsername, []ssh.AuthMethod{ssh.Password(testbed.RootPassword)})
		isAsyncSvUpgradeEnabled, _ := appputil.IsFSSEnabled(sshCommandRunner, utils.SupervisorAsyncUpgradeFSS)
		isVMResizeFSSEnabled, _ = appputil.IsFSSEnabled(sshCommandRunner, "FSS_WCP_VMSERVICE_RESIZE")
		isVMMutableNetworksCapEnabled = utils.IsSupervisorCapabilityEnabled(ctx,
			clusterProxy.GetClientSet(), clusterProxy.GetDynamicClient(),
			"supports_VM_service_mutable_networks", isAsyncSvUpgradeEnabled)
	})

	// skipUnlessConvergingPath skips the device-change-asserting scenarios
	// when the environment cannot reach a reconcile path that computes NIC
	// device changes (plan.md I16): with the VMResize FSS enabled and the
	// MutableNetworks capability off, neither
	// resizeVMWhenPoweredStateOff nor getConfigSpecForPoweredOffVM runs.
	skipUnlessConvergingPath := func() {
		if isVMResizeFSSEnabled && !isVMMutableNetworksCapEnabled {
			Skip("no converging reconcile path: VMResize FSS is enabled and the MutableNetworks capability is off (plan.md I16)")
		}
	}

	// ── In-spec helpers ──────────────────────────────────────────────────────

	// buildNICUnitVM builds a VM with explicit network interfaces. Interfaces
	// carry no Network ref: the mutating webhook defaults it, which keeps the
	// scenarios independent of the NSX-T/VPC-vs-VDS testbed flavor (the
	// network re-point scenario overrides this on VPC testbeds).
	buildNICUnitVM := func(
		name string,
		powerState vmopv1.VirtualMachinePowerState,
		ifaces ...vmopv1.VirtualMachineNetworkInterfaceSpec) *vmopv1.VirtualMachine {

		vm := &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: vmNamespaceName,
			},
			Spec: vmopv1.VirtualMachineSpec{
				ClassName:    clusterResources.VMClassName,
				ImageName:    linuxVMIName,
				StorageClass: clusterResources.StorageClassName,
				Bootstrap: &vmopv1.VirtualMachineBootstrapSpec{
					Disabled: true,
				},
				PowerState:   powerState,
				PowerOffMode: vmopv1.VirtualMachinePowerOpModeHard,
			},
		}
		if len(ifaces) > 0 {
			vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
				Interfaces: ifaces,
			}
		}
		return vm
	}

	// createNICUnitVM creates the VM and waits for it to exist and have a
	// MOID, registering deletion cleanup.
	createNICUnitVM := func(vm *vmopv1.VirtualMachine) {
		Expect(svClusterClient.Create(ctx, vm)).To(Succeed(),
			"failed to create VirtualMachine %s/%s", vm.Namespace, vm.Name)
		vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, vmNamespaceName, vm.Name)
		vmoperator.WaitForVirtualMachineMOID(ctx, config, svClusterClient, vmNamespaceName, vm.Name)
		DeferCleanup(func() {
			vmoperator.DeleteVirtualMachine(ctx, svClusterClient, vmNamespaceName, vm.Name)
			vmoperator.WaitForVirtualMachineToBeDeleted(ctx, config, svClusterClient, vmNamespaceName, vm.Name)
		})
	}

	// getNICUnitVM fetches the VM CR.
	getNICUnitVM := func(name string) *vmopv1.VirtualMachine {
		vm := &vmopv1.VirtualMachine{}
		key := ctrlclient.ObjectKey{Name: name, Namespace: vmNamespaceName}
		Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
		return vm
	}

	// updateNICUnitVM applies a mutation to the VM spec with the standard
	// retry-on-conflict loop.
	updateNICUnitVM := func(name string, mutate func(*vmopv1.VirtualMachine)) {
		key := ctrlclient.ObjectKey{Name: name, Namespace: vmNamespaceName}
		Eventually(func(g Gomega) {
			vm := &vmopv1.VirtualMachine{}
			g.Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
			mutate(vm)
			g.Expect(svClusterClient.Update(ctx, vm)).To(Succeed())
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).
			Should(Succeed(), "timed out updating VirtualMachine %s/%s", vmNamespaceName, name)
	}

	// ethDevicesOf retrieves the VM's observed ethernet devices from vSphere.
	ethDevicesOf := func(name string) []vimtypes.BaseVirtualDevice {
		vmMoID := vmoperator.GetVirtualMachineMOID(ctx, svClusterClient, vmNamespaceName, name)
		vmMoRef := vimtypes.ManagedObjectReference{Type: "VirtualMachine", Value: vmMoID}
		var vmMO mo.VirtualMachine
		Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
		return object.VirtualDeviceList(vmMO.Config.Hardware.Device).
			SelectByType((*vimtypes.VirtualEthernetCard)(nil))
	}

	// snapshotEthDevices maps observed ethernet devices by unit number.
	snapshotEthDevices := func(name string) map[int32]nicDevSnapshot {
		out := map[int32]nicDevSnapshot{}
		for _, dev := range ethDevicesOf(name) {
			eth := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
			unit := dev.GetVirtualDevice().UnitNumber
			if unit == nil {
				continue
			}
			out[*unit] = nicDevSnapshot{
				Key:         eth.Key,
				Unit:        unit,
				MAC:         eth.MacAddress,
				AddressType: eth.AddressType,
				ExternalID:  eth.ExternalId,
			}
		}
		return out
	}

	// waitForBackfilledUnits waits until every spec interface carries a unit
	// number (the schema-upgrade backfill), all in 7..16 and unique.
	waitForBackfilledUnits := func(name string, numIfaces int) {
		Eventually(func(g Gomega) {
			vm := getNICUnitVM(name)
			g.Expect(vm.Spec.Network).ToNot(BeNil())
			g.Expect(vm.Spec.Network.Interfaces).To(HaveLen(numIfaces))

			seen := map[int32]bool{}
			for i := range vm.Spec.Network.Interfaces {
				u := vm.Spec.Network.Interfaces[i].UnitNumber
				g.Expect(u).ToNot(BeNil(), "interface %d not yet backfilled", i)
				g.Expect(*u).To(BeNumerically(">=", 7))
				g.Expect(*u).To(BeNumerically("<=", 16))
				g.Expect(seen).ToNot(HaveKey(*u))
				seen[*u] = true
			}
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).
			Should(Succeed(), "timed out waiting for backfilled unit numbers on %s/%s", vmNamespaceName, name)
	}

	// waitForStatusUnits waits until the Tools-reported interface status
	// entries all carry a unitNumber matching the observed device slots.
	waitForStatusUnits := func(name string) {
		Eventually(func(g Gomega) {
			vm := getNICUnitVM(name)
			g.Expect(vm.Status.Network).ToNot(BeNil())
			ifaces := vm.Status.Network.Interfaces
			g.Expect(ifaces).ToNot(BeEmpty())

			observed := snapshotEthDevices(name)
			for i := range ifaces {
				unit := ifaces[i].UnitNumber
				g.Expect(unit).ToNot(BeNil(), "status entry %d has no unitNumber yet", i)
				dev, ok := observed[*unit]
				g.Expect(ok).To(BeTrue(), "status unit %d has no observed device", *unit)
				g.Expect(ifaces[i].DeviceKey).To(Equal(dev.Key))
			}
		}, config.GetIntervals("default", "wait-virtual-machine-ip")...).
			Should(Succeed(), "timed out waiting for status unit numbers on %s/%s", vmNamespaceName, name)
	}

	// specUnitsByName returns the spec unit numbers keyed by interface name.
	specUnitsByName := func(name string) map[string]*int32 {
		vm := getNICUnitVM(name)
		out := map[string]*int32{}
		if vm.Spec.Network != nil {
			for i := range vm.Spec.Network.Interfaces {
				out[vm.Spec.Network.Interfaces[i].Name] = vm.Spec.Network.Interfaces[i].UnitNumber
			}
		}
		return out
	}

	// statusUnitsByName returns the status unit numbers keyed by entry name.
	statusUnitsByName := func(name string) map[string]*int32 {
		vm := getNICUnitVM(name)
		out := map[string]*int32{}
		if vm.Status.Network != nil {
			for i := range vm.Status.Network.Interfaces {
				out[vm.Status.Network.Interfaces[i].Name] = vm.Status.Network.Interfaces[i].UnitNumber
			}
		}
		return out
	}

	// setPowerState updates the VM's power state and waits for it.
	setPowerState := func(name string, powerState vmopv1.VirtualMachinePowerState) {
		updateNICUnitVM(name, func(vm *vmopv1.VirtualMachine) {
			vm.Spec.PowerState = powerState
		})
		vmoperator.WaitForVirtualMachinePowerState(
			ctx, config, svClusterClient, vmNamespaceName, name, string(powerState))
	}

	// ── Scenarios ────────────────────────────────────────────────────────────

	It("backfills unit numbers on create and assigns them at admission for additions",
		Label("core-functional", "experimental"), func() {
			// The backfill/admission-assignment assertions below do not need a
			// converging reconcile path; only the final eth2 device assertion
			// (after the power cycle) does, and it skips there.

			vmName := fmt.Sprintf("%s-backfill-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOn,
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"},
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1"})

			By("Creating a VM with two interfaces and no unit numbers")
			createNICUnitVM(vm)

			By("Asserting admission left the spec unit numbers untouched")
			// The mutation webhook does not run on create (oldVM == nil), so
			// the created spec must carry the values exactly as submitted:
			// all nil. (Best-effort: a very fast first reconcile could already
			// have backfilled.)
			units := specUnitsByName(vmName)
			Expect(units["eth0"]).To(BeNil())
			Expect(units["eth1"]).To(BeNil())

			By("Waiting for the schema-upgrade backfill to record observed slots")
			waitForBackfilledUnits(vmName, 2)

			By("Appending an interface and asserting admission assigns the next free slot")
			updateNICUnitVM(vmName, func(vm *vmopv1.VirtualMachine) {
				vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces,
					vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth2"})
			})
			// The update-path mutator assigns at admission: by the time the
			// update succeeds, eth2 carries a value in range and unique.
			units = specUnitsByName(vmName)
			Expect(units).To(HaveLen(3))
			Expect(units["eth2"]).ToNot(BeNil())
			Expect(*units["eth2"]).To(BeNumerically(">=", 7))
			Expect(*units["eth2"]).To(BeNumerically("<=", 16))
			Expect(*units["eth2"]).ToNot(Equal(*units["eth0"]))
			Expect(*units["eth2"]).ToNot(Equal(*units["eth1"]))

			By("Powering off and back on so the admitted interface's device is created")
			// I5: the powered-on reconcile computes no NIC device changes, so
			// eth2's device only appears at the next powered-off reconcile.
			// The device assertions below need a converging path.
			skipUnlessConvergingPath()
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOff)
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOn)

			By("Waiting for Tools and asserting status matches the observed devices")
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
			waitForStatusUnits(vmName)

			By("Asserting observed devices sit at the declared slots")
			observed := snapshotEthDevices(vmName)
			for _, ifaceName := range []string{"eth0", "eth1", "eth2"} {
				u := specUnitsByName(vmName)[ifaceName]
				Expect(observed).To(HaveKey(*u))
			}
		})

	It("honours an explicit unitNumber on create", Label("core-functional", "experimental"), func() {
		vmName := fmt.Sprintf("%s-explicit-%s", specName, capiutil.RandomString(4))
		vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOn,
			vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0", UnitNumber: ptr.To(int32(9))})

		By("Creating a VM whose interface pins unit 9")
		createNICUnitVM(vm)
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)

		By("Asserting the device landed at slot 9 and the spec value is unchanged")
		Expect(specUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(int32(9))))
		observed := snapshotEthDevices(vmName)
		Expect(observed).To(HaveKey(int32(9)))

		By("Asserting status reports the observed unit")
		waitForStatusUnits(vmName)
		Expect(statusUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(int32(9))))
	})

	It("claims explicit slots exclusively and backfills the remainder (exact-only matching)",
		Label("core-functional", "experimental"), func() {
			vmName := fmt.Sprintf("%s-mixed-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOn,
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0", UnitNumber: ptr.To(int32(16))},
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1"})

			By("Creating a VM with one explicit unit (16) and one unset")
			createNICUnitVM(vm)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)

			By("Asserting the unset interface's backfilled value differs from and is unique to the explicit one")
			waitForBackfilledUnits(vmName, 2)
			units := specUnitsByName(vmName)
			Expect(units["eth0"]).To(Equal(ptr.To(int32(16))))
			Expect(units["eth1"]).ToNot(Equal(ptr.To(int32(16))))
			Expect(*units["eth1"]).To(BeNumerically(">=", 7))
			Expect(*units["eth1"]).To(BeNumerically("<=", 16))

			By("Asserting the devices are at the declared slots")
			observed := snapshotEthDevices(vmName)
			Expect(observed).To(HaveKey(int32(16)))
			Expect(observed).To(HaveKey(*units["eth1"]))

			By("Asserting status binds eth0's identity to its declared slot")
			// The status entry joins the spec interface to its device via the
			// authoritative exact-only mapping: an entry named eth0 reporting
			// unit 16 proves the explicit interface — not a fallback-matching
			// one — owns the slot-16 device.
			waitForStatusUnits(vmName)
			Expect(statusUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(int32(16))))
		})

	It("admits a powered-on NIC add without a device change until the next power-off",
		Label("core-functional", "experimental"), func() {
			skipUnlessConvergingPath()

			// I5: poweredOnReconfigure computes no NIC device changes today —
			// this documents the admitted-now, applied-at-next-power-off
			// semantics rather than a unit-number defect.
			vmName := fmt.Sprintf("%s-hotadd-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOn,
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"})

			By("Creating a powered-on VM and settling its unit numbers")
			createNICUnitVM(vm)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
			waitForBackfilledUnits(vmName, 1)
			waitForStatusUnits(vmName)
			eth0UnitBefore := *specUnitsByName(vmName)["eth0"]
			eth0KeyBefore := snapshotEthDevices(vmName)[eth0UnitBefore].Key

			By("Appending an interface to the running VM and asserting it is admitted")
			updateNICUnitVM(vmName, func(vm *vmopv1.VirtualMachine) {
				vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces,
					vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1"})
			})
			Expect(specUnitsByName(vmName)["eth1"]).ToNot(BeNil())

			By("Asserting no new device or status entry appears while the VM stays powered on")
			Consistently(func(g Gomega) {
				g.Expect(ethDevicesOf(vmName)).To(HaveLen(1))
				vm := getNICUnitVM(vmName)
				if vm.Status.Network != nil {
					g.Expect(vm.Status.Network.Interfaces).To(HaveLen(1),
						"no status entry should appear for an interface with no device")
				}
			}, "30s", "5s").Should(Succeed(),
				"no new NIC device should be created while the VM is powered on")

			By("Powering off and back on, then asserting the device is created")
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOff)
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOn)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)

			observed := snapshotEthDevices(vmName)
			Expect(observed).To(HaveLen(2))
			eth1Unit := specUnitsByName(vmName)["eth1"]
			Expect(observed).To(HaveKey(*eth1Unit))

			By("Asserting the pre-existing NIC's spec and status unit numbers are unchanged")
			Expect(specUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(eth0UnitBefore)))
			Expect(snapshotEthDevices(vmName)[eth0UnitBefore].Key).To(Equal(eth0KeyBefore))
			waitForStatusUnits(vmName)
			Expect(statusUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(eth0UnitBefore)))
		})

	It("replaces the device when an existing interface is renumbered while powered off",
		Label("core-functional", "experimental"), func() {
			skipUnlessConvergingPath()

			// Replacement, not a slot move: do NOT assert via a changed Key —
			// on at least one real-VC build the Key is derived from the unit
			// (T001 E06), so a same-slot Remove+Add reproduces the same Key.
			// The MAC assertion is branched on the interface's addressing mode
			// (P1-5): a Manual MAC is pinned by the spec and carries over, a
			// Generated MAC is re-derived and differs.
			//
			// The two interfaces carry DISTINCT Manual MACs so the companion
			// occupied-slot SWAP below forces replacement on both sides (with
			// Generated MACs and identical backing, a swap may trade slots
			// with zero device changes — plan Design point 3 — making an
			// own-identity assertion meaningless).
			vmName := fmt.Sprintf("%s-renum-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOff,
				vmopv1.VirtualMachineNetworkInterfaceSpec{
					Name:    "eth0",
					MACAddr: "aa:bb:cc:dd:ee:01",
				},
				vmopv1.VirtualMachineNetworkInterfaceSpec{
					Name:    "eth1",
					MACAddr: "aa:bb:cc:dd:ee:02",
				})

			By("Creating a powered-off VM and settling its unit numbers")
			createNICUnitVM(vm)
			waitForBackfilledUnits(vmName, 2)

			By("Powering on and settling status, then powering back off")
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOn)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
			waitForStatusUnits(vmName)
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOff)

			eth0Unit := *specUnitsByName(vmName)["eth0"]
			eth1Unit := *specUnitsByName(vmName)["eth1"]
			devsBefore := snapshotEthDevices(vmName)
			eth0MACBefore := devsBefore[eth0Unit].MAC
			eth0KeyBefore := devsBefore[eth0Unit].Key

			By("Renumbering eth0 to a different, currently-unoccupied unit")
			newUnit := int32(16)
			if eth1Unit == 16 {
				newUnit = 15
			}
			Expect(newUnit).ToNot(Equal(eth0Unit))
			Expect(newUnit).ToNot(Equal(eth1Unit))
			updateNICUnitVM(vmName, func(vm *vmopv1.VirtualMachine) {
				vm.Spec.Network.Interfaces[0].UnitNumber = ptr.To(newUnit)
			})

			By("Asserting the old device is gone and a new device sits at the new slot")
			Eventually(func(g Gomega) {
				observed := snapshotEthDevices(vmName)
				g.Expect(observed).To(HaveLen(2))
				g.Expect(observed).ToNot(HaveKey(eth0Unit), "the old device at the old slot should be removed")
				g.Expect(observed).To(HaveKey(newUnit))
				// (b) is necessary but not sufficient for "replace"; the MAC
				// assertion below is what proves it.
				g.Expect(observed[newUnit].Key).ToNot(Equal(eth0KeyBefore))
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())

			By("Asserting the replacement carries the interface's identity at the new slot")
			devsAfter := snapshotEthDevices(vmName)
			if devsBefore[eth0Unit].AddressType == string(vimtypes.VirtualEthernetCardMacTypeManual) {
				// A Manual MAC is pinned by the spec: it carries over to the
				// replacement device.
				Expect(devsAfter[newUnit].MAC).To(Equal(eth0MACBefore))
			} else {
				// A Generated MAC is re-derived for the new device.
				Expect(devsAfter[newUnit].MAC).ToNot(Equal(eth0MACBefore))
			}
			Expect(specUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(newUnit)))

			By("Asserting eth1 is untouched")
			Expect(devsAfter[eth1Unit].Key).To(Equal(devsBefore[eth1Unit].Key))
			Expect(devsAfter[eth1Unit].MAC).To(Equal(devsBefore[eth1Unit].MAC))
			Expect(specUnitsByName(vmName)["eth1"]).To(Equal(ptr.To(eth1Unit)))

			By("Powering on and asserting both spec and status reflect the new unit")
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOn)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
			waitForStatusUnits(vmName)
			Expect(statusUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(newUnit)))
			Expect(statusUnitsByName(vmName)["eth1"]).To(Equal(ptr.To(eth1Unit)))

			// Companion (i): renumber onto an OCCUPIED slot. Plan Design
			// point 3: the occupied-slot case is a SWAP — both interfaces
			// renumber in ONE update (A 8→9, B 9→8); two entries at one number
			// would be admission-rejected (uniqueness, and the powered-on
			// change rule). The VM must be powered OFF: each interface's
			// declared slot is occupied by the OTHER interface's device, and —
			// with distinct Manual MACs — each side's comparison fails, so both
			// devices are replaced and neither interface inherits the other's
			// identity.
			By("Powering off before the occupied-slot swap")
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOff)

			By("Swapping the CURRENT unit numbers of eth0 and eth1 in one update")
			// eth0 currently sits at newUnit (from the primary renumber above);
			// eth1 is still at eth1Unit. The swap sends eth0 to eth1's occupied
			// slot and eth1 to eth0's now-occupied slot — both occupied-slot
			// replacements, with distinct Manual MACs forcing replacement on
			// both sides.
			updateNICUnitVM(vmName, func(vm *vmopv1.VirtualMachine) {
				vm.Spec.Network.Interfaces[0].UnitNumber = ptr.To(eth1Unit)
				vm.Spec.Network.Interfaces[1].UnitNumber = ptr.To(newUnit)
			})

			By("Asserting each slot ends up holding its claiming interface's OWN identity")
			Eventually(func(g Gomega) {
				observed := snapshotEthDevices(vmName)
				g.Expect(observed).To(HaveLen(2))
				g.Expect(observed).To(HaveKey(eth1Unit))
				g.Expect(observed).To(HaveKey(newUnit))
				// eth0's device at eth1's former slot carries eth0's own MAC —
				// NOT the previous occupant's (eth1's).
				g.Expect(observed[eth1Unit].MAC).To(Equal(eth0MACBefore))
				// Symmetrically, eth1's device at eth0's former slot carries
				// eth1's own MAC. (The spec pins no ExternalID here, so the MAC
				// is the pinned-identity signal; with a provider that pins
				// ExternalID, that comparison would be asserted the same way.)
				g.Expect(observed[newUnit].MAC).To(Equal(devsBefore[eth1Unit].MAC))
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())

			By("Asserting the spec carries the swapped, distinct, in-range units")
			units := specUnitsByName(vmName)
			Expect(units["eth0"]).To(Equal(ptr.To(eth1Unit)))
			Expect(units["eth1"]).To(Equal(ptr.To(newUnit)))
			Expect(eth1Unit).To(BeNumerically(">=", 7))
			Expect(eth1Unit).To(BeNumerically("<=", 16))
		})

	It("replaces the device when a numbered interface's network is re-pointed without renumbering",
		Label("core-functional", "experimental"), func() {
			skipUnlessConvergingPath()

			// Companion (ii) to the renumber scenario: once an interface
			// carries a unitNumber, a property change (network re-point) is
			// converged by REPLACING the device at that unit number rather
			// than editing it in place — the interim replace-not-Edit
			// behavior. Assert on MAC (Generated), not Key (E06).
			if !vmoperator.IsNetworkNsxtVPC(ctx, svClusterClient, config) {
				Skip("Test requires VPC networking environment to create SubnetSet")
			}

			vmName := fmt.Sprintf("%s-repoint-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOff,
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"})

			By("Creating a powered-off VM and settling its unit numbers")
			createNICUnitVM(vm)
			waitForBackfilledUnits(vmName, 1)

			By("Creating a custom SubnetSet to re-point to")
			subnetSetName := fmt.Sprintf("%s-subnet-%s", specName, capiutil.RandomString(4))
			Eventually(func(g Gomega) {
				sYaml := utils.CreateSubnetOrSubnetSetYaml(utils.SubnetSetKind, subnetSetName, vmNamespaceName, utils.DHCPConfig, true)
				g.Expect(clusterProxy.CreateWithArgs(ctx, sYaml)).To(Succeed(), "failed to create the SubnetSet: %s", string(sYaml))
			}, config.GetIntervals("default", "wait-subnet-creation")...).Should(Succeed(), "timed out creating SubnetSet")
			vmservice.VerifySubnetOrSubnetSetCreation(ctx, config, svClusterClient, vmNamespaceName, subnetSetName, utils.SubnetSetKind)
			DeferCleanup(func() {
				vmoperator.DeleteSubnetOrSubnetSet(ctx, svClusterClient, vmNamespaceName, subnetSetName, utils.SubnetSetKind)
				vmoperator.WaitForSubnetOrSubnetSetToBeDeleted(ctx, config, svClusterClient, vmNamespaceName, subnetSetName, utils.SubnetSetKind)
			})

			vmUnit := *specUnitsByName(vmName)["eth0"]
			macBefore := snapshotEthDevices(vmName)[vmUnit].MAC

			By("Re-pointing eth0's network to the custom SubnetSet, keeping its unit number")
			updateNICUnitVM(vmName, func(vm *vmopv1.VirtualMachine) {
				netRef := &vmopv1common.PartialObjectRef{Name: subnetSetName}
				netRef.Kind = utils.SubnetSetKind
				netRef.APIVersion = "crd.nsx.vmware.com/v1alpha1"
				vm.Spec.Network.Interfaces[0].Network = netRef
			})

			By("Asserting the device at the (unchanged) unit number is replaced, not edited")
			Eventually(func(g Gomega) {
				observed := snapshotEthDevices(vmName)
				g.Expect(observed).To(HaveKey(vmUnit))
				g.Expect(observed[vmUnit].MAC).ToNot(Equal(macBefore))
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())

			By("Asserting the spec unit number is unchanged")
			Expect(specUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(vmUnit)))
		})

	It("removes a NIC's device when the interface is deleted, and leaves survivors untouched",
		Label("core-functional", "experimental"), func() {
			skipUnlessConvergingPath()

			vmName := fmt.Sprintf("%s-remove-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOff,
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"},
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1"},
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth2"})

			By("Creating a powered-off VM with three interfaces and settling")
			createNICUnitVM(vm)
			waitForBackfilledUnits(vmName, 3)
			devsBefore := snapshotEthDevices(vmName)
			unitsBefore := specUnitsByName(vmName)
			Expect(devsBefore).To(HaveLen(3))

			By("Deleting eth1 from the spec")
			updateNICUnitVM(vmName, func(vm *vmopv1.VirtualMachine) {
				kept := vm.Spec.Network.Interfaces[:0]
				for _, iface := range vm.Spec.Network.Interfaces {
					if iface.Name != "eth1" {
						kept = append(kept, iface)
					}
				}
				vm.Spec.Network.Interfaces = kept
			})

			By("Asserting the removed interface's device (by Key) is gone and survivors are unchanged")
			eth1Unit := *unitsBefore["eth1"]
			Eventually(func(g Gomega) {
				observed := snapshotEthDevices(vmName)
				g.Expect(observed).To(HaveLen(2))
				for u, dev := range devsBefore {
					if u == eth1Unit {
						g.Expect(observed).ToNot(HaveKey(u),
							"the removed interface's device should be gone (was Key %d)", dev.Key)
						continue
					}
					g.Expect(observed).To(HaveKey(u))
					g.Expect(observed[u].Key).To(Equal(dev.Key))
					g.Expect(observed[u].MAC).To(Equal(dev.MAC))
				}
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())

			By("Asserting the survivors keep their spec unit numbers")
			units := specUnitsByName(vmName)
			Expect(units).To(HaveLen(2))
			Expect(units["eth0"]).To(Equal(unitsBefore["eth0"]))
			Expect(units["eth2"]).To(Equal(unitsBefore["eth2"]))

			By("Powering on and asserting the survivors' status entries retain their unit numbers")
			setPowerState(vmName, vmopv1.VirtualMachinePowerStateOn)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
			waitForStatusUnits(vmName)
			statusUnits := statusUnitsByName(vmName)
			Expect(statusUnits["eth0"]).To(Equal(unitsBefore["eth0"]))
			Expect(statusUnits["eth2"]).To(Equal(unitsBefore["eth2"]))
		})

	It("does not churn settled unit numbers across steady-state reconciles",
		Label("core-functional", "experimental"), func() {
			// Anti-churn guard: assert on (Unit, MAC) pairs, not Key — on at
			// least one real-VC build Key derives from the unit, so a spurious
			// same-slot remove+add would reproduce both Key and Unit and pass
			// a Key-only assertion while the exact churn this guards against
			// is happening (T001 E06).
			vmName := fmt.Sprintf("%s-steady-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOn,
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"},
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1"})

			By("Creating a powered-on VM and settling its unit numbers")
			createNICUnitVM(vm)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
			waitForBackfilledUnits(vmName, 2)
			waitForStatusUnits(vmName)

			By("Capturing the settled device snapshots and asserting no churn")
			before := snapshotEthDevices(vmName)
			Consistently(func() map[int32]nicDevSnapshot {
				return snapshotEthDevices(vmName)
			}, "45s", "5s").Should(Equal(before),
				"settled NICs must not be removed/re-added across reconciles")
		})

	It("keeps unit numbers stable across a power cycle", Label("core-functional", "experimental"), func() {
		vmName := fmt.Sprintf("%s-cycle-%s", specName, capiutil.RandomString(4))
		vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOn,
			vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0"},
			vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1"})

		By("Creating a powered-on VM and settling its unit numbers and status")
		createNICUnitVM(vm)
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
		waitForBackfilledUnits(vmName, 2)
		waitForStatusUnits(vmName)
		specBefore := specUnitsByName(vmName)
		statusBefore := statusUnitsByName(vmName)
		devsBefore := snapshotEthDevices(vmName)

		By("Powering off and back on")
		setPowerState(vmName, vmopv1.VirtualMachinePowerStateOff)
		setPowerState(vmName, vmopv1.VirtualMachinePowerStateOn)
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
		waitForStatusUnits(vmName)

		By("Asserting spec, status, and observed devices are unchanged")
		Expect(specUnitsByName(vmName)).To(Equal(specBefore))
		Expect(statusUnitsByName(vmName)).To(Equal(statusBefore))
		Expect(snapshotEthDevices(vmName)).To(Equal(devsBefore))
	})

	It("preserves unitNumber through a v1alpha2-shaped update via the annotation restore",
		Label("core-functional", "experimental"), func() {
			// The only place the real API-server conversion chain is
			// exercised: a v1alpha2 UPDATE cannot carry spec.network.
			// interfaces[].unitNumber, so the value survives only through the
			// annotation-based restore (k8s#111703 hazard).
			vmName := fmt.Sprintf("%s-conv-%s", specName, capiutil.RandomString(4))
			vm := buildNICUnitVM(vmName, vmopv1.VirtualMachinePowerStateOn,
				vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth0", UnitNumber: ptr.To(int32(9))})

			By("Creating a VM with an explicit unit number")
			createNICUnitVM(vm)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, vmNamespaceName, vmName)
			Expect(specUnitsByName(vmName)["eth0"]).To(Equal(ptr.To(int32(9))))

			By("Submitting an UPDATE through the v1alpha2 manifest shape (omitting unitNumber)")
			// The spoke CANNOT carry unitNumber (hub-only field), so the
			// submitted interfaces deliberately omit it. The interfaces list
			// must be non-empty for the update to exercise
			// restore_v1alpha6_VirtualMachineNetworkInterfaces — the restore
			// helper is name-keyed per interface and does nothing when the
			// converted list is empty — and the power state must match the
			// live VM so the update does not request an unrelated power
			// transition. The interface's network ref is taken from the live
			// object (the mutating webhook defaulted it at create).
			liveVM := getNICUnitVM(vmName)
			Expect(liveVM.Spec.Network).ToNot(BeNil())
			Expect(liveVM.Spec.Network.Interfaces).ToNot(BeEmpty())
			netRef := liveVM.Spec.Network.Interfaces[0].Network
			Expect(netRef).ToNot(BeNil(), "the mutating webhook should have defaulted the interface's network")
			vmYaml := manifestbuilders.GetVirtualMachineYamlA2(manifestbuilders.VirtualMachineYaml{
				Namespace:        vmNamespaceName,
				Name:             vmName,
				ImageName:        linuxVMIName,
				VMClassName:      clusterResources.VMClassName,
				StorageClassName: clusterResources.StorageClassName,
				PowerState:       "PoweredOn",
				NetworkA2: manifestbuilders.NetworkA2{
					// Note InterfaceSpec.Name is the NETWORK's name per the
					// v1a2 template (the interface is hardcoded eth0 there,
					// matching this VM's interface name).
					Interfaces: []manifestbuilders.InterfaceSpec{{
						APIVersion: netRef.APIVersion,
						Kind:       netRef.Kind,
						Name:       netRef.Name,
					}},
				},
			})
			Expect(clusterProxy.ApplyWithArgs(ctx, vmYaml)).To(Succeed(),
				"failed to update virtualmachine via v1alpha2 shape:\n %s", string(vmYaml))

			By("Re-reading the object as v1alpha6 and asserting the value survived")
			Eventually(func(g Gomega) {
				units := specUnitsByName(vmName)
				g.Expect(units["eth0"]).To(Equal(ptr.To(int32(9))))
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())
		})
}
