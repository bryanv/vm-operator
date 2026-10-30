// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopcommon "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/e2e/manifestbuilders"
	"github.com/vmware-tanzu/vm-operator/test/e2e/utils"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/common"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/consts"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/lib/vmoperator"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/vmservice"
)

type lifecycleCoverageInput struct {
	VMSpecInput
	vimClient *vim25.Client
	imageName string
}

// vmLifecycleCoverage uses the enclosing VMSpec's cluster setup and diagnostics.
func vmLifecycleCoverage(ctx context.Context, inputGetter func() lifecycleCoverageInput) {
	var (
		input lifecycleCoverageInput
		vm    *vmopv1.VirtualMachine
		c     ctrlclient.Client
	)

	BeforeEach(func() {
		input = inputGetter()
		c = input.ClusterProxy.GetClient()
		resources := input.Config.InfraConfig.ManagementClusterConfig.Resources
		secret := manifestbuilders.CloudConfigSecret(manifestbuilders.Secret{
			Namespace: input.WCPNamespaceName,
			Name:      "lifecycle-" + capiutil.RandomString(6),
		})
		Expect(c.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() {
			if !input.SkipCleanup {
				Expect(ctrlclient.IgnoreNotFound(c.Delete(ctx, secret))).To(Succeed())
			}
		})
		vm = &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{Name: secret.Name, Namespace: secret.Namespace},
			Spec: vmopv1.VirtualMachineSpec{
				ClassName: resources.VMClassName, ImageName: input.imageName,
				StorageClass: resources.StorageClassName,
				PowerState:   vmopv1.VirtualMachinePowerStateOn,
				Bootstrap: &vmopv1.VirtualMachineBootstrapSpec{
					CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
						RawCloudConfig: &vmopcommon.SecretKeySelector{Name: secret.Name, Key: "user-data"},
					},
				},
			},
		}
	})

	// vmop-3580: class changes affect existing VMs only after explicit opt-in.
	It("resizes from an updated class only after opting into same-class resize", Label("core-functional"), func() {
		if !utils.IsFssEnabled(ctx, c, input.Config.GetVariable("VMOPNamespace"),
			input.Config.GetVariable("VMOPDeploymentName"), input.Config.GetVariable("VMOPManagerCommand"),
			input.Config.GetVariable("EnvFSSVMResizeCPUMemory")) {
			Skip("VM CPU/memory resize is disabled")
		}
		className := "same-class-" + capiutil.RandomString(6)
		classSpec := vmservice.CreateSpecCustomizedVMClass(className, 2, 2048, "")
		Expect(input.WCPClient.CreateVMClass(classSpec)).To(Succeed())
		DeferCleanup(vmservice.VerifyVMClassDeletion, input.WCPClient, className)
		Expect(vmservice.EnsureNamespaceHasAccess(input.WCPClient, className, vm.Namespace)).To(Succeed())
		vm.Spec.ClassName = className
		vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
		vmoperator.CreateVirtualMachine(ctx, input.Config, input.ClusterProxy, vm, input.SkipCleanup)
		vmoperator.WaitForVirtualMachineConditionCreated(ctx, input.Config, c, vm.Namespace, vm.Name)
		Eventually(func(g Gomega) {
			properties := lifecycleVMProperties(ctx, input, vm)
			g.Expect(properties.Config.Hardware.NumCPU).To(BeEquivalentTo(2))
			g.Expect(properties.Config.Hardware.MemoryMB).To(BeEquivalentTo(2048))
		}, input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())
		waitForLifecycleCondition(ctx, input, vm, vmopv1.VirtualMachineClassConfigurationSynced, metav1.ConditionTrue)

		By("Updating the dedicated class without opting the VM into same-class resize")
		classSpec.CPUCount = ptr.To(4)
		classSpec.MemoryMB = ptr.To(4096)
		Expect(input.WCPClient.UpdateVMClass(classSpec)).To(Succeed())
		waitForLifecycleCondition(ctx, input, vm, vmopv1.VirtualMachineClassConfigurationSynced, metav1.ConditionFalse)
		Consistently(func(g Gomega) {
			properties := lifecycleVMProperties(ctx, input, vm)
			g.Expect(properties.Config.Hardware.NumCPU).To(BeEquivalentTo(2))
			g.Expect(properties.Config.Hardware.MemoryMB).To(BeEquivalentTo(2048))
		}, input.Config.GetIntervals("default", "consistent-virtual-machine-condition")...).Should(Succeed())

		By("Opting into the class update and waiting for the actual hardware to converge")
		Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
		patch := ctrlclient.MergeFrom(vm.DeepCopy())
		metav1.SetMetaDataAnnotation(&vm.ObjectMeta, vmopv1.VirtualMachineSameVMClassResizeAnnotation, "true")
		Expect(c.Patch(ctx, vm, patch)).To(Succeed())
		Eventually(func(g Gomega) {
			properties := lifecycleVMProperties(ctx, input, vm)
			g.Expect(properties.Config.Hardware.NumCPU).To(BeEquivalentTo(4))
			g.Expect(properties.Config.Hardware.MemoryMB).To(BeEquivalentTo(4096))
			g.Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
			g.Expect(vm.Spec.ClassName).To(Equal(className))
			g.Expect(apimeta.IsStatusConditionTrue(vm.Status.Conditions, vmopv1.VirtualMachineClassConfigurationSynced)).To(BeTrue())
		}, input.Config.GetIntervals("default", "wait-virtual-machine-resize")...).Should(Succeed())
	})

	// vmop-3581: a restart status timestamp alone cannot prove the guest rebooted.
	DescribeTable("restarts the guest and acknowledges each restart request", Label("core-functional"),
		func(mode vmopv1.VirtualMachinePowerOpMode) {
			createLifecycleVM(ctx, input, vm)
			waitForLifecycleCondition(ctx, input, vm, vmopv1.VirtualMachineToolsCondition, metav1.ConditionTrue)
			runLifecycleGuestCommand(ctx, input, vm,
				"cat /proc/sys/kernel/random/boot_id > /var/tmp/vmop-e2e-boot-id && sync && echo recorded", "recorded")
			Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
			lastRestart := vm.Status.LastRestartTime.DeepCopy()
			patch := ctrlclient.MergeFrom(vm.DeepCopy())
			vm.Spec.RestartMode = mode
			vm.Spec.NextRestartTime = "now"
			Expect(c.Patch(ctx, vm, patch)).To(Succeed())
			requestTime, err := time.Parse(time.RFC3339Nano, vm.Spec.NextRestartTime)
			Expect(err).ToNot(HaveOccurred(), "admission must normalize now to a timestamp")
			requestTime = requestTime.Truncate(time.Second)
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
				g.Expect(vm.Status.LastRestartTime).ToNot(BeNil())
				g.Expect(vm.Status.LastRestartTime.Time.Before(requestTime)).To(BeFalse())
				if lastRestart != nil {
					g.Expect(vm.Status.LastRestartTime.After(lastRestart.Time)).To(BeTrue())
				}
				g.Expect(vm.Status.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOn))
			}, input.Config.GetIntervals("default", "wait-virtual-machine-powerstate")...).Should(Succeed())
			runLifecycleGuestCommand(ctx, input, vm,
				"test -s /var/tmp/vmop-e2e-boot-id && test \"$(cat /proc/sys/kernel/random/boot_id)\" != \"$(cat /var/tmp/vmop-e2e-boot-id)\" && echo rebooted", "rebooted")
		},
		Entry("using Hard", vmopv1.VirtualMachinePowerOpModeHard),
	)

}

func createLifecycleVM(ctx context.Context, input lifecycleCoverageInput, vm *vmopv1.VirtualMachine) {
	GinkgoHelper()
	vmoperator.CreateVirtualMachine(ctx, input.Config, input.ClusterProxy, vm, input.SkipCleanup)
	vmoperator.WaitForVirtualMachineCreation(ctx, input.Config, input.ClusterProxy.GetClient(), vm.Namespace, vm.Name)
}

func waitForLifecycleCondition(ctx context.Context, input lifecycleCoverageInput, vm *vmopv1.VirtualMachine, conditionType string, status metav1.ConditionStatus) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(input.ClusterProxy.GetClient().Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
		condition := apimeta.FindStatusCondition(vm.Status.Conditions, conditionType)
		g.Expect(condition).ToNot(BeNil())
		g.Expect(condition.Status).To(Equal(status))
	}, input.Config.GetIntervals("default", "wait-virtual-machine-condition-update")...).Should(Succeed())
}

func lifecycleVMProperties(ctx context.Context, input lifecycleCoverageInput, vm *vmopv1.VirtualMachine) mo.VirtualMachine {
	GinkgoHelper()
	moid := vmoperator.GetVirtualMachineMOID(ctx, input.ClusterProxy.GetClient(), vm.Namespace, vm.Name)
	var result mo.VirtualMachine
	Expect(property.DefaultCollector(input.vimClient).RetrieveOne(ctx,
		vimtypes.ManagedObjectReference{Type: "VirtualMachine", Value: moid},
		[]string{"config", "runtime", "guestHeartbeatStatus"}, &result)).To(Succeed())
	return result
}

func runLifecycleGuestCommand(ctx context.Context, input lifecycleCoverageInput, vm *vmopv1.VirtualMachine, command, want string) {
	GinkgoHelper()
	clusterProxy := input.ClusterProxy.(*common.VMServiceClusterProxy)
	ip := vmoperator.GetVirtualMachineIP(ctx, clusterProxy.GetClient(), vm.Namespace, vm.Name)
	if input.Config.InfraConfig.NetworkingTopology == consts.NSX {
		vmservice.WaitForPodReady(ctx, input.Config, clusterProxy.GetClient(), vm.Namespace, consts.JumpboxPodVMName)
		vmservice.VerifyLoginAndRunCmdsInNSXSetup(ctx, input.Config, clusterProxy, vm.Namespace,
			consts.JumpboxPodVMName, ip, []string{command}, []string{want})
	} else {
		vmservice.VerifyLoginAndRunCmdsInVDSSetup(input.Config, ip, []string{command}, []string{want})
	}
}
