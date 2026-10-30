// Copyright (c) 2025 Broadcom. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	vmopv1a2 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/constants"
	"github.com/vmware-tanzu/vm-operator/test/e2e/framework"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/vcenter"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/wcp"
	"github.com/vmware-tanzu/vm-operator/test/e2e/manifestbuilders"
	"github.com/vmware-tanzu/vm-operator/test/e2e/utils"
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
		Eventually(func() bool {
			vm := &vmopv1.VirtualMachine{}

			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn

			err = svClusterClient.Update(ctx, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			return true
		}, config.GetIntervals("default", "wait-virtual-machine-powerstate")...).Should(BeTrue(), "Timed out updating VirtualMachine %s PowerState to On", vmName)
		vmoperator.WaitForVirtualMachinePowerState(ctx, config, svClusterClient, input.WCPNamespaceName, vmName, "PoweredOn")
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
		Eventually(func() bool {
			vm := &vmopv1.VirtualMachine{}
			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn

			err = svClusterClient.Update(ctx, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			return true
		}, config.GetIntervals("default", "wait-virtual-machine-powerstate")...).Should(BeTrue(), "Timed out updating VirtualMachine %s PowerState to On", vmName)

		vmoperator.WaitForVirtualMachinePowerState(ctx, config, svClusterClient, input.WCPNamespaceName, vmName, "PoweredOn")
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

		By("Powered On VM should still have one EthernetCard configured")
		Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
		ethCards = object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
		Expect(ethCards).To(HaveLen(1), "VM config should have one EthernetCard")
	})

	Context("External network interfaces", Label("extended-functional", "byo-network-interface", "experimental"), func() {
		BeforeEach(func() {
			skipper.SkipUnlessSupervisorCapabilityEnabled(ctx, clusterProxy, consts.BringYourOwnNetworkInterfaceCapabilityName)
			skipper.SkipUnlessSupervisorCapabilityEnabled(ctx, clusterProxy, consts.MutableNetworksCapabilityName)
		})

		It("Should allow an external network interface to be added to and removed from a VirtualMachine", func() {
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

			By("Create external network interface from the VM's managed network interface")

			key := ctrlclient.ObjectKey{Name: vmName, Namespace: input.WCPNamespaceName}
			vm := &vmopv1.VirtualMachine{}
			Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())

			externalNetIf, externalRef := newExternalNetworkInterface(ctx, config, svClusterClient, vm, vmName+"-external")
			Expect(svClusterClient.Create(ctx, externalNetIf)).To(Succeed())
			externalNetIfKey := ctrlclient.ObjectKeyFromObject(externalNetIf)
			DeferCleanup(func() {
				Expect(ctrlclient.IgnoreNotFound(svClusterClient.Delete(ctx, externalNetIf))).To(Succeed())
			})

			By("Add external network interface to VM Spec")
			Eventually(func() bool {
				vm := &vmopv1.VirtualMachine{}

				err := svClusterClient.Get(ctx, key, vm)
				if err != nil {
					e2eframework.Logf("retry due to: %v", err)
					return false
				}

				Expect(vm.Spec.Network).ToNot(BeNil())
				Expect(vm.Spec.Network.Interfaces).To(HaveLen(1))
				vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces,
					vmopv1.VirtualMachineNetworkInterfaceSpec{
						Name:        "eth1",
						ExternalRef: externalRef,
					})

				err = svClusterClient.Update(ctx, vm)
				if err != nil {
					e2eframework.Logf("retry due to: %v", err)
					return false
				}

				return true
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(BeTrue(), "Timed out updating VirtualMachine %s to add external network interface", vmName)

			By("Wait for VM to be reconfigured with second EthernetCard")
			Eventually(func(g Gomega) {
				g.Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
				ethCards := object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
				g.Expect(ethCards).To(HaveLen(2), "VM should have two EthernetCards configured")
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "VM reconfigured with second EthernetCard")

			By("External network interface should not be owned by the VM")
			Expect(svClusterClient.Get(ctx, externalNetIfKey, externalNetIf)).To(Succeed())
			Expect(externalNetIf.GetOwnerReferences()).To(BeEmpty())
			if _, ok := externalNetIf.(*vpcv1alpha1.SubnetPort); ok {
				Expect(externalNetIf.GetLabels()).To(HaveKeyWithValue(vmNameLabel, vmName))
				Expect(externalNetIf.GetAnnotations()).To(HaveKeyWithValue(pkgconst.VPCAttachmentRef, "virtualmachine/"+vmName+"/eth1"))
			}

			By("Remove external network interface from VM Spec")
			Eventually(func() bool {
				vm := &vmopv1.VirtualMachine{}

				err := svClusterClient.Get(ctx, key, vm)
				if err != nil {
					e2eframework.Logf("retry due to: %v", err)
					return false
				}

				Expect(vm.Spec.Network).ToNot(BeNil())
				Expect(vm.Spec.Network.Interfaces).To(HaveLen(2))
				vm.Spec.Network.Interfaces = vm.Spec.Network.Interfaces[:1]

				err = svClusterClient.Update(ctx, vm)
				if err != nil {
					e2eframework.Logf("retry due to: %v", err)
					return false
				}

				return true
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(BeTrue(), "Timed out updating VirtualMachine %s to remove external network interface", vmName)

			By("Wait for VM to be reconfigured with one EthernetCard")
			Eventually(func(g Gomega) {
				g.Expect(propCollector.RetrieveOne(ctx, vmMoRef, []string{"config"}, &vmMO)).To(Succeed())
				ethCards := object.VirtualDeviceList(vmMO.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil))
				g.Expect(ethCards).To(HaveLen(1), "VM should have one EthernetCard configured")
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "VM reconfigured with one EthernetCard")

			By("External network interface should still exist and no longer be associated with the VM")
			Eventually(func(g Gomega) {
				g.Expect(svClusterClient.Get(ctx, externalNetIfKey, externalNetIf)).To(Succeed())
				g.Expect(externalNetIf.GetOwnerReferences()).To(BeEmpty())
				g.Expect(externalNetIf.GetLabels()).ToNot(HaveKey(vmNameLabel))
				g.Expect(externalNetIf.GetAnnotations()).ToNot(HaveKey(pkgconst.VPCAttachmentRef))
			}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(), "External network interface released from VM")

			By("Delete VM")
			vmoperator.DeleteVirtualMachineAndWait(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

			By("External network interface should still exist after the VM is deleted")
			Expect(svClusterClient.Get(ctx, externalNetIfKey, externalNetIf)).To(Succeed())
		})
	})
}

// vmNameLabel is the label VM Operator puts on a network interface CR to
// identify its VM by name.
const vmNameLabel = "vmoperator.vmware.com/vm-name"

// newExternalNetworkInterface returns a network interface CR, not owned by the
// VM, with the same spec as the VM's managed network interface CR. It also
// returns the reference to use in the VM's interface ExternalRef.
func newExternalNetworkInterface(
	ctx context.Context,
	config *e2eConfig.E2EConfig,
	client ctrlclient.Client,
	vm *vmopv1.VirtualMachine,
	name string) (ctrlclient.Object, *vmopv1common.LocalObjectRef) {

	GinkgoHelper()

	var list ctrlclient.ObjectList
	switch {
	case vmoperator.IsNetworkNsxtVPC(ctx, client, config):
		list = &vpcv1alpha1.SubnetPortList{}
	case framework.NetworkTopologyIs(config.InfraConfig.NetworkingTopology, consts.NSX):
		list = &ncpv1alpha1.VirtualNetworkInterfaceList{}
	default:
		list = &netopv1alpha1.NetworkInterfaceList{}
	}

	var managed ctrlclient.Object
	Eventually(func(g Gomega) {
		g.Expect(client.List(ctx, list, ctrlclient.InNamespace(vm.Namespace))).To(Succeed())
		objs, err := meta.ExtractList(list)
		g.Expect(err).ToNot(HaveOccurred())
		for _, obj := range objs {
			if obj := obj.(ctrlclient.Object); metav1.IsControlledBy(obj, vm) {
				managed = obj
			}
		}
		g.Expect(managed).ToNot(BeNil(), "VM should have a managed network interface")
	}, config.GetIntervals("default", "wait-virtual-machine-creation")...).Should(Succeed())

	objectMeta := metav1.ObjectMeta{
		Namespace: vm.Namespace,
		Name:      name,
	}

	switch managed := managed.(type) {
	case *netopv1alpha1.NetworkInterface:
		return &netopv1alpha1.NetworkInterface{
			ObjectMeta: objectMeta,
			Spec:       managed.Spec,
		}, &vmopv1common.LocalObjectRef{
			APIVersion: netopv1alpha1.SchemeGroupVersion.String(),
			Kind:       "NetworkInterface",
			Name:       name,
		}
	case *ncpv1alpha1.VirtualNetworkInterface:
		return &ncpv1alpha1.VirtualNetworkInterface{
			ObjectMeta: objectMeta,
			Spec:       managed.Spec,
		}, &vmopv1common.LocalObjectRef{
			APIVersion: ncpv1alpha1.SchemeGroupVersion.String(),
			Kind:       "VirtualNetworkInterface",
			Name:       name,
		}
	case *vpcv1alpha1.SubnetPort:
		return &vpcv1alpha1.SubnetPort{
			ObjectMeta: objectMeta,
			Spec:       managed.Spec,
		}, &vmopv1common.LocalObjectRef{
			APIVersion: vpcv1alpha1.SchemeGroupVersion.String(),
			Kind:       "SubnetPort",
			Name:       name,
		}
	}

	Fail(fmt.Sprintf("unsupported network interface type %T", managed))
	return nil, nil
}
