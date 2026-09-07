// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vsphere_test

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/pkg/conditions"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	ctxop "github.com/vmware-tanzu/vm-operator/pkg/context/operation"
	pkgerr "github.com/vmware-tanzu/vm-operator/pkg/errors"
	"github.com/vmware-tanzu/vm-operator/pkg/providers"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere"
	upgradevm "github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/upgrade/virtualmachine"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine"
	kubeutil "github.com/vmware-tanzu/vm-operator/pkg/util/kube"
	"github.com/vmware-tanzu/vm-operator/pkg/util/kube/cource"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ovfcache"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	vmconfunmanagedvolsfil "github.com/vmware-tanzu/vm-operator/pkg/vmconfig/volumes/unmanaged/backfill"
	vmconfunmanagedvolsreg "github.com/vmware-tanzu/vm-operator/pkg/vmconfig/volumes/unmanaged/register"
	"github.com/vmware-tanzu/vm-operator/test/builder"
	"github.com/vmware-tanzu/vm-operator/test/testutil"
)

var _ = Describe(
	"VirtualMachineSnapshot",
	Label(testlabels.VCSim),
	Label(testlabels.Snapshot), func() {

		const (
			dummySnapshot = "dummy-snapshot"
		)

		var (
			initObjects []ctrlclient.Object
			ctx         *builder.TestContextForVCSim
			vmProvider  providers.VirtualMachineProviderInterface
			nsInfo      builder.WorkloadNamespaceInfo
			vmSnapshot  *vmopv1.VirtualMachineSnapshot
			vcVM        *object.VirtualMachine
			vm          *vmopv1.VirtualMachine
			vmCtx       pkgctx.VirtualMachineContext
		)

		BeforeEach(func() {
			ctx = suite.NewTestContextForVCSim(builder.VCSimTestConfig{}, initObjects...)
			vmProvider = vsphere.NewVSphereVMProviderFromClient(ctx, ctx.Client, ctx.Recorder)
			nsInfo = ctx.CreateWorkloadNamespace()

			var err error
			vcVM, err = ctx.Finder.VirtualMachine(ctx, "DC0_C0_RP0_VM0")
			Expect(err).ToNot(HaveOccurred())
			Expect(vcVM).ToNot(BeNil())

			By("Creating VM CR")
			vm = builder.DummyBasicVirtualMachine(dummySnapshot, nsInfo.Namespace)
			vm.Status.UniqueID = vcVM.Reference().Value
			Expect(ctx.Client.Create(ctx, vm)).To(Succeed())

			By("Creating snapshot CR")
			vmSnapshot = builder.DummyVirtualMachineSnapshot(nsInfo.Namespace, dummySnapshot, vcVM.Name())
			Expect(ctx.Client.Create(ctx, vmSnapshot)).To(Succeed())

			// TODO (lubron): Add FCD to the VM and test the snapshot size once
			// vcsim has support to show attached disk as device

			By("Creating snapshot on vSphere")
			logger := testutil.GinkgoLogr(5)
			vmCtx = pkgctx.VirtualMachineContext{
				Context: logr.NewContext(ctx, logger),
				Logger:  logger.WithValues("vmName", vcVM.Name()),
				VM:      vm,
			}
			args := virtualmachine.SnapshotArgs{
				VMCtx:      vmCtx,
				VMSnapshot: *vmSnapshot,
				VcVM:       vcVM,
			}
			snapMo, err := virtualmachine.CreateSnapshot(args)
			Expect(err).ToNot(HaveOccurred())
			Expect(snapMo).ToNot(BeNil())
		})

		AfterEach(func() {
			ctx.AfterEach()
			ctx = nil
			initObjects = nil
			vmProvider = nil
			vmSnapshot = nil
			vmCtx = pkgctx.VirtualMachineContext{}
			vm = nil
			nsInfo = builder.WorkloadNamespaceInfo{}
		})

		Context("GetSnapshotSize", func() {
			It("should return the size of the snapshot", func() {
				size, err := vmProvider.GetSnapshotSize(ctx, vmSnapshot.Name, vm)
				Expect(err).ToNot(HaveOccurred())

				// Since we only have one snapshot, the size should be same as the vm
				var moVM mo.VirtualMachine
				Expect(vcVM.Properties(ctx, vcVM.Reference(), []string{"layoutEx"}, &moVM)).To(Succeed())
				var total int64
				for _, file := range moVM.LayoutEx.File {
					switch filepath.Ext(file.Name) {
					case ".vmdk", ".vmsn", ".vmem":
						total += file.Size
					}
				}
				Expect(size).To(Equal(total))
			})

			When("there is issue finding vm", func() {
				BeforeEach(func() {
					vm.Status.UniqueID = ""
				})
				It("should return error", func() {
					size, err := vmProvider.GetSnapshotSize(ctx, vmSnapshot.Name, vm)
					Expect(err).To(HaveOccurred())
					Expect(size).To(BeZero())
				})
			})

			When("there is issue finding snapshot", func() {
				BeforeEach(func() {
					vmSnapshot.Name = ""
				})
				It("should return error", func() {
					size, err := vmProvider.GetSnapshotSize(ctx, vmSnapshot.Name, vm)
					Expect(err).To(HaveOccurred())
					Expect(size).To(BeZero())
				})
			})
		})

		Context("DeleteSnapshot", func() {
			var (
				deleted bool
				err     error
			)

			JustBeforeEach(func() {
				deleted, err = vmProvider.DeleteSnapshot(ctx, vmSnapshot, vm, true, nil)
			})

			It("should return false and no error", func() {
				Expect(deleted).To(BeFalse())
				Expect(err).NotTo(HaveOccurred())
				snapMoRef, err := vcVM.FindSnapshot(ctx, dummySnapshot)
				Expect(err).To(HaveOccurred())
				Expect(snapMoRef).To(BeNil())
			})

			Context("VM is not found", func() {
				BeforeEach(func() {
					vm.Status.UniqueID = ""
				})
				It("should return true and no error", func() {
					Expect(deleted).To(BeTrue())
					Expect(err).NotTo(HaveOccurred())
					snapMoRef, err := vcVM.FindSnapshot(ctx, dummySnapshot)
					Expect(err).NotTo(HaveOccurred())
					Expect(snapMoRef).NotTo(BeNil())
				})
			})

			Context("snapshot not found", func() {
				BeforeEach(func() {
					By("Deleting snapshot in advance")
					Expect(virtualmachine.DeleteSnapshot(virtualmachine.SnapshotArgs{
						VMCtx:      vmCtx,
						VMSnapshot: *vmSnapshot,
						VcVM:       vcVM,
					})).To(Succeed())
				})
				It("should return false and no error", func() {
					Expect(deleted).To(BeFalse())
					Expect(err).NotTo(HaveOccurred())
				})
			})
		})

		Context("SyncVMSnapshotTreeStatus", func() {
			It("should sync the VM's current and root snapshots status", func() {
				Expect(vmProvider.SyncVMSnapshotTreeStatus(ctx, vm)).To(Succeed())
				Expect(vm.Status.CurrentSnapshot).ToNot(BeNil())
				Expect(vm.Status.CurrentSnapshot.Type).To(Equal(vmopv1.VirtualMachineSnapshotReferenceTypeManaged))
				Expect(vm.Status.CurrentSnapshot.Name).To(Equal(vmSnapshot.Name))
				Expect(vm.Status.RootSnapshots).To(HaveLen(1))
				Expect(vm.Status.RootSnapshots[0].Name).To(Equal(vmSnapshot.Name))
				Expect(vm.Status.RootSnapshots[0].Type).To(Equal(vmopv1.VirtualMachineSnapshotReferenceTypeManaged))
			})

			When("VM is not found", func() {
				BeforeEach(func() {
					vm.Status.UniqueID = ""
				})
				It("should return error", func() {
					Expect(vmProvider.SyncVMSnapshotTreeStatus(ctx, vm)).NotTo(Succeed())
				})
			})

			When("there is no snapshot", func() {
				BeforeEach(func() {
					Expect(virtualmachine.DeleteSnapshot(virtualmachine.SnapshotArgs{
						VMCtx:      vmCtx,
						VMSnapshot: *vmSnapshot,
						VcVM:       vcVM,
					})).To(Succeed())
				})
				It("should show expected current snapshot and root snapshots", func() {
					Expect(vmProvider.SyncVMSnapshotTreeStatus(ctx, vm)).To(Succeed())
					Expect(vm.Status.CurrentSnapshot).To(BeNil())
					Expect(vm.Status.RootSnapshots).To(BeNil())
				})
			})
		})

		Context("ReconcileCurrentSnapshot", func() {
			var (
				snapshot1 *vmopv1.VirtualMachineSnapshot
				snapshot2 *vmopv1.VirtualMachineSnapshot

				verifyK8sVMSnapshot = func(name, namespace string, isCreated bool) {
					GinkgoHelper()
					vmSnapshot := &vmopv1.VirtualMachineSnapshot{}
					Expect(ctx.Client.Get(ctx, ctrlclient.ObjectKey{
						Name:      name,
						Namespace: namespace,
					}, vmSnapshot)).To(Succeed())
					Expect(conditions.IsTrue(vmSnapshot, vmopv1.VirtualMachineSnapshotCreatedCondition)).To(Equal(isCreated))
				}

				verifyNoVcVMSnapshot = func() {
					GinkgoHelper()
					var moVM mo.VirtualMachine
					Expect(vcVM.Properties(ctx, vcVM.Reference(), []string{"snapshot"}, &moVM)).To(Succeed())
					Expect(moVM.Snapshot).To(BeNil())
				}
			)

			BeforeEach(func() {
				By("Deleting the snapshot on vSphere created in outer BeforeEach")
				Expect(virtualmachine.DeleteSnapshot(virtualmachine.SnapshotArgs{
					VMCtx:      vmCtx,
					VMSnapshot: *vmSnapshot,
					VcVM:       vcVM,
				})).To(Succeed())

				By("Deleting the snapshot CR created in outer BeforeEach")
				Expect(ctx.Client.Delete(ctx, vmSnapshot)).To(Succeed())
			})

			AfterEach(func() {
				snapshot1 = nil
				snapshot2 = nil
			})

			When("no snapshots exist", func() {
				It("should complete without error", func() {
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())
					verifyNoVcVMSnapshot()
				})
			})

			When("one snapshot exists and is not created", func() {
				JustBeforeEach(func() {
					// Create snapshot1 CR with owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())
				})

				It("should process the snapshot", func() {
					// Reconcile the current snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Verify snapshot is created.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, true)

					// Verify snapshot status.
					updatedSnapshot := &vmopv1.VirtualMachineSnapshot{}
					Expect(ctx.Client.Get(ctx, ctrlclient.ObjectKey{
						Name:      snapshot1.Name,
						Namespace: snapshot1.Namespace,
					}, updatedSnapshot)).To(Succeed())
					Expect(updatedSnapshot.Status.Quiesced).To(BeTrue())
					// Snapshot should be powered off since memory is not included in the snapshot.
					Expect(updatedSnapshot.Status.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
				})
			})

			When("multiple snapshots exist", func() {
				It("should process snapshots in order (oldest first)", func() {
					// Create snapshot1 CR with owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					creationTimeStamp := metav1.NewTime(time.Now())
					snapshot1.CreationTimestamp = creationTimeStamp
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Create snapshot2 CR with a later time and owner reference set to the VM.
					later := metav1.NewTime(time.Now().Add(1 * time.Second))
					snapshot2 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-2", vm.Name)
					snapshot2.CreationTimestamp = later
					Expect(controllerutil.SetOwnerReference(vm, snapshot2, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot2)).To(Succeed())

					// First reconcile should process snapshot1, and requeue to process snapshot2.
					err := vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)
					Expect(err).To(HaveOccurred())
					Expect(pkgerr.IsRequeueError(err)).To(BeTrue())
					Expect(err.Error()).To(ContainSubstring("requeuing to process 1 remaining snapshots"))

					// Check that snapshot1 is created.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, true)

					// Check that snapshot2 is NOT created.
					verifyK8sVMSnapshot(snapshot2.Name, snapshot2.Namespace, false)

					// Second reconcile should process snapshot2.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Check that snapshot2 is now created.
					verifyK8sVMSnapshot(snapshot2.Name, snapshot2.Namespace, true)

					// Note: The Children status is populated by SyncVMSnapshotTreeStatus,
					// not by ReconcileCurrentSnapshot, which is tested separately above.
				})
			})

			When("one snapshot is already in progress", func() {
				It("should process the in-progress snapshot and requeue for the next", func() {
					// Create snapshot1 CR with in progress condition and owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					conditions.MarkFalse(snapshot1,
						vmopv1.VirtualMachineSnapshotCreatedCondition,
						vmopv1.VirtualMachineSnapshotCreationInProgressReason,
						"in progress",
					)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Create snapshot2 CR with owner reference set to the VM.
					snapshot2 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-2", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot2, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot2)).To(Succeed())

					// Reconcile the current snapshot and expect a requeue error.
					err := vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)
					Expect(err).To(HaveOccurred())
					Expect(pkgerr.IsRequeueError(err)).To(BeTrue())

					// First snapshot should be created.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, true)

					// Second snapshot should NOT be created.
					verifyK8sVMSnapshot(snapshot2.Name, snapshot2.Namespace, false)
				})
			})

			When("snapshot is being deleted", func() {
				It("should skip all snapshot creation due to vSphere constraint", func() {
					// Create snapshot1 CR with owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					// Set a finalizer so we can delete the snapshot CR without it being removed from cluster.
					snapshot1.ObjectMeta.Finalizers = []string{"dummy-finalizer"}
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Create snapshot2 CR with owner reference set to the VM.
					snapshot2 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-2", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot2, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot2)).To(Succeed())

					// Delete snapshot1 CR.
					Expect(ctx.Client.Delete(ctx, snapshot1)).To(Succeed())

					// Reconcile the current snapshot and expect a requeue error.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// snapshot1 should NOT be created.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, false)

					// snapshot2 should NOT be created.
					verifyK8sVMSnapshot(snapshot2.Name, snapshot2.Namespace, false)
				})
			})

			When("snapshot already exists and has created condition", func() {
				It("should skip ready snapshot and process the next one", func() {
					// Create snapshot1 CR with created condition and owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					conditions.MarkTrue(snapshot1, vmopv1.VirtualMachineSnapshotCreatedCondition)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Create snapshot2 CR with owner reference set to the VM.
					snapshot2 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-2", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot2, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot2)).To(Succeed())

					// Reconcile the current snapshot and expect no error.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// snapshot1 should remain created.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, true)

					// snapshot2 should be processed and marked as created.
					verifyK8sVMSnapshot(snapshot2.Name, snapshot2.Namespace, true)
				})
			})

			When("snapshot has empty VM name", func() {
				It("should skip snapshot with empty VM name and process the next one", func() {
					// Create snapshot1 with empty vmName so both Spec.VMName and
					// VMNameForSnapshotLabel are empty — label filter excludes it.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", "")
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Create snapshot2 with owner reference set to the VM.
					snapshot2 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-2", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot2, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot2)).To(Succeed())

					// Reconcile the current snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// snapshot1 should not be processed (empty VMName).
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, false)

					// snapshot2 should be processed and marked as created.
					verifyK8sVMSnapshot(snapshot2.Name, snapshot2.Namespace, true)
				})
			})

			When("snapshot references different VM", func() {
				It("should skip snapshot for different VM and process the next one", func() {
					// Create snapshot1 with "different-vm" so both Spec.VMName and
					// VMNameForSnapshotLabel point to a different VM — label filter excludes it.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", "different-vm")
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Create snapshot2 CR with owner reference set to the VM.
					snapshot2 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-2", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot2, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot2)).To(Succeed())

					// Reconcile the current snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// snapshot1 should not be processed (different VM).
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, false)

					// snapshot2 should be processed and marked as created.
					verifyK8sVMSnapshot(snapshot2.Name, snapshot2.Namespace, true)
				})
			})

			When("VM is a VKS/TKG node", func() {
				It("should skip snapshot processing for VKS/TKG nodes", func() {
					// Add CAPI labels to mark VM as VKS/TKG node.
					vm.Labels = map[string]string{
						kubeutil.CAPWClusterRoleLabelKey: "worker",
					}
					Expect(ctx.Client.Update(ctx, vm)).To(Succeed())

					// Create snapshot1 CR with owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Reconcile the current snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Snapshot should not be processed.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, false)
					verifyNoVcVMSnapshot()
				})
			})

			When("disk promotion sync is enabled but not ready", func() {
				JustBeforeEach(func() {
					pkgcfg.SetContext(ctx, func(config *pkgcfg.Config) {
						config.Features.FastDeploy = true
					})
				})

				It("should create snapshot after disk promotion sync is ready", func() {
					// Set the VM's promote disks mode to not disabled and disk promotion sync condition to false.
					vm.Spec.PromoteDisksMode = vmopv1.VirtualMachinePromoteDisksModeOnline
					conditions.MarkFalse(vm, vmopv1.VirtualMachineDiskPromotionSynced, "", "")
					Expect(ctx.Client.Status().Update(ctx, vm)).To(Succeed())

					// Create a snapshot CR with owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Reconcile the snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Snapshot should not be processed.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, false)
					verifyNoVcVMSnapshot()

					// Update the VM's VirtualMachineDiskPromotionSynced condition to true.
					conditions.MarkTrue(vm, vmopv1.VirtualMachineDiskPromotionSynced)
					Expect(ctx.Client.Status().Update(ctx, vm)).To(Succeed())

					// Reconcile the snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Snapshot should be created.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, true)
				})
			})

			When("AllDisksArePVCs is enabled but disks are not registered", func() {
				JustBeforeEach(func() {
					pkgcfg.SetContext(ctx, func(config *pkgcfg.Config) {
						config.Features.AllDisksArePVCs = true
					})
				})

				It("should create snapshot after disk registration is ready", func() {
					// Set the VM's disk backfill condition to false.
					conditions.MarkFalse(vm, vmconfunmanagedvolsfil.Condition, "", "")
					Expect(ctx.Client.Status().Update(ctx, vm)).To(Succeed())

					// Create a snapshot CR with owner reference set to the VM.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					// Reconcile the snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Snapshot should not be processed (backfill not ready).
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, false)
					verifyNoVcVMSnapshot()

					// Update the VM's disk backfill condition to true.
					conditions.MarkTrue(vm, vmconfunmanagedvolsfil.Condition)
					Expect(ctx.Client.Status().Update(ctx, vm)).To(Succeed())

					// Reconcile the snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Snapshot should NOT be created (pending disk registration).
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, false)
					verifyNoVcVMSnapshot()

					// Snapshot should have WaitingForDiskRegistration condition set.
					updatedSnapshot := &vmopv1.VirtualMachineSnapshot{}
					Expect(ctx.Client.Get(ctx, ctrlclient.ObjectKey{
						Name:      snapshot1.Name,
						Namespace: snapshot1.Namespace,
					}, updatedSnapshot)).To(Succeed())
					Expect(conditions.GetReason(updatedSnapshot,
						vmopv1.VirtualMachineSnapshotCreatedCondition)).To(Equal(
						vmopv1.VirtualMachineSnapshotWaitingForDiskRegistrationReason))

					// Update the VM's disk registration condition to true.
					conditions.MarkTrue(vm, vmconfunmanagedvolsreg.Condition)
					Expect(ctx.Client.Status().Update(ctx, vm)).To(Succeed())

					// Reconcile the snapshot.
					Expect(vsphere.ReconcileCurrentSnapshot(vmCtx, ctx.Client, vcVM)).To(Succeed())

					// Snapshot should be created.
					verifyK8sVMSnapshot(snapshot1.Name, snapshot1.Namespace, true)
				})

				It("should set WaitingForDiskRegistration on all pending snapshots when registration is pending", func() {
					// Both backfill and registration conditions are not set (pending).
					conditions.MarkTrue(vm, vmconfunmanagedvolsfil.Condition)
					conditions.MarkFalse(vm, vmconfunmanagedvolsreg.Condition,
						"PendingRegistration", "CnsRegisterVolume objects are being processed")
					Expect(ctx.Client.Status().Update(ctx, vm)).To(Succeed())

					// Create two snapshot CRs.
					snapshot1 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-1", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot1, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

					snapshot2 = builder.DummyVirtualMachineSnapshot(vm.Namespace, "snapshot-2", vm.Name)
					Expect(controllerutil.SetOwnerReference(vm, snapshot2, ctx.Scheme)).To(Succeed())
					Expect(ctx.Client.Create(ctx, snapshot2)).To(Succeed())

					// ReconcileSnapshotWaitForCRVCondition mirrors what vmprovider_vm.go
					// calls when reconcileConfig exits early due to pending CRVs.
					Expect(vsphere.ReconcileSnapshotWaitForCRVCondition(vmCtx, ctx.Client)).To(Succeed())

					// Both snapshots should have WaitingForDiskRegistration condition.
					for _, name := range []string{snapshot1.Name, snapshot2.Name} {
						s := &vmopv1.VirtualMachineSnapshot{}
						Expect(ctx.Client.Get(ctx, ctrlclient.ObjectKey{
							Name: name, Namespace: vm.Namespace,
						}, s)).To(Succeed())
						Expect(conditions.GetReason(s,
							vmopv1.VirtualMachineSnapshotCreatedCondition)).To(Equal(
							vmopv1.VirtualMachineSnapshotWaitingForDiskRegistrationReason))
					}
				})

				// Selection must key off Spec.VMName, not the label, since the
				// label can be absent or stale independently of the VM it names.
				DescribeTable("selecting which snapshot to mark WaitingForDiskRegistration",
					func(labelBelongsToThisVM, specBelongsToThisVM, wantTouched bool) {
						resolve := func(belongsToThisVM bool) string {
							if belongsToThisVM {
								return vm.Name
							}
							return "other-vm"
						}

						conditions.MarkTrue(vm, vmconfunmanagedvolsfil.Condition)
						conditions.MarkFalse(vm, vmconfunmanagedvolsreg.Condition,
							"PendingRegistration", "CnsRegisterVolume objects are being processed")
						Expect(ctx.Client.Status().Update(ctx, vm)).To(Succeed())

						snapshot1 = builder.DummyVirtualMachineSnapshot(
							vm.Namespace, "snapshot-1", resolve(specBelongsToThisVM))
						metav1.SetMetaDataLabel(&snapshot1.ObjectMeta,
							vmopv1.VMNameForSnapshotLabel, resolve(labelBelongsToThisVM))
						Expect(ctx.Client.Create(ctx, snapshot1)).To(Succeed())

						Expect(vsphere.ReconcileSnapshotWaitForCRVCondition(vmCtx, ctx.Client)).To(Succeed())

						got := &vmopv1.VirtualMachineSnapshot{}
						Expect(ctx.Client.Get(ctx, ctrlclient.ObjectKey{
							Name: snapshot1.Name, Namespace: vm.Namespace,
						}, got)).To(Succeed())

						touched := conditions.GetReason(got, vmopv1.VirtualMachineSnapshotCreatedCondition) ==
							vmopv1.VirtualMachineSnapshotWaitingForDiskRegistrationReason
						Expect(touched).To(Equal(wantTouched))
					},
					Entry("label and Spec.VMName both belong to this VM", true, true, true),
					Entry("label and Spec.VMName both belong to a different VM", false, false, false),
					Entry("Spec.VMName belongs to this VM but the label is stale/wrong", false, true, true),
					Entry("label belongs to this VM but Spec.VMName belongs elsewhere", true, false, false),
				)
			})
		})
	})

// This Describe covers the NIC unit-number interplay with snapshot revert
// (spec 006 Q8, T031):
//
//   - restoreVMSpecFromSnapshot swaps the VM's spec AND annotations from the
//     backup YAML stored in the snapshot's ExtraConfig, so backfilled unit
//     numbers and the FeatureVersionNICUnitNumbers bit travel with the
//     snapshot. Reverting to a snapshot taken before the backfill drops the
//     bit, IsObjectUpgraded fails again, and the schema-upgrade backfill
//     re-records the observed slots.
//   - The imported-snapshot fallback (no backup YAML + ImportedSnapshotAnnotation)
//     synthesizes interfaces from the observed hardware; with
//     VMNetworkUnitNumbers enabled it now records the observed unit numbers
//     directly (I29) instead of leaving them nil for the mutator to invent.
var _ = Describe(
	"VirtualMachineSnapshot Unit Number Revert",
	Label(testlabels.VCSim),
	Label(testlabels.Snapshot), func() {

		var (
			parentCtx   context.Context
			initObjects []ctrlclient.Object
			testConfig  builder.VCSimTestConfig
			ctx         *builder.TestContextForVCSim
			vmProvider  providers.VirtualMachineProviderInterface
			nsInfo      builder.WorkloadNamespaceInfo

			vm      *vmopv1.VirtualMachine
			vmClass *vmopv1.VirtualMachineClass
			vcVM    *object.VirtualMachine

			vmSnapshot *vmopv1.VirtualMachineSnapshot

			reconcileUntilRevert func(
				testCtx *builder.TestContextForVCSim,
				provider providers.VirtualMachineProviderInterface,
				vm *vmopv1.VirtualMachine) error
		)

		BeforeEach(func() {
			parentCtx = pkgcfg.NewContextWithDefaultConfig()
			parentCtx = ctxop.WithContext(parentCtx)
			parentCtx = ovfcache.WithContext(parentCtx)
			parentCtx = cource.WithContext(parentCtx)
			pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
				config.AsyncCreateEnabled = false
				config.AsyncSignalEnabled = false
			})
			testConfig = builder.VCSimTestConfig{
				WithContentLibrary: true,
				WithVMSnapshots:    true,
				WithNetworkEnv:     builder.NetworkEnvNamed,
			}

			vmClass = builder.DummyVirtualMachineClassGenName()
			vm = builder.DummyBasicVirtualMachine("test-vm", "")
			vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
				Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
					{
						Name:    "eth0",
						Network: &vmopv1common.PartialObjectRef{Name: "VM Network"},
					},
				},
			}
		})

		JustBeforeEach(func() {
			ctx = suite.NewTestContextForVCSimWithParentContext(
				parentCtx, testConfig, initObjects...)
			pkgcfg.SetContext(ctx, func(config *pkgcfg.Config) {
				config.MaxDeployThreadsOnProvider = 1
			})
			vmProvider = vsphere.NewVSphereVMProviderFromClient(
				ctx, ctx.Client, ctx.Recorder)
			nsInfo = ctx.CreateWorkloadNamespace()

			vmClass.Namespace = nsInfo.Namespace
			Expect(ctx.Client.Create(ctx, vmClass)).To(Succeed())

			clusterVMI1 := &vmopv1.ClusterVirtualMachineImage{}
			Expect(ctx.Client.Get(
				ctx, ctrlclient.ObjectKey{Name: ctx.ContentLibraryItem1Name},
				clusterVMI1)).To(Succeed())

			vm.Namespace = nsInfo.Namespace
			vm.Spec.ClassName = vmClass.Name
			vm.Spec.ImageName = clusterVMI1.Name
			vm.Spec.Image.Kind = cvmiKind
			vm.Spec.Image.Name = clusterVMI1.Name
			vm.Spec.StorageClass = ctx.StorageClassName

			Expect(ctx.Client.Create(ctx, vm)).To(Succeed())
		})

		AfterEach(func() {
			vmClass = nil
			vm = nil
			vcVM = nil
			vmSnapshot = nil

			ctx.AfterEach()
			ctx = nil
			initObjects = nil
			vmProvider = nil
			nsInfo = builder.WorkloadNamespaceInfo{}
		})

		// createSnapshotOnVcVM creates a vSphere snapshot plus a ready snapshot
		// CR (marked Created/Ready so the snapshot workflow will not touch it),
		// optionally annotated as an imported snapshot.
		createSnapshotOnVcVM := func(imp bool) {
			GinkgoHelper()

			task, err := vcVM.CreateSnapshot(
				ctx, vmSnapshot.Name, "unit-number snapshot", false, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(task.Wait(ctx)).To(Succeed())

			conditions.MarkTrue(vmSnapshot, vmopv1.VirtualMachineSnapshotCreatedCondition)
			conditions.MarkTrue(vmSnapshot, vmopv1.VirtualMachineSnapshotReadyCondition)
			vmSnapshot.Namespace = nsInfo.Namespace
			if imp {
				vmSnapshot.Annotations[vmopv1.ImportedSnapshotAnnotation] = ""
			}
			Expect(ctx.Client.Create(ctx, vmSnapshot)).To(Succeed())

			// Snapshot should be owned by the VM resource.
			o := vmopv1.VirtualMachine{}
			Expect(ctx.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), &o)).To(Succeed())
			Expect(controllerutil.SetOwnerReference(&o, vmSnapshot, ctx.Scheme)).To(Succeed())
			Expect(ctx.Client.Update(ctx, vmSnapshot)).To(Succeed())
		}

		// observedNICUnitNumber returns the observed unit number of the VM's
		// first ethernet device.
		observedNICUnitNumber := func() int32 {
			GinkgoHelper()

			var moVM mo.VirtualMachine
			Expect(vcVM.Properties(ctx, vcVM.Reference(), []string{"config"}, &moVM)).To(Succeed())
			devList := object.VirtualDeviceList(moVM.Config.Hardware.Device)
			ethCards := devList.SelectByType((*vimtypes.VirtualEthernetCard)(nil))
			Expect(ethCards).ToNot(BeEmpty())
			unit := ethCards[0].GetVirtualDevice().UnitNumber
			Expect(unit).ToNot(BeNil())
			return *unit
		}

		// reconcileUntilRevert drives CreateOrUpdateVirtualMachine until the
		// snapshot revert runs (returning ErrSnapshotRevert), tolerating the
		// pre-revert no-requeue errors (backup/schema/object re-upgrades) that
		// exit the reconcile before step 9.
		reconcileUntilRevert = func(
			testCtx *builder.TestContextForVCSim,
			provider providers.VirtualMachineProviderInterface,
			vm *vmopv1.VirtualMachine) error {

			var err error
			for i := 0; i < 10; i++ {
				err = provider.CreateOrUpdateVirtualMachine(ctxop.WithContext(testCtx), vm)
				switch {
				case errors.Is(err, vsphere.ErrSnapshotRevert):
					return err
				case err == nil,
					errors.Is(err, vsphere.ErrUpgradeSchema),
					errors.Is(err, vsphere.ErrUpgradeObject),
					errors.Is(err, vsphere.ErrBackup):
					// Pre-revert no-requeues: keep driving.
				default:
					return err
				}
			}
			return err
		}

		Context("revert to a snapshot taken before the unit-number backfill", func() {

			BeforeEach(func() {
				vmSnapshot = builder.DummyVirtualMachineSnapshot(
					"", "test-pre-backfill-snap", vm.Name)
			})

			It("drops the feature-version bit on revert and the backfill re-records the slots", func() {
				By("creating and fully reconciling the VM with the feature disabled")
				Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
				vcVM = ctx.GetVMFromMoID(vm.Status.UniqueID)
				Expect(vcVM).ToNot(BeNil())

				// Feature version is base only: the VM is upgraded, but no NIC
				// unit numbers were backfilled while the flag was off.
				Expect(vm.Annotations).To(HaveKeyWithValue(
					pkgconst.UpgradedToFeatureVersionAnnotationKey, "1"))
				Expect(vm.Spec.Network.Interfaces[0].UnitNumber).To(BeNil())

				By("snapshotting the pre-backfill state")
				createSnapshotOnVcVM(false)

				By("enabling the feature and letting the schema upgrade backfill run")
				pkgcfg.SetContext(ctx, func(config *pkgcfg.Config) {
					config.Features.VMNetworkUnitNumbers = true
				})
				// The feature-version-only upgrade runs the backfill inline,
				// then the config reconcile no-requeues with ErrUpgradeObject
				// to re-reconcile the upgraded object on the next call.
				err := vmProvider.CreateOrUpdateVirtualMachine(ctxop.WithContext(ctx), vm)
				Expect(err == nil ||
					errors.Is(err, vsphere.ErrUpgradeSchema) ||
					errors.Is(err, vsphere.ErrUpgradeObject)).To(BeTrue())

				// The backfill recorded the observed slot and stamped the bit.
				Expect(vm.Spec.Network.Interfaces[0].UnitNumber).
					To(Equal(ptr.To(observedNICUnitNumber())))
				Expect(vm.Annotations).To(HaveKeyWithValue(
					pkgconst.UpgradedToFeatureVersionAnnotationKey, "17"))

				By("reverting to the pre-backfill snapshot")
				vm.Spec.CurrentSnapshotName = vmSnapshot.Name
				err = reconcileUntilRevert(ctx, vmProvider, vm)
				Expect(errors.Is(err, vsphere.ErrSnapshotRevert)).To(BeTrue())

				// The revert swapped in the snapshot's spec AND annotations:
				// no unit numbers, and the feature-version bit is gone (the
				// snapshot predates the feature).
				Expect(vm.Spec.Network.Interfaces[0].UnitNumber).To(BeNil())
				Expect(vm.Annotations).To(HaveKeyWithValue(
					pkgconst.UpgradedToFeatureVersionAnnotationKey, "1"))

				By("re-running the schema upgrade on the reverted VM")
				var moVM mo.VirtualMachine
				Expect(vcVM.Properties(ctx, vcVM.Reference(), []string{"config"}, &moVM)).To(Succeed())
				// ReconcileSchemaUpgrade no-requeues with ErrUpgradeObject
				// whenever it modified the object.
				Expect(upgradevm.ReconcileSchemaUpgrade(
					ctx, ctx.Client, vm, moVM)).To(
					MatchError(upgradevm.ErrUpgradeObject))

				// IsObjectUpgraded failed (feature version 1 vs target 17), so
				// the NIC backfill re-ran and recorded the observed slot again.
				Expect(vm.Spec.Network.Interfaces[0].UnitNumber).
					To(Equal(ptr.To(observedNICUnitNumber())))
				Expect(vm.Annotations).To(HaveKeyWithValue(
					pkgconst.UpgradedToFeatureVersionAnnotationKey, "17"))
			})
		})

		Context("revert to an imported snapshot with no backup YAML", func() {

			BeforeEach(func() {
				vmSnapshot = builder.DummyVirtualMachineSnapshot(
					"", "test-imported-snap", vm.Name)

				// Skip the backup YAML write (reconcileBackupState returns
				// early for CAPI-labeled VMs) so the snapshot's ExtraConfig
				// lacks VMResourceYAMLExtraConfigKey and the revert takes the
				// synthesized-spec path. The label is removed before the
				// revert so the revert flow itself is not skipped.
				vm.Labels[kubeutil.CAPVClusterRoleLabelKey] = ""
			})

			It("synthesizes interfaces that carry the observed unit numbers", func() {
				pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
					config.Features.VMNetworkUnitNumbers = true
				})

				By("creating the VM on vSphere (first call creates it, then fails schema upgrade)")
				var err error
				for i := 0; i < 10 && vm.Status.UniqueID == ""; i++ {
					err = vmProvider.CreateOrUpdateVirtualMachine(ctxop.WithContext(ctx), vm)
					if vm.Status.UniqueID != "" {
						break
					}
					Expect(err).To(HaveOccurred())
				}
				Expect(vm.Status.UniqueID).ToNot(BeEmpty())
				vcVM = ctx.GetVMFromMoID(vm.Status.UniqueID)
				Expect(vcVM).ToNot(BeNil())

				By("snapshotting the VM before any backup YAML exists")
				createSnapshotOnVcVM(true)

				By("reconciling the VM to a settled state (backup YAML now written, snapshot unaffected)")
				Expect(vm.Labels).ToNot(BeNil())
				delete(vm.Labels, kubeutil.CAPVClusterRoleLabelKey)
				Expect(ctx.Client.Update(ctx, vm)).To(Succeed())
				Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())

				By("reverting to the imported snapshot")
				vm.Spec.CurrentSnapshotName = vmSnapshot.Name
				err = reconcileUntilRevert(ctx, vmProvider, vm)
				Expect(errors.Is(err, vsphere.ErrSnapshotRevert)).To(BeTrue())

				// The synthesized spec approximates the VM from the snapshot's
				// hardware: one interface, eth0, carrying the observed unit
				// number directly (I29) — the spec matches the hardware as
				// soon as the revert lands, with no window for the mutator to
				// invent a slot. Annotations are synthesized empty.
				Expect(vm.Annotations).To(BeEmpty())
				Expect(vm.Spec.Network.Interfaces).To(HaveLen(1))
				Expect(vm.Spec.Network.Interfaces[0].Name).To(Equal("eth0"))
				Expect(vm.Spec.Network.Interfaces[0].UnitNumber).
					To(Equal(ptr.To(observedNICUnitNumber())))

				By("re-running the schema upgrade on the reverted VM")
				var moVM mo.VirtualMachine
				Expect(vcVM.Properties(ctx, vcVM.Reference(), []string{"config"}, &moVM)).To(Succeed())

				// The synthesized annotations are empty, so the first pass
				// stamps the build/schema annotations and no-requeues; the
				// second pass runs the feature backfills.
				Expect(upgradevm.ReconcileSchemaUpgrade(
					ctx, ctx.Client, vm, moVM)).To(
					MatchError(upgradevm.ErrUpgradeSchema))
				// The second pass runs the feature backfills and, having made
				// modifications, no-requeues with ErrUpgradeObject.
				Expect(upgradevm.ReconcileSchemaUpgrade(
					ctx, ctx.Client, vm, moVM)).To(
					MatchError(upgradevm.ErrUpgradeObject))

				// Spec wins: the synthesized unit number is already correct,
				// so the backfill leaves it untouched and only stamps the bit.
				Expect(vm.Spec.Network.Interfaces[0].UnitNumber).
					To(Equal(ptr.To(observedNICUnitNumber())))
				Expect(vm.Annotations).To(HaveKeyWithValue(
					pkgconst.UpgradedToFeatureVersionAnnotationKey, "17"))
			})
		})
	})
