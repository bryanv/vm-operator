// Copyright (c) 2025 Broadcom. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	e2eframework "k8s.io/kubernetes/test/e2e/framework"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1a3 "github.com/vmware-tanzu/vm-operator/api/v1alpha3"
	vmopv1a6 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/e2e/appple2e/util"
	"github.com/vmware-tanzu/vm-operator/test/e2e/framework"
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

type VMNetworkSpecInput struct {
	Config           *e2eConfig.E2EConfig
	ClusterProxy     wcpframework.WCPClusterProxyInterface
	WCPClient        wcp.WorkloadManagementAPI
	ArtifactFolder   string
	WCPNamespaceName string
}

func VMNetworkSpec(ctx context.Context, inputGetter func() VMNetworkSpecInput) {
	const (
		specName           = "vm-networking"
		mutableNetworksCap = "supports_VM_service_mutable_networks"
	)

	var (
		input            VMNetworkSpecInput
		wcpClient        wcp.WorkloadManagementAPI
		config           *e2eConfig.E2EConfig
		clusterProxy     *common.VMServiceClusterProxy
		svClusterClient  ctrlclient.Client
		clusterResources *e2eConfig.Resources
		tmpNamespaceCtx  wcpframework.NamespaceContext
		vmYaml           []byte
		vmName           string

		isVMMutableNetworksCapEnabled bool
		isVMRoutingPoliciesCapEnabled bool
		linuxImageDisplayName         string
		linuxVMIName                  string
	)

	BeforeEach(func() {
		input = inputGetter()
		Expect(input.Config).ToNot(BeNil(), "Invalid argument. input.E2EConfig can't be nil when calling %s spec", specName)
		Expect(input.Config.InfraConfig).ToNot(BeNil(), "Invalid argument. input.E2EConfig.InfraConfig can't be nil when calling %s spec", specName)
		skipper.SkipUnlessInfraIs(input.Config.InfraConfig.InfraName, consts.WCP)

		Expect(input.ClusterProxy).ToNot(BeNil(), "Invalid argument. input.SVClusterProxy can't be nil when calling %s spec", specName)
		Expect(input.WCPNamespaceName).ToNot(BeEmpty(), "Invalid argument. input.WCPNamespaceName can't be empty when calling %s spec", specName)
		Expect(os.MkdirAll(input.ArtifactFolder, 0755)).To(Succeed(), "Invalid argument. input.ArtifactFolder can't be created for %s spec", specName)

		svClusterProxy := input.ClusterProxy
		wcpClient = input.WCPClient
		config = input.Config
		clusterResources = config.InfraConfig.ManagementClusterConfig.Resources
		clusterProxy = input.ClusterProxy.(*common.VMServiceClusterProxy)
		svClusterClient = clusterProxy.GetClient()
		cancelPodWatches := framework.WatchPodLogsAndEventsInNamespaces(ctx, []string{config.GetVariable("VMOPNamespace")}, clusterProxy.GetClientSet(), filepath.Join(input.ArtifactFolder, specName))
		DeferCleanup(cancelPodWatches)

		linuxImageDisplayName = vmservice.GetDefaultImageDisplayName(clusterResources)
		vmYaml = nil
		tmpNamespaceCtx = wcpframework.NamespaceContext{}
		vmName = fmt.Sprintf("%s-%s", specName, capiutil.RandomString(4))

		var err error
		linuxVMIName, err = vmoperator.WaitForVirtualMachineImageName(ctx, &config.Config, svClusterClient, input.WCPNamespaceName, linuxImageDisplayName)
		Expect(err).NotTo(HaveOccurred(), "failed to get VMI name for display name %q in namespace %q", linuxImageDisplayName, input.WCPNamespaceName)

		sshCommandRunner, _ := e2essh.NewSSHCommandRunner(
			vcenter.GetVCPNIDFromKubeconfigFile(ctx, svClusterProxy.GetKubeconfigPath()),
			vcenter.VCSSHPort, testbed.RootUsername, []ssh.AuthMethod{ssh.Password(testbed.RootPassword)})
		isAsyncSvUpgradeEnabled, _ := util.IsFSSEnabled(sshCommandRunner, utils.SupervisorAsyncUpgradeFSS)
		isVMMutableNetworksCapEnabled = utils.IsSupervisorCapabilityEnabled(ctx,
			svClusterProxy.GetClientSet(), svClusterProxy.GetDynamicClient(), mutableNetworksCap, isAsyncSvUpgradeEnabled)
		isVMRoutingPoliciesCapEnabled = utils.IsSupervisorCapabilityEnabled(ctx,
			svClusterProxy.GetClientSet(), svClusterProxy.GetDynamicClient(), consts.VMRoutingPoliciesCapabilityName, isAsyncSvUpgradeEnabled)
	})

	AfterEach(func() {
		vmNamespaceName := input.WCPNamespaceName
		if tmpNamespaceCtx.GetNamespace() != nil {
			vmNamespaceName = tmpNamespaceCtx.GetNamespace().Name
		}

		if CurrentGinkgoTestDescription().Failed {
			vmoperator.DescribeResourceIfExists(ctx, svClusterClient, clusterProxy.GetKubeconfigPath(), vmNamespaceName, vmName, "vm")
		}

		// Delete the virtual machine if it was created.
		if len(vmYaml) > 0 {
			Expect(clusterProxy.DeleteWithArgs(ctx, vmYaml)).To(Succeed(), "failed to delete virtualmachine")
			// Verify that virtual machine does not exist.
			vmoperator.WaitForVirtualMachineToBeDeleted(ctx, config, svClusterClient, vmNamespaceName, vmName)
		}

		// Delete the temporary namespace if it was created.
		if tmpNamespaceCtx.GetNamespace() != nil {
			clusterProxy.DeleteWCPNamespace(tmpNamespaceCtx)
			wcp.WaitForNamespaceDeleted(wcpClient, tmpNamespaceCtx.GetNamespace().Name)
		}
	})

	It("Should allow network interface to be added to VirtualMachine when mutability cap is enabled", Label("smoke"), func() {
		if !isVMMutableNetworksCapEnabled {
			Skip("VM Mutable Networks capability is not enabled")
		}

		vmParameters := manifestbuilders.VirtualMachineYaml{
			Namespace:        input.WCPNamespaceName,
			Name:             vmName,
			ImageName:        linuxVMIName,
			VMClassName:      clusterResources.VMClassName,
			StorageClassName: clusterResources.StorageClassName,
			PowerState:       "PoweredOff",
		}
		vmYaml = manifestbuilders.GetVirtualMachineYamlA2(vmParameters)
		Expect(clusterProxy.CreateWithArgs(ctx, vmYaml)).To(Succeed(), "failed to create virtualmachine:\n %s", string(vmYaml))

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
			vm := &vmopv1a3.VirtualMachine{}

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
			vm := &vmopv1a3.VirtualMachine{}

			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			vm.Spec.PowerState = vmopv1a3.VirtualMachinePowerStateOn

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
		if !isVMMutableNetworksCapEnabled {
			Skip("VM Mutable Networks capability is not enabled")
		}

		if !vmoperator.IsNetworkNsxtVPC(ctx, svClusterClient, config) {
			Skip("Test requires VPC networking environment to create SubnetSet")
		}

		subnetSetName := "custom-subnetset"

		By("Creating custom SubnetSet")
		Eventually(func(g Gomega) {
			sYaml := utils.CreateSubnetOrSubnetSetYaml(utils.SubnetSetKind, subnetSetName, input.WCPNamespaceName, utils.DHCPConfig, true)
			g.Expect(clusterProxy.CreateWithArgs(ctx, sYaml)).To(Succeed(), "failed to create the SubnetSet: %s", string(sYaml))
		}, config.GetIntervals("default", "wait-subnet-creation")...).Should(Succeed(), "Timed out in creating SubnetSet")
		vmservice.VerifySubnetOrSubnetSetCreation(ctx, config, svClusterClient, input.WCPNamespaceName, subnetSetName, utils.SubnetSetKind)

		DeferCleanup(func() {
			vmoperator.DeleteSubnetOrSubnetSet(ctx, svClusterClient, input.WCPNamespaceName, subnetSetName, utils.SubnetSetKind)
			vmoperator.WaitForSubnetOrSubnetSetToBeDeleted(ctx, config, svClusterClient, input.WCPNamespaceName, subnetSetName, utils.SubnetSetKind)
		})

		vmParameters := manifestbuilders.VirtualMachineYaml{
			Namespace:        input.WCPNamespaceName,
			Name:             vmName,
			ImageName:        linuxVMIName,
			VMClassName:      clusterResources.VMClassName,
			StorageClassName: clusterResources.StorageClassName,
			PowerState:       "PoweredOff",
		}
		vmYaml = manifestbuilders.GetVirtualMachineYamlA2(vmParameters)
		Expect(clusterProxy.CreateWithArgs(ctx, vmYaml)).To(Succeed(), "failed to create virtualmachine:\n %s", string(vmYaml))

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
			vm := &vmopv1a3.VirtualMachine{}

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
			vm := &vmopv1a3.VirtualMachine{}
			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			vm.Spec.PowerState = vmopv1a3.VirtualMachinePowerStateOn

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

	It("Should apply routing-policy rules and route tables for a CloudInit VM with two interfaces on the same network", Label("experimental"), func() {
		if !isVMMutableNetworksCapEnabled {
			Skip("VM Mutable Networks capability is not enabled")
		}
		if !isVMRoutingPoliciesCapEnabled {
			Skip("VM Routing Policies capability is not enabled")
		}

		const (
			eth1Name        = "eth1"
			routeTable      = int64(100)
			routeToCIDR     = "10.99.0.0/16"
			eth1DNSOverride = "8.8.4.4"
		)

		vmParameters := manifestbuilders.VirtualMachineYaml{
			Namespace:        input.WCPNamespaceName,
			Name:             vmName,
			ImageName:        linuxVMIName,
			VMClassName:      clusterResources.VMClassName,
			StorageClassName: clusterResources.StorageClassName,
			PowerState:       "PoweredOff",
			Bootstrap: manifestbuilders.Bootstrap{
				CloudInit: &manifestbuilders.CloudInit{},
			},
		}
		vmYaml = manifestbuilders.GetVirtualMachineYamlA6(vmParameters)
		Expect(clusterProxy.CreateWithArgs(ctx, vmYaml)).To(Succeed(), "failed to create virtualmachine:\n %s", string(vmYaml))

		vmoperator.WaitForVirtualMachineToExist(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)

		key := ctrlclient.ObjectKey{Name: vmName, Namespace: input.WCPNamespaceName}

		By("Add second network interface, on the same network, to VM Spec")
		Eventually(func() bool {
			vm := &vmopv1a6.VirtualMachine{}

			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			Expect(vm.Spec.Network).ToNot(BeNil())
			Expect(vm.Spec.Network.Interfaces).To(HaveLen(1))
			vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces, vm.Spec.Network.Interfaces[0])
			vm.Spec.Network.Interfaces[1].Name = eth1Name

			err = svClusterClient.Update(ctx, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			return true
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(BeTrue(), "Timed out updating VirtualMachine %s to add second network interface", vmName)

		// This assumes the network provider resolves the interface's IP/gateway
		// into status.network.config while the VM is still powered off (true for
		// NSX/VPC IPAM). On a DHCP/VDS testbed this may not populate until the
		// guest boots and Tools report in, in which case this Eventually will
		// time out and the test needs a topology gate, similar to the
		// IsNetworkNsxtVPC skip used by the "moved to a different subnet" test
		// above.
		By("Wait for the second interface's configured IP and gateway to be resolved by the network provider")
		var eth1CIDR, eth1Gateway4 string
		Eventually(func(g Gomega) {
			vm := &vmopv1a6.VirtualMachine{}
			g.Expect(svClusterClient.Get(ctx, key, vm)).To(Succeed())
			g.Expect(vm.Status.Network).ToNot(BeNil())
			g.Expect(vm.Status.Network.Config).ToNot(BeNil())

			for _, ifaceStatus := range vm.Status.Network.Config.Interfaces {
				if ifaceStatus.Name == eth1Name && ifaceStatus.IP != nil {
					eth1CIDR = ""
					if len(ifaceStatus.IP.Addresses) > 0 {
						eth1CIDR = ifaceStatus.IP.Addresses[0]
					}
					eth1Gateway4 = ifaceStatus.IP.Gateway4
				}
			}

			g.Expect(eth1CIDR).ToNot(BeEmpty(), "eth1's configured IP address was not resolved")
			g.Expect(eth1Gateway4).ToNot(BeEmpty(), "eth1's configured gateway4 was not resolved")
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed(),
			"Timed out waiting for eth1's configured IP and gateway")

		// The route's via address must be a reachable next hop. Since eth1
		// shares eth0's network, eth1's own configured gateway is guaranteed
		// to be on-link for eth1's subnet.
		eth1Via := strings.SplitN(eth1Gateway4, "/", 2)[0]

		By("Configure a route table and a routing-policy rule matching eth1's own address on the second interface")
		Eventually(func() bool {
			vm := &vmopv1a6.VirtualMachine{}

			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			Expect(vm.Spec.Network.Interfaces).To(HaveLen(2))
			vm.Spec.Network.Interfaces[1].Nameservers = []string{eth1DNSOverride}
			vm.Spec.Network.Interfaces[1].Routes = []vmopv1a6.VirtualMachineNetworkRouteSpec{
				{
					To:    routeToCIDR,
					Via:   eth1Via,
					Table: ptr.To(routeTable),
				},
			}
			vm.Spec.Network.Interfaces[1].RoutingPolicies = []vmopv1a6.VirtualMachineNetworkRoutingPolicySpec{
				{
					From:     eth1CIDR,
					Table:    routeTable,
					Priority: ptr.To(int64(100)),
				},
			}

			err = svClusterClient.Update(ctx, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			return true
		}, config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(BeTrue(),
			"Timed out updating VirtualMachine %s with routing policy and route table", vmName)

		By("Power on VM")
		Eventually(func() bool {
			vm := &vmopv1a6.VirtualMachine{}
			err := svClusterClient.Get(ctx, key, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			vm.Spec.PowerState = vmopv1a6.VirtualMachinePowerStateOn

			err = svClusterClient.Update(ctx, vm)
			if err != nil {
				e2eframework.Logf("retry due to: %v", err)
				return false
			}

			return true
		}, config.GetIntervals("default", "wait-virtual-machine-powerstate")...).Should(BeTrue(), "Timed out updating VirtualMachine %s PowerState to On", vmName)

		vmoperator.WaitForVirtualMachinePowerState(ctx, config, svClusterClient, input.WCPNamespaceName, vmName, "PoweredOn")
		vmoperator.WaitForVirtualMachineIP(ctx, config, svClusterClient, input.WCPNamespaceName, vmName)
		vmIP := vmoperator.GetVirtualMachineIP(ctx, svClusterClient, input.WCPNamespaceName, vmName)

		By("Verify the routing-policy rule, route table, and per-interface DNS are applied inside the guest")
		verifyNetworkingGuestCmds(ctx, config, clusterProxy, svClusterClient, input.WCPNamespaceName, vmIP,
			[]string{
				"ip rule show",
				fmt.Sprintf("ip route show table %d", routeTable),
				fmt.Sprintf("resolvectl status %s", eth1Name),
			},
			[]string{
				fmt.Sprintf("lookup %d", routeTable),
				routeToCIDR,
				eth1DNSOverride,
			},
		)
	})
}

// verifyNetworkingGuestCmds runs cmds inside the VM's guest over SSH (routed via
// the NSX jumpbox or directly over the VDS gateway, depending on the testbed's
// networking topology) and asserts each command's output contains the
// corresponding entry in expectedOutput.
func verifyNetworkingGuestCmds(
	ctx context.Context,
	config *e2eConfig.E2EConfig,
	clusterProxy *common.VMServiceClusterProxy,
	svClusterClient ctrlclient.Client,
	namespace, vmIP string,
	cmds, expectedOutput []string) {

	switch config.InfraConfig.NetworkingTopology {
	case consts.NSX:
		vmservice.WaitForPodReady(ctx, config, svClusterClient, namespace, consts.JumpboxPodVMName)
		vmservice.VerifyLoginAndRunCmdsInNSXSetup(ctx, config, clusterProxy, namespace, consts.JumpboxPodVMName, vmIP, cmds, expectedOutput)
	case consts.VDS:
		vmservice.VerifyLoginAndRunCmdsInVDSSetup(config, vmIP, cmds, expectedOutput)
	}
}
