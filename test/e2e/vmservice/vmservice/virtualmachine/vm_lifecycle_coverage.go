// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopcommon "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
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
		Entry("using Soft", vmopv1.VirtualMachinePowerOpModeSoft),
		Entry("using TrySoft", vmopv1.VirtualMachinePowerOpModeTrySoft),
	)

	// vmop-3586: exercise guest shutdown and fallback when Tools are unavailable.
	DescribeTable("powers off a guest using the requested mode", Label("extended-functional"),
		func(mode vmopv1.VirtualMachinePowerOpMode, stopTools bool) {
			createLifecycleVM(ctx, input, vm)
			waitForLifecycleCondition(ctx, input, vm, vmopv1.VirtualMachineToolsCondition, metav1.ConditionTrue)
			if stopTools {
				runLifecycleGuestCommand(ctx, input, vm,
					"(sudo systemctl stop vmtoolsd.service 2>/dev/null || sudo systemctl stop open-vm-tools.service) && echo stopped", "stopped")
				waitForLifecycleCondition(ctx, input, vm, vmopv1.VirtualMachineToolsCondition, metav1.ConditionFalse)
			}
			Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
			patch := ctrlclient.MergeFrom(vm.DeepCopy())
			vm.Spec.PowerOffMode = mode
			vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
			Expect(c.Patch(ctx, vm, patch)).To(Succeed())
			vmoperator.WaitForVirtualMachinePowerState(ctx, input.Config, c, vm.Namespace, vm.Name, poweredOffState)
			Expect(lifecycleVMProperties(ctx, input, vm).Runtime.PowerState).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
		},
		Entry("using Soft with running Tools", vmopv1.VirtualMachinePowerOpModeSoft, false),
		Entry("using TrySoft with running Tools", vmopv1.VirtualMachinePowerOpModeTrySoft, false),
		Entry("using TrySoft with stopped Tools to require hard fallback", vmopv1.VirtualMachinePowerOpModeTrySoft, true),
	)

	// vmop-3584: each GuestInfo expression participates in the AND evaluation.
	It("updates readiness when GuestInfo expressions start and stop matching", Label("core-functional"), func() {
		vm.Spec.ReadinessProbe = &vmopv1.VirtualMachineReadinessProbeSpec{
			PeriodSeconds: 2,
			GuestInfo: []vmopv1.GuestInfoAction{
				{Key: "e2e.phase", Value: "^ready$"},
				{Key: "e2e.version", Value: "^v[0-9]+$"},
			},
		}
		createLifecycleVM(ctx, input, vm)
		waitForLifecycleCondition(ctx, input, vm, vmopv1.ReadyConditionType, metav1.ConditionFalse)
		setLifecycleGuestInfo(ctx, input, vm, "e2e.phase", "ready")
		Consistently(func(g Gomega) {
			g.Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
			g.Expect(apimeta.IsStatusConditionFalse(vm.Status.Conditions, vmopv1.ReadyConditionType)).To(BeTrue())
		}, input.Config.GetIntervals("default", "consistent-virtual-machine-condition")...).Should(Succeed())
		setLifecycleGuestInfo(ctx, input, vm, "e2e.version", "v2")
		waitForLifecycleCondition(ctx, input, vm, vmopv1.ReadyConditionType, metav1.ConditionTrue)
		setLifecycleGuestInfo(ctx, input, vm, "e2e.phase", "starting")
		waitForLifecycleCondition(ctx, input, vm, vmopv1.ReadyConditionType, metav1.ConditionFalse)
	})

	It("marks a guest ready after its heartbeat reaches the configured threshold", Label("core-functional"), func() {
		vm.Spec.ReadinessProbe = &vmopv1.VirtualMachineReadinessProbeSpec{
			PeriodSeconds:  2,
			GuestHeartbeat: &vmopv1.GuestHeartbeatAction{ThresholdStatus: vmopv1.GreenHeartbeatStatus},
		}
		createLifecycleVM(ctx, input, vm)
		waitForLifecycleCondition(ctx, input, vm, vmopv1.ReadyConditionType, metav1.ConditionTrue)
		Eventually(func(g Gomega) {
			g.Expect(lifecycleVMProperties(ctx, input, vm).GuestHeartbeatStatus).To(Equal(vimtypes.ManagedEntityStatusGreen))
		}, input.Config.GetIntervals("default", "wait-virtual-machine-condition-update")...).Should(Succeed())
	})

	It("updates TCP readiness when the probed port becomes reachable", Label("core-functional"), func() {
		if input.Config.InfraConfig.NetworkingTopology != consts.NSX {
			Skip("TCP readiness requires operator connectivity to the NSX workload network")
		}
		vm.Spec.ReadinessProbe = &vmopv1.VirtualMachineReadinessProbeSpec{
			PeriodSeconds: 2,
			TCPSocket:     &vmopv1.TCPSocketAction{Port: intstr.FromInt32(1)},
		}
		createLifecycleVM(ctx, input, vm)
		waitForLifecycleCondition(ctx, input, vm, vmopv1.ReadyConditionType, metav1.ConditionFalse)
		for _, port := range []int32{22, 1} {
			Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
			patch := ctrlclient.MergeFrom(vm.DeepCopy())
			vm.Spec.ReadinessProbe.TCPSocket.Port = intstr.FromInt32(port)
			Expect(c.Patch(ctx, vm, patch)).To(Succeed())
			status := metav1.ConditionFalse
			if port == 22 {
				status = metav1.ConditionTrue
			}
			waitForLifecycleCondition(ctx, input, vm, vmopv1.ReadyConditionType, status)
		}
	})

	// vmop-3589: submit the pause and pending change atomically.
	It("defers a power-state change while paused and applies it after resume", Label("extended-functional"), func() {
		createLifecycleVM(ctx, input, vm)
		DeferCleanup(func() {
			if input.SkipCleanup {
				return
			}
			// A failed assertion must not leave the annotation blocking VM deletion.
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
				if _, paused := vm.Annotations[vmopv1.PauseAnnotation]; paused {
					patch := ctrlclient.MergeFrom(vm.DeepCopy())
					delete(vm.Annotations, vmopv1.PauseAnnotation)
					g.Expect(c.Patch(ctx, vm, patch)).To(Succeed())
				}
			}).Should(Succeed())
		})
		Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
		patch := ctrlclient.MergeFrom(vm.DeepCopy())
		metav1.SetMetaDataAnnotation(&vm.ObjectMeta, vmopv1.PauseAnnotation, "true")
		vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
		vm.Spec.PowerOffMode = vmopv1.VirtualMachinePowerOpModeHard
		Expect(c.Patch(ctx, vm, patch)).To(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(lifecycleVMProperties(ctx, input, vm).Runtime.PowerState).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
		}, input.Config.GetIntervals("default", "consistent-virtual-machine-condition")...).Should(Succeed())
		Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
		patch = ctrlclient.MergeFrom(vm.DeepCopy())
		delete(vm.Annotations, vmopv1.PauseAnnotation)
		Expect(c.Patch(ctx, vm, patch)).To(Succeed())
		vmoperator.WaitForVirtualMachinePowerState(ctx, input.Config, c, vm.Namespace, vm.Name, poweredOffState)
		Expect(lifecycleVMProperties(ctx, input, vm).Runtime.PowerState).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
	})

	// vmop-3590: successful GuestID changes remove the error condition.
	It("applies explicit GuestID and offline updates and rejects online updates", Label("extended-functional"), func() {
		vm.Spec.GuestID = "otherLinux64Guest"
		vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
		vmoperator.CreateVirtualMachine(ctx, input.Config, input.ClusterProxy, vm, input.SkipCleanup)
		vmoperator.WaitForVirtualMachineConditionCreated(ctx, input.Config, c, vm.Namespace, vm.Name)
		Eventually(func(g Gomega) {
			g.Expect(lifecycleVMProperties(ctx, input, vm).Config.GuestId).To(Equal("otherLinux64Guest"))
		}, input.Config.GetIntervals("default", "wait-virtual-machine-condition-update")...).Should(Succeed())
		Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
		patch := ctrlclient.MergeFrom(vm.DeepCopy())
		vm.Spec.GuestID = "ubuntu64Guest"
		Expect(c.Patch(ctx, vm, patch)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(lifecycleVMProperties(ctx, input, vm).Config.GuestId).To(Equal("ubuntu64Guest"))
			g.Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
			g.Expect(apimeta.FindStatusCondition(vm.Status.Conditions, vmopv1.GuestIDReconfiguredCondition)).To(BeNil())
		}, input.Config.GetIntervals("default", "wait-virtual-machine-condition-update")...).Should(Succeed())
		vmoperator.UpdateVirtualMachinePowerState(ctx, input.Config, c, vm.Namespace, vm.Name, poweredOnState)
		vmoperator.WaitForVirtualMachinePowerState(ctx, input.Config, c, vm.Namespace, vm.Name, poweredOnState)
		Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
		patch = ctrlclient.MergeFrom(vm.DeepCopy())
		vm.Spec.GuestID = "otherLinux64Guest"
		Expect(c.Patch(ctx, vm, patch)).To(MatchError(ContainSubstring("guestID")))
		Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
		Expect(vm.Spec.GuestID).To(Equal("ubuntu64Guest"))
	})

	// vmop-3585: snapshot coverage enables CBT but does not disable it.
	It("enables and disables CBT on a powered-off VM", Label("extended-functional"), func() {
		vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
		vm.Spec.Advanced = &vmopv1.VirtualMachineAdvancedSpec{ChangeBlockTracking: ptr.To(true)}
		vmoperator.CreateVirtualMachine(ctx, input.Config, input.ClusterProxy, vm, input.SkipCleanup)
		vmoperator.WaitForVirtualMachineConditionCreated(ctx, input.Config, c, vm.Namespace, vm.Name)
		for _, enabled := range []bool{true, false, true} {
			By(fmt.Sprintf("Verifying CBT=%t in both status and vSphere", enabled))
			Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
			patch := ctrlclient.MergeFrom(vm.DeepCopy())
			vm.Spec.Advanced.ChangeBlockTracking = ptr.To(enabled)
			Expect(c.Patch(ctx, vm, patch)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), vm)).To(Succeed())
				g.Expect(vm.Status.ChangeBlockTracking).To(Equal(ptr.To(enabled)))
				g.Expect(lifecycleVMProperties(ctx, input, vm).Config.ChangeTrackingEnabled).To(Equal(ptr.To(enabled)))
			}, input.Config.GetIntervals("default", "wait-virtual-machine-condition-update")...).Should(Succeed())
		}
	})

	// vmop-4405: encryption must not be what implicitly selects direct mode.
	It("explicitly selects fast-deploy direct mode for an ordinary VM", Label("core-functional"), func() {
		if !utils.IsFssEnabled(ctx, c, input.Config.GetVariable("VMOPNamespace"),
			input.Config.GetVariable("VMOPDeploymentName"), input.Config.GetVariable("VMOPManagerCommand"),
			"FSS_WCP_VMSERVICE_FAST_DEPLOY") {
			Skip("FastDeploy is disabled")
		}
		vm.Annotations = map[string]string{pkgconst.FastDeployAnnotationKey: pkgconst.FastDeployModeDirect}
		vm.Spec.PromoteDisksMode = vmopv1.VirtualMachinePromoteDisksModeDisabled
		createLifecycleVM(ctx, input, vm)
		waitForLifecycleCondition(ctx, input, vm, vmopv1.VirtualMachineConditionImageCacheReady, metav1.ConditionTrue)
		properties := lifecycleVMProperties(ctx, input, vm)
		devices := object.VirtualDeviceList(properties.Config.Hardware.Device)
		Expect(properties.Config.KeyId).To(BeNil(), "encryption must not implicitly select direct mode")
		disks := devices.SelectByType((*vimtypes.VirtualDisk)(nil))
		Expect(disks).ToNot(BeEmpty())
		for _, device := range disks {
			backing, ok := device.(*vimtypes.VirtualDisk).Backing.(*vimtypes.VirtualDiskFlatVer2BackingInfo)
			Expect(ok).To(BeTrue(), "expected a flat disk for direct deployment")
			Expect(backing.Parent).To(BeNil(), "direct-deploy disks must not depend on cached parent disks")
			Expect(backing.KeyId).To(BeNil(), "disk encryption must not implicitly select direct mode")
		}
	})
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

func setLifecycleGuestInfo(ctx context.Context, input lifecycleCoverageInput, vm *vmopv1.VirtualMachine, key, value string) {
	GinkgoHelper()
	moid := vmoperator.GetVirtualMachineMOID(ctx, input.ClusterProxy.GetClient(), vm.Namespace, vm.Name)
	vcVM := object.NewVirtualMachine(input.vimClient, vimtypes.ManagedObjectReference{Type: "VirtualMachine", Value: moid})
	task, err := vcVM.Reconfigure(ctx, vimtypes.VirtualMachineConfigSpec{
		ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "guestinfo." + key, Value: value}},
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(task.Wait(ctx)).To(Succeed())
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
