// Copyright (c) 2025 Broadcom. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	e2eframework "k8s.io/kubernetes/test/e2e/framework"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1a2 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/test/e2e/framework"
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

type VMNetworkSpecInput struct {
	Config           *e2eConfig.E2EConfig
	ClusterProxy     wcpframework.WCPClusterProxyInterface
	WCPClient        wcp.WorkloadManagementAPI
	ArtifactFolder   string
	WCPNamespaceName string
}

func VMNetworkSpec(ctx context.Context, inputGetter func() VMNetworkSpecInput) {
	const (
		specName = "vm-networking"
	)

	var (
		input            VMNetworkSpecInput
		config           *e2eConfig.E2EConfig
		clusterProxy     framework.ClusterProxyInterface
		svClusterClient  ctrlclient.Client
		clusterResources *e2eConfig.Resources
		vmName           string

		linuxVMIName string
	)

	BeforeEach(func() {
		input = inputGetter()
		Expect(input.Config).ToNot(BeNil(), "Invalid argument. input.E2EConfig can't be nil when calling %s spec", specName)
		Expect(input.Config.InfraConfig).ToNot(BeNil(), "Invalid argument. input.E2EConfig.InfraConfig can't be nil when calling %s spec", specName)
		skipper.SkipUnlessInfraIs(input.Config.InfraConfig.InfraName, consts.WCP)

		Expect(input.ClusterProxy).ToNot(BeNil(), "Invalid argument. input.SVClusterProxy can't be nil when calling %s spec", specName)
		Expect(input.WCPNamespaceName).ToNot(BeEmpty(), "Invalid argument. input.WCPNamespaceName can't be empty when calling %s spec", specName)
		Expect(os.MkdirAll(input.ArtifactFolder, 0755)).To(Succeed(), "Invalid argument. input.ArtifactFolder can't be created for %s spec", specName)

		config = input.Config
		clusterResources = config.InfraConfig.ManagementClusterConfig.Resources
		clusterProxy = input.ClusterProxy
		svClusterClient = clusterProxy.GetClient()
		cancelPodWatches := framework.WatchPodLogsAndEventsInNamespaces(ctx, []string{config.GetVariable("VMOPNamespace")}, clusterProxy.GetRESTConfig(), filepath.Join(input.ArtifactFolder, specName))
		DeferCleanup(cancelPodWatches)

		linuxImageDisplayName := vmservice.GetDefaultImageDisplayName(clusterResources)
		vmName = fmt.Sprintf("%s-%s", specName, capiutil.RandomString(4))

		linuxVMIName = vmoperator.WaitForVirtualMachineImageName(ctx, &config.Config, svClusterClient, input.WCPNamespaceName, linuxImageDisplayName)
	})

	It("Should allow network interface to be added to VirtualMachine when mutability cap is enabled", Label("smoke"), func() {
		vmToCreate := &vmopv1a2.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: input.WCPNamespaceName,
				Name:      vmName,
			},
			Spec: vmopv1a2.VirtualMachineSpec{
				ImageName:    linuxVMIName,
				ClassName:    clusterResources.VMClassName,
				StorageClass: clusterResources.StorageClassName,
				PowerState:   vmopv1a2.VirtualMachinePowerStateOff,
			},
		}
		vmoperator.CreateVirtualMachine(ctx, config, clusterProxy, vmToCreate, false)

		vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmoperator.WaitForVirtualMachineMOID(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmMoID := vmoperator.GetVirtualMachineMOID(ctx, svClusterClient, input.WCPNamespaceName, vmName)
		vmMoRef := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmMoID}

		vCenterClient := vcenter.NewVimClientFromKubeconfig(ctx, clusterProxy.GetKubeconfigPath())
		propCollector := property.DefaultCollector(vCenterClient)

		var vmMO mo.VirtualMachine
		Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
		ethCards := object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
		Expect(ethCards).To(HaveLen(1), "VM config should have one EthernetCard")

		By("Add second network interface to VM Spec")

		key := ctrlclient.ObjectKey{Name: vmName, Namespace: input.WCPNamespaceName}
		Eventually(func() bool {
			vm := &vmopv1.VirtualMachine{}

			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			Expect(vm.Spec.Network).ToNot(BeNil())
			Expect(vm.Spec.Network.Interfaces).To(HaveLen(1))
			vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces, vm.Spec.Network.Interfaces[0])

			vm.Spec.Network.Interfaces[1].Name = "eth1"

			err = svClusterClient.Update(ctx, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			return true
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(BeTrue(), "Timed out updating VirtualMachine %s to add second network interface", vmName)

		By("Wait for VM to be reconfigured with second EthernetCard")
		Eventually(func(g Gomega) {
			g.Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
			ethCards := object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
			g.Expect(ethCards).To(HaveLen(2), "VM should have two EthernetCards configured")
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "VM reconfigured with second EthernetCard")

		By("Power on VM")
		vmoperator.PowerOnVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

		By("Powered On VM should still have two EthernetCards configured")
		Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
		ethCards = object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
		Expect(ethCards).To(HaveLen(2), "VM config should have two EthernetCards")
	})

	It("Should allow network interface to be moved to a different subnet when mutability cap is enabled", Label("smoke"), func() {
		if !vmoperator.IsNetworkNsxtVPC(ctx, svClusterClient, config) {
			Skip("Test requires VPC networking environment to create SubnetSet")
		}

		subnetSetName := "custom-subnetset"

		By("Creating custom SubnetSet")
		Eventually(func(g Gomega) {
			subnetSet, err := manifestbuilders.DHCPSubnetOrSubnetSet(manifestbuilders.SubnetOrSubnetSet{
				Kind:      utils.SubnetSetKind,
				Name:      subnetSetName,
				Namespace: input.WCPNamespaceName,
			}, true)
			g.Expect(err).NotTo(HaveOccurred(), "failed to build SubnetSet")
			g.Expect(svClusterClient.Create(ctx, subnetSet)).To(Succeed(), "failed to create SubnetSet %s", subnetSetName)
		}, config.GetIntervals("default", "wait-subnet-creation")...).Should(Succeed(), "Timed out in creating SubnetSet")
		vmservice.VerifySubnetOrSubnetSetCreation(ctx, config, svClusterClient, input.WCPNamespaceName, subnetSetName, utils.SubnetSetKind)

		DeferCleanup(func() {
			vmoperator.DeleteSubnetOrSubnetSet(ctx, config, svClusterClient, input.WCPNamespaceName, subnetSetName, utils.SubnetSetKind)
			vmoperator.WaitForSubnetOrSubnetSetToBeDeleted(ctx, config, svClusterClient, input.WCPNamespaceName, subnetSetName, utils.SubnetSetKind)
		})

		vmToCreate := &vmopv1a2.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: input.WCPNamespaceName,
				Name:      vmName,
			},
			Spec: vmopv1a2.VirtualMachineSpec{
				ImageName:    linuxVMIName,
				ClassName:    clusterResources.VMClassName,
				StorageClass: clusterResources.StorageClassName,
				PowerState:   vmopv1a2.VirtualMachinePowerStateOff,
			},
		}
		vmoperator.CreateVirtualMachine(ctx, config, clusterProxy, vmToCreate, false)

		vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmoperator.WaitForVirtualMachineMOID(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmMoID := vmoperator.GetVirtualMachineMOID(ctx, svClusterClient, input.WCPNamespaceName, vmName)
		vmMoRef := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmMoID}

		vCenterClient := vcenter.NewVimClientFromKubeconfig(ctx, clusterProxy.GetKubeconfigPath())
		propCollector := property.DefaultCollector(vCenterClient)

		var vmMO mo.VirtualMachine
		Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
		ethCards := object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
		Expect(ethCards).To(HaveLen(1), "VM config should have one EthernetCard")

		By("Change network interface to a different subnet")

		key := ctrlclient.ObjectKey{Name: vmName, Namespace: input.WCPNamespaceName}
		Eventually(func() bool {
			vm := &vmopv1.VirtualMachine{}

			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			Expect(vm.Spec.Network).ToNot(BeNil())
			Expect(vm.Spec.Network.Interfaces).To(HaveLen(1))

			// Change the network name to the custom SubnetSet
			vm.Spec.Network.Interfaces[0].Network.Name = subnetSetName
			vm.Spec.Network.Interfaces[0].Network.Kind = utils.SubnetSetKind
			vm.Spec.Network.Interfaces[0].Network.APIVersion = "crd.nsx.vmware.com/v1alpha1"

			err = svClusterClient.Update(ctx, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			return true
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(BeTrue(), "Timed out updating VirtualMachine %s to change subnet", vmName)

		By("Wait for VM to be reconfigured with updated EthernetCard")
		Eventually(func(g Gomega) {
			g.Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
			ethCards := object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
			g.Expect(ethCards).To(HaveLen(1), "VM should still have one EthernetCard configured")
			// We can add validation here to actually check the ethCard.  For now, just validate that
			// the reconfiguring the NIC to connect to a different network succeeds.
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "VM reconfigured with updated EthernetCard")

		By("Power on VM")
		vmoperator.PowerOnVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

		By("Powered On VM should still have one EthernetCard configured")
		Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
		ethCards = object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
		Expect(ethCards).To(HaveLen(1), "VM config should have one EthernetCard")
	})

	Context("External network interfaces", Label("experimental"), func() {
		var (
			adminClient   ctrlclient.Client
			propCollector *property.Collector
		)

		BeforeEach(func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(config.SupervisorCapabilities, consts.BringYourOwnNetworkInterfaceCapabilityName)
			skipper.SkipUnlessSupervisorCapabilityEnabled(config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

			// Only privileged users may set an interface's ExternalRef.
			adminProxy, err := input.ClusterProxy.(*common.VMServiceClusterProxy).NewAdminClusterProxy(ctx)
			Expect(err).ToNot(HaveOccurred(), "failed to create admin cluster proxy")
			DeferCleanup(adminProxy.Dispose)
			adminClient, err = adminProxy.GetAdminClient()
			Expect(err).ToNot(HaveOccurred(), "failed to get admin client")

			vCenterClient := vcenter.NewVimClientFromKubeconfig(ctx, clusterProxy.GetKubeconfigPath())
			DeferCleanup(vcenter.LogoutVimClient, vCenterClient)
			propCollector = property.DefaultCollector(vCenterClient)
		})

		// createExternalNetIf has a privileged user create a network interface
		// CR that is not owned by any VM. Call it before creating the VM that
		// references the CR, so the VM is deleted first during cleanup.
		createExternalNetIf := func(suffix string) (ctrlclient.Object, *vmopv1common.LocalObjectRef) {
			GinkgoHelper()

			netIf, ref := newExternalNetworkInterface(ctx, config, svClusterClient, input.WCPNamespaceName, vmName+"-"+suffix)
			DeferCleanup(func(ctx context.Context) {
				Expect(ctrlclient.IgnoreNotFound(adminClient.Delete(ctx, netIf))).To(Succeed(),
					"failed to delete external network interface %s", netIf.GetName())
			})
			Expect(adminClient.Create(ctx, netIf)).To(Succeed(), "failed to create external network interface %s", netIf.GetName())

			return netIf, ref
		}

		// createVM has a privileged user create a VM with the given interfaces.
		// It does not wait for the VM to be created in vCenter.
		createVM := func(
			interfaces []vmopv1.VirtualMachineNetworkInterfaceSpec,
			powerState vmopv1.VirtualMachinePowerState) {

			GinkgoHelper()

			vm := &vmopv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: input.WCPNamespaceName,
					Name:      vmName,
				},
				Spec: vmopv1.VirtualMachineSpec{
					ImageName:    linuxVMIName,
					ClassName:    clusterResources.VMClassName,
					StorageClass: clusterResources.StorageClassName,
					PowerState:   powerState,
					Network: &vmopv1.VirtualMachineNetworkSpec{
						Interfaces: interfaces,
					},
				},
			}
			Expect(adminClient.Create(ctx, vm)).To(Succeed(), "failed to create VirtualMachine %s", vmName)
			vmoperator.DeferCleanupVirtualMachine(config, svClusterClient, clusterProxy.GetKubeconfigPath(),
				input.WCPNamespaceName, vmName, false)
			vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		}

		// createVMWithExternalInterface creates a VM with one managed interface,
		// then has a privileged user add a second, external interface.
		createVMWithExternalInterface := func() (types.ManagedObjectReference, ctrlclient.Object) {
			GinkgoHelper()

			netIf, ref := createExternalNetIf("external")

			createVM(
				[]vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name: "eth0",
					},
				},
				vmopv1.VirtualMachinePowerStateOff)

			moRef := vmoperator.WaitForVirtualMachineMoRef(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
			expectEthCards(ctx, config, propCollector, moRef, false)

			By("Add external network interface to VM Spec as a privileged user")
			Eventually(func() error {
				return vmoperator.PatchVirtualMachine(ctx, adminClient, input.WCPNamespaceName, vmName,
					func(vm *vmopv1.VirtualMachine) error {
						if vm.Spec.Network == nil {
							return fmt.Errorf("VM should have a network spec")
						}
						vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces,
							vmopv1.VirtualMachineNetworkInterfaceSpec{
								Name:        "eth1",
								ExternalRef: ref,
							},
						)
						return nil
					},
					ctrlclient.MergeFromWithOptimisticLock{})
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(),
				"failed to add external network interface to VirtualMachine %s", vmName)

			By("Second EthernetCard should be backed by the external network interface, not a new managed one")
			expectEthCardsBackedByExternal(ctx, config, svClusterClient, propCollector, moRef,
				input.WCPNamespaceName, vmName, 2, netIf)
			expectExternalInterfaceAssociated(ctx, config, svClusterClient, netIf, vmName, "eth1")

			return moRef, netIf
		}

		It("Should create a VirtualMachine with mixed interfaces once its external network interfaces exist", func() {
			// The first external interface does not exist when the VM is created.
			missingNetIf, missingRef := newExternalNetworkInterface(ctx, config, svClusterClient, input.WCPNamespaceName, vmName+"-external1")
			DeferCleanup(func(ctx context.Context) {
				Expect(ctrlclient.IgnoreNotFound(adminClient.Delete(ctx, missingNetIf))).To(Succeed(),
					"failed to delete external network interface %s", missingNetIf.GetName())
			})
			readyNetIf, readyRef := createExternalNetIf("external2")

			createVM(
				[]vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name: "eth0",
					},
					{
						Name:        "eth1",
						ExternalRef: missingRef,
					},
					{
						Name:        "eth2",
						ExternalRef: readyRef,
					},
				},
				vmopv1.VirtualMachinePowerStateOff)

			By("VM should report its network is not ready while an external network interface is missing")
			vmoperator.WaitOnVirtualMachineCondition(ctx, config, svClusterClient, input.WCPNamespaceName, vmName,
				metav1.Condition{
					Type:   vmopv1.VirtualMachineConditionNetworkReady,
					Status: metav1.ConditionFalse,
					Reason: "NotReady",
				})
			vm, err := utils.GetVirtualMachine(ctx, svClusterClient, input.WCPNamespaceName, vmName)
			Expect(err).ToNot(HaveOccurred(), "failed to get VirtualMachine %s", vmName)
			Expect(vm.Status.UniqueID).To(BeEmpty(), "VM should not be created without its external network interface")

			By("Create the missing external network interface without editing the VM")
			Expect(adminClient.Create(ctx, missingNetIf)).To(Succeed(), "failed to create external network interface %s", missingNetIf.GetName())

			By("VM should recover and be created with three EthernetCards")
			moRef := vmoperator.WaitForVirtualMachineMoRef(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
			vmoperator.WaitOnVirtualMachineCondition(ctx, config, svClusterClient, input.WCPNamespaceName, vmName,
				metav1.Condition{
					Type:   vmopv1.VirtualMachineConditionNetworkReady,
					Status: metav1.ConditionTrue,
				})

			By("EthernetCards should be backed by the external network interfaces, with only eth0 managed")
			expectEthCardsBackedByExternal(ctx, config, svClusterClient, propCollector, moRef,
				input.WCPNamespaceName, vmName, 3, missingNetIf, readyNetIf)
			expectExternalInterfaceAssociated(ctx, config, svClusterClient, missingNetIf, vmName, "eth1")
			expectExternalInterfaceAssociated(ctx, config, svClusterClient, readyNetIf, vmName, "eth2")

			By("Delete VM")
			vmoperator.DeleteVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

			By("External network interfaces should still exist, unchanged")
			expectExternalInterfaceReleased(ctx, config, svClusterClient, missingNetIf)
			expectExternalInterfaceReleased(ctx, config, svClusterClient, readyNetIf)
		})

		It("Should power on and power cycle a VirtualMachine with an external network interface", func() {
			netIf, ref := createExternalNetIf("external")

			createVM(
				[]vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:        "eth0",
						ExternalRef: ref,
					},
				},
				vmopv1.VirtualMachinePowerStateOn)
			moRef := vmoperator.WaitForVirtualMachineMoRef(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

			By("VM should power on with a connected EthernetCard backed by the external network interface")
			vmoperator.WaitForVirtualMachinePowerState(ctx, config, svClusterClient, input.WCPNamespaceName, vmName, vmopv1.VirtualMachinePowerStateOn)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
			expectEthCards(ctx, config, propCollector, moRef, true)
			expectEthCardsBackedByExternal(ctx, config, svClusterClient, propCollector, moRef,
				input.WCPNamespaceName, vmName, 1, netIf)
			expectExternalInterfaceAssociated(ctx, config, svClusterClient, netIf, vmName, "eth0")

			By("Power off VM")
			vmoperator.PowerOffVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

			By("External network interface should remain associated while powered off")
			expectEthCards(ctx, config, propCollector, moRef, false)
			expectExternalInterfaceAssociated(ctx, config, svClusterClient, netIf, vmName, "eth0")

			By("Power on VM again")
			vmoperator.PowerOnVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
			vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
			expectEthCards(ctx, config, propCollector, moRef, true)
			expectExternalInterfaceAssociated(ctx, config, svClusterClient, netIf, vmName, "eth0")
		})

		It("Should allow an external network interface to be added to and removed from a VirtualMachine", func() {
			moRef, netIf := createVMWithExternalInterface()

			By("Remove external network interface from VM Spec")
			Eventually(func() error {
				return vmoperator.PatchVirtualMachine(ctx, svClusterClient, input.WCPNamespaceName, vmName,
					func(vm *vmopv1.VirtualMachine) error {
						if vm.Spec.Network == nil || len(vm.Spec.Network.Interfaces) != 2 {
							return fmt.Errorf("VM should have two network interfaces")
						}
						vm.Spec.Network.Interfaces = vm.Spec.Network.Interfaces[:1]
						return nil
					},
					ctrlclient.MergeFromWithOptimisticLock{})
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(),
				"failed to remove external network interface from VirtualMachine %s", vmName)

			By("VM should be reconfigured with one EthernetCard")
			expectEthCards(ctx, config, propCollector, moRef, false)

			By("External network interface should still exist and no longer be associated with the VM")
			expectExternalInterfaceReleased(ctx, config, svClusterClient, netIf)

			By("Delete VM")
			vmoperator.DeleteVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

			By("External network interface should still exist after the VM is deleted")
			expectExternalInterfaceReleased(ctx, config, svClusterClient, netIf)
		})

		It("Should release an external network interface when the VirtualMachine is deleted", func() {
			_, netIf := createVMWithExternalInterface()

			By("Delete VM while it still references the external network interface")
			vmoperator.DeleteVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

			By("External network interface should still exist and no longer be associated with the VM")
			expectExternalInterfaceReleased(ctx, config, svClusterClient, netIf)
		})

		It("Should not allow a non-privileged user to set an external network interface", func() {
			vm := &vmopv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: input.WCPNamespaceName,
					Name:      vmName,
				},
				Spec: vmopv1.VirtualMachineSpec{
					ImageName:    linuxVMIName,
					ClassName:    clusterResources.VMClassName,
					StorageClass: clusterResources.StorageClassName,
					PowerState:   vmopv1.VirtualMachinePowerStateOff,
					Network: &vmopv1.VirtualMachineNetworkSpec{
						Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
							{Name: "eth0"},
						},
					},
				},
			}
			vmoperator.CreateVirtualMachine(ctx, config, clusterProxy, vm, false)
			vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

			// The webhook rejects the reference before the CR needs to exist.
			_, externalRef := newExternalNetworkInterface(ctx, config, svClusterClient, input.WCPNamespaceName, vmName+"-external")

			Eventually(func() error {
				return vmoperator.PatchVirtualMachine(ctx, svClusterClient, input.WCPNamespaceName, vmName,
					func(vm *vmopv1.VirtualMachine) error {
						if vm.Spec.Network == nil {
							return fmt.Errorf("VM should have a network spec")
						}
						vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces,
							vmopv1.VirtualMachineNetworkInterfaceSpec{Name: "eth1", ExternalRef: externalRef})
						return nil
					},
					ctrlclient.MergeFromWithOptimisticLock{})
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(
				MatchError(ContainSubstring("only privileged users may set an external network interface")),
				"non-privileged user should not be able to set an external network interface")
		})
	})

	// Spec 017 T018: status, boot options, and device reconciliation share one
	// mapping between network interfaces and Ethernet cards. Two interfaces on
	// the same network cannot be told apart by their backing, so this checks
	// that each guest-reported status entry names a distinct interface and
	// points at a distinct card whose MAC address it reports.
	It("Should name each network interface status after the Ethernet card it is matched to", Label("experimental"), func() {
		skipper.SkipUnlessSupervisorCapabilityEnabled(config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

		vmToCreate := &vmopv1a2.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: input.WCPNamespaceName,
				Name:      vmName,
			},
			Spec: vmopv1a2.VirtualMachineSpec{
				ImageName:    linuxVMIName,
				ClassName:    clusterResources.VMClassName,
				StorageClass: clusterResources.StorageClassName,
				PowerState:   vmopv1a2.VirtualMachinePowerStateOff,
			},
		}
		vmoperator.CreateVirtualMachine(ctx, config, clusterProxy, vmToCreate, false)

		vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmoperator.WaitForVirtualMachineMOID(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmMoID := vmoperator.GetVirtualMachineMOID(ctx, svClusterClient, input.WCPNamespaceName, vmName)
		vmMoRef := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmMoID}

		vCenterClient := vcenter.NewVimClientFromKubeconfig(ctx, clusterProxy.GetKubeconfigPath())
		propCollector := property.DefaultCollector(vCenterClient)

		By("Add a second network interface on the same network")

		key := ctrlclient.ObjectKey{Name: vmName, Namespace: input.WCPNamespaceName}
		Eventually(func(g Gomega) {
			vm := &vmopv1.VirtualMachine{}
			g.Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
			g.Expect(vm.Spec.Network).ToNot(BeNil())
			g.Expect(vm.Spec.Network.Interfaces).To(HaveLen(1))

			eth1 := *vm.Spec.Network.Interfaces[0].DeepCopy()
			eth1.Name = "eth1"
			vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces, eth1)
			g.Expect(svClusterClient.Update(ctx, vm)).To(Succeed())
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "Timed out updating VirtualMachine %s to add second network interface", vmName)

		By("Wait for VM to be reconfigured with second EthernetCard")
		var vmMO mo.VirtualMachine
		Eventually(func(g Gomega) {
			g.Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
			ethCards := object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
			g.Expect(ethCards).To(HaveLen(2), "VM should have two EthernetCards configured")
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "VM reconfigured with second EthernetCard")

		By("Power on VM")
		vmoperator.PowerOnVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

		By("Each status interface names a distinct interface and the card whose MAC address it reports")
		Eventually(func(g Gomega) {
			g.Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
			macByKey := map[int32]string{}
			for _, dev := range object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil)) {
				ethCard := dev.(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
				macByKey[ethCard.Key] = ethCard.MacAddress
			}
			g.Expect(macByKey).To(HaveLen(2))

			vm := &vmopv1.VirtualMachine{}
			g.Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
			g.Expect(vm.Status.Network).ToNot(BeNil())

			keyByName := map[string]int32{}
			for _, iface := range vm.Status.Network.Interfaces {
				if iface.DeviceKey < 0 {
					continue
				}
				mac, ok := macByKey[iface.DeviceKey]
				g.Expect(ok).To(BeTrue(), "status interface %q has device key %d, which is not an EthernetCard", iface.Name, iface.DeviceKey)
				g.Expect(iface.IP).ToNot(BeNil())
				g.Expect(strings.EqualFold(iface.IP.MACAddr, mac)).To(BeTrue(),
					"status interface %q reports MAC %q but its device %d has MAC %q", iface.Name, iface.IP.MACAddr, iface.DeviceKey, mac)
				g.Expect(keyByName).ToNot(HaveKey(iface.Name), "status interface name %q is used twice", iface.Name)
				keyByName[iface.Name] = iface.DeviceKey
			}
			g.Expect(keyByName).To(HaveLen(2))
			g.Expect(keyByName).To(HaveKey(vm.Spec.Network.Interfaces[0].Name))
			g.Expect(keyByName).To(HaveKey("eth1"))
			g.Expect(keyByName[vm.Spec.Network.Interfaces[0].Name]).ToNot(Equal(keyByName["eth1"]))
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "VM status interfaces named after their EthernetCards")
	})

	// Spec 017 T020 (G2): an interface whose provider resource cannot become
	// ready is data, not a reconcile failure. A running VM stays running and
	// can still be powered off, VirtualMachineNetworkReady names the
	// interface, and only the transition from powered off to powered on is
	// blocked until the interface is ready or removed.
	It("Should power off but not power on a VM with a pending network interface", Label("experimental"), func() {
		skipper.SkipUnlessSupervisorCapabilityEnabled(config.SupervisorCapabilities, consts.MutableNetworksCapabilityName)

		const pendingInterfaceName = "eth-pending"
		missingNetworkName := fmt.Sprintf("%s-missing-%s", specName, capiutil.RandomString(4))

		vmToCreate := &vmopv1a2.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: input.WCPNamespaceName,
				Name:      vmName,
			},
			Spec: vmopv1a2.VirtualMachineSpec{
				ImageName:    linuxVMIName,
				ClassName:    clusterResources.VMClassName,
				StorageClass: clusterResources.StorageClassName,
				PowerState:   vmopv1a2.VirtualMachinePowerStateOn,
			},
		}
		vmoperator.CreateVirtualMachine(ctx, config, clusterProxy, vmToCreate, false)
		vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmoperator.WaitForVirtualMachinePowerState(ctx, config, svClusterClient, input.WCPNamespaceName, vmName, vmopv1.VirtualMachinePowerStateOn)

		key := ctrlclient.ObjectKey{Name: vmName, Namespace: input.WCPNamespaceName}

		By("Add a network interface whose network does not exist")
		Eventually(func(g Gomega) {
			g.Expect(vmoperator.PatchVirtualMachine(ctx, svClusterClient, input.WCPNamespaceName, vmName, func(vm *vmopv1.VirtualMachine) error {
				if vm.Spec.Network == nil || len(vm.Spec.Network.Interfaces) != 1 || vm.Spec.Network.Interfaces[0].Network == nil {
					return fmt.Errorf("expected one network interface with a network, got %+v", vm.Spec.Network)
				}
				pending := *vm.Spec.Network.Interfaces[0].DeepCopy()
				pending.Name = pendingInterfaceName
				// The same provider type as the first interface, so its
				// provider resource is created, but it never becomes ready.
				pending.Network.Name = missingNetworkName
				pending.MACAddr = ""
				pending.UnitNumber = nil
				vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces, pending)
				return nil
			}, ctrlclient.MergeFromWithOptimisticLock{})).To(Succeed())
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "Timed out adding the pending network interface to VirtualMachine %s", vmName)

		By("The VM stays running and reports the pending interface")
		Eventually(func(g Gomega) {
			vm := &vmopv1.VirtualMachine{}
			g.Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
			g.Expect(vm.Status.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOn))
			c := meta.FindStatusCondition(vm.Status.Conditions, vmopv1.VirtualMachineConditionNetworkReady)
			g.Expect(c).ToNot(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Message).To(ContainSubstring(pendingInterfaceName))
		}, config.GetIntervals("default", "wait-virtual-machine-condition-update")...).Should(Succeed(), "VirtualMachine %s does not report its pending network interface", vmName)

		By("Power off the VM while the interface is pending")
		vmoperator.PowerOffVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

		By("Power on stays blocked while the interface is pending")
		vmoperator.UpdateVirtualMachinePowerState(ctx, config, svClusterClient, input.WCPNamespaceName, vmName, vmopv1.VirtualMachinePowerStateOn)
		Consistently(func(g Gomega) {
			vm := &vmopv1.VirtualMachine{}
			g.Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
			g.Expect(vm.Status.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
			c := meta.FindStatusCondition(vm.Status.Conditions, vmopv1.VirtualMachineConditionNetworkReady)
			g.Expect(c).ToNot(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(c.Message).To(ContainSubstring(pendingInterfaceName))
		}, config.GetIntervals("default", "consistent-virtual-machine-condition")...).Should(Succeed(), "VirtualMachine %s powered on with a pending network interface", vmName)

		By("Remove the pending interface")
		Eventually(func(g Gomega) {
			g.Expect(vmoperator.PatchVirtualMachine(ctx, svClusterClient, input.WCPNamespaceName, vmName, func(vm *vmopv1.VirtualMachine) error {
				if vm.Spec.Network == nil || len(vm.Spec.Network.Interfaces) != 2 {
					return fmt.Errorf("expected two network interfaces, got %+v", vm.Spec.Network)
				}
				vm.Spec.Network.Interfaces = vm.Spec.Network.Interfaces[:1]
				return nil
			}, ctrlclient.MergeFromWithOptimisticLock{})).To(Succeed())
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "Timed out removing the pending network interface from VirtualMachine %s", vmName)

		By("The VM powers on once its network is ready")
		vmoperator.WaitForVirtualMachinePowerState(ctx, config, svClusterClient, input.WCPNamespaceName, vmName, vmopv1.VirtualMachinePowerStateOn)
		Eventually(func(g Gomega) {
			vm := &vmopv1.VirtualMachine{}
			g.Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
			c := meta.FindStatusCondition(vm.Status.Conditions, vmopv1.VirtualMachineConditionNetworkReady)
			g.Expect(c).ToNot(BeNil())
			g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
		}, config.GetIntervals("default", "wait-virtual-machine-condition-update")...).Should(Succeed(), "VirtualMachine %s network is not ready", vmName)
	})
}
