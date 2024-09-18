// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package virtualmachine_test

import (
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	imgregv1a1 "github.com/vmware-tanzu/image-registry-operator-api/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha3"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgctx "github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	pkgclient "github.com/vmware-tanzu/vm-operator/pkg/util/vsphere/client"
	"github.com/vmware-tanzu/vm-operator/test/builder"
	"github.com/vmware-tanzu/vm-operator/test/testutil"
)

func cdromTests() {

	const (
		ns                = "test-ns"
		vmName            = "test-vm"
		vmiName           = "vmi-iso"
		cvmiName          = "cvmi-iso"
		vmiFileName       = "vmi-iso-file-name"
		cvmiFileName      = "cvmi-iso-file-name"
		vmiKind           = "VirtualMachineImage"
		cvmiKind          = "ClusterVirtualMachineImage"
		cdromName1        = "cdrom1"
		cdromName2        = "cdrom2"
		ideControllerKey  = 200
		sataControllerKey = 15000
		pciControllerKey  = 100
	)

	Context("UpdateCdromDeviceChanges", func() {

		var (
			result    []vimtypes.BaseVirtualDeviceConfigSpec
			resultErr error

			ctx        *builder.TestContextForVCSim
			vmCtx      pkgctx.VirtualMachineContext
			restClient *rest.Client
			k8sClient  ctrlclient.Client
			curDevices object.VirtualDeviceList
		)

		BeforeEach(func() {
			ctx = suite.NewTestContextForVCSim(builder.VCSimTestConfig{
				WithContentLibrary: true,
			})
			vcClient, err := pkgclient.NewClient(ctx, ctx.VCClientConfig)
			Expect(err).ToNot(HaveOccurred())
			restClient = vcClient.RestClient()

			vmCtx = pkgctx.VirtualMachineContext{
				Context: ctx,
				Logger:  suite.GetLogger(),
				VM:      builder.DummyBasicVirtualMachine(vmName, ns),
				MoVM:    mo.VirtualMachine{},
			}
			curDevices = object.VirtualDeviceList{}
		})

		Context("Happy Path (no errors occurred)", func() {

			BeforeEach(func() {
				// Create a fake K8s client with both namespace & cluster scope ISO type images and their content library item objects.
				k8sInitObjs := builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)
				k8sInitObjs = append(k8sInitObjs, builder.DummyImageAndItemObjectsForCdromBacking(cvmiName, ns, cvmiKind, cvmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)...)
				k8sClient = builder.NewFakeClient(k8sInitObjs...)
			})

			JustBeforeEach(func() {
				result, resultErr = virtualmachine.UpdateCdromDeviceChanges(vmCtx, restClient, k8sClient, curDevices)
				Expect(resultErr).ToNot(HaveOccurred())
			})

			When("VM.Spec.Cdrom is empty and VM has no existing CD-ROM device", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = nil
				})

				It("should return an empty list of device changes", func() {
					Expect(result).To(BeEmpty())
				})
			})

			When("VM.Spec.Cdrom adds a new CD-ROM device", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				When("VM has IDE controller slots available", func() {

					BeforeEach(func() {
						curDevices = object.VirtualDeviceList{
							&vimtypes.VirtualIDEController{
								VirtualController: vimtypes.VirtualController{
									VirtualDevice: vimtypes.VirtualDevice{
										Key: ideControllerKey,
									},
									Device: []int32{}, // can have two devices assigned
								},
							},
						}
					})

					It("should add the new CD-ROM device with IDE controller assigned", func() {
						Expect(result).To(HaveLen(1))
						verifyCdromDeviceConfigSpec(result[0], vimtypes.VirtualDeviceConfigSpecOperationAdd, true, true, ideControllerKey, 0, vmiFileName)
					})
				})

				When("VM has no IDE but SATA controller slots available", func() {

					BeforeEach(func() {
						curDevices = object.VirtualDeviceList{
							&vimtypes.VirtualSATAController{
								VirtualController: vimtypes.VirtualController{
									BusNumber: 0,
									VirtualDevice: vimtypes.VirtualDevice{
										Key: sataControllerKey,
									},
									Device: []int32{}, // can have four devices assigned
								},
							},
						}
					})

					It("should add the new CD-ROM device with SATA controller assigned", func() {
						Expect(result).To(HaveLen(1))
						verifyCdromDeviceConfigSpec(result[0], vimtypes.VirtualDeviceConfigSpecOperationAdd, true, true, sataControllerKey, 0, vmiFileName)
					})

				})

				When("VM has neither IDE nor SATA controller slot available", func() {

					When("PCI controller is present in the VM", func() {

						BeforeEach(func() {
							curDevices = object.VirtualDeviceList{
								&vimtypes.VirtualPCIController{
									VirtualController: vimtypes.VirtualController{
										BusNumber: 0,
										VirtualDevice: vimtypes.VirtualDevice{
											Key: pciControllerKey,
										},
										Device: []int32{}, // can have 32 devices assigned
									},
								},
							}
						})

						It("should add a new CD-ROM and AHCI controller with the CD-ROM assigned to the latter", func() {
							Expect(result).To(HaveLen(2))

							var cdromChange, ahciChange vimtypes.BaseVirtualDeviceConfigSpec
							for _, r := range result {
								if _, ok := r.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualCdrom); ok {
									cdromChange = r
								} else if _, ok := r.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualAHCIController); ok {
									ahciChange = r
								} else {
									Fail("unexpected device change")
								}
							}
							Expect(cdromChange).ToNot(BeNil())
							Expect(ahciChange).ToNot(BeNil())

							Expect(ahciChange.GetVirtualDeviceConfigSpec().Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationAdd))
							ahci := ahciChange.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualAHCIController)
							Expect(ahci.ControllerKey).To(Equal(int32(pciControllerKey)))

							verifyCdromDeviceConfigSpec(result[0], vimtypes.VirtualDeviceConfigSpecOperationAdd, true, true, ahci.Key, 0, vmiFileName)
						})
					})

					When("PCI controller is not present in the VM", func() {

						It("should add a new CD-ROM, AHCI controller, and PCI controller with expected controller assignment", func() {
							Expect(result).To(HaveLen(3))

							var cdromChange, ahciChange, pciChange vimtypes.BaseVirtualDeviceConfigSpec
							for _, r := range result {
								if _, ok := r.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualCdrom); ok {
									cdromChange = r
								} else if _, ok := r.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualAHCIController); ok {
									ahciChange = r
								} else if _, ok := r.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualPCIController); ok {
									pciChange = r
								} else {
									Fail("unexpected device change")
								}
							}
							Expect(cdromChange).ToNot(BeNil())
							Expect(ahciChange).ToNot(BeNil())
							Expect(pciChange).ToNot(BeNil())

							pci := pciChange.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualPCIController)
							ahci := ahciChange.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualAHCIController)
							Expect(ahci.ControllerKey).To(Equal(pci.Key))

							verifyCdromDeviceConfigSpec(cdromChange, vimtypes.VirtualDeviceConfigSpecOperationAdd, true, true, ahci.Key, 0, vmiFileName)
						})
					})
				})
			})

			When("VM.Spec.Cdrom adds multiple new CD-ROM devices with different connection state", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
						{
							Name: cdromName2,
							Image: vmopv1.VirtualMachineImageRef{
								Name: cvmiName,
								Kind: cvmiKind,
							},
							AllowGuestControl: ptr.To(false),
							Connected:         ptr.To(false),
						},
					}

					curDevices = object.VirtualDeviceList{
						&vimtypes.VirtualIDEController{
							VirtualController: vimtypes.VirtualController{
								VirtualDevice: vimtypes.VirtualDevice{
									Key: ideControllerKey,
								},
								Device: []int32{}, // can have two devices assigned
							},
						},
					}
				})

				It("should add new CD-ROM devices with default controller assigned and expected connection state", func() {
					Expect(result).To(HaveLen(2))

					var cdromChangeVmi, cdromChangeCvmi vimtypes.BaseVirtualDeviceConfigSpec
					var unitNumVmi, unitNumCvmi *int32
					for _, r := range result {
						if d, ok := r.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualCdrom); ok {
							if b, ok := d.Backing.(*vimtypes.VirtualCdromIsoBackingInfo); ok {
								if b.FileName == vmiFileName {
									cdromChangeVmi = r
									unitNumVmi = d.UnitNumber
								} else if b.FileName == cvmiFileName {
									cdromChangeCvmi = r
									unitNumCvmi = d.UnitNumber
								}
							}
						}
					}
					Expect(cdromChangeVmi).ToNot(BeNil())
					Expect(cdromChangeCvmi).ToNot(BeNil())
					Expect(unitNumVmi).ToNot(BeNil())
					Expect(unitNumCvmi).ToNot(BeNil())

					verifyCdromDeviceConfigSpec(cdromChangeVmi, vimtypes.VirtualDeviceConfigSpecOperationAdd, true, true, ideControllerKey, *unitNumVmi, vmiFileName)
					verifyCdromDeviceConfigSpec(cdromChangeCvmi, vimtypes.VirtualDeviceConfigSpecOperationAdd, false, false, ideControllerKey, *unitNumCvmi, cvmiFileName)
				})
			})

			When("VM.Spec.Cdrom removes existing CD-ROM devices", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
					curDevices = object.VirtualDeviceList{
						&vimtypes.VirtualCdrom{
							// Set all the expected fields to avoid this CD-ROM being updated.
							VirtualDevice: vimtypes.VirtualDevice{
								Key: 3000,
								Backing: &vimtypes.VirtualCdromIsoBackingInfo{
									VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
										FileName: vmiFileName,
									},
								},
								ControllerKey: 200,
							},
						},
						&vimtypes.VirtualCdrom{
							// CD-ROM to be removed.
							VirtualDevice: vimtypes.VirtualDevice{
								Key: 3001,
							},
						},
					}
				})

				It("should remove the specified CD-ROM device from VM", func() {
					Expect(result).To(HaveLen(1))
					Expect(result[0].GetVirtualDeviceConfigSpec().Operation).To(Equal(vimtypes.VirtualDeviceConfigSpecOperationRemove))
					cdrom, ok := result[0].GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualCdrom)
					Expect(ok).To(BeTrue())
					Expect(cdrom.Key).To(Equal(int32(3001)))
				})
			})

			When("VM.Spec.Cdrom updates existing CD-ROM devices connection", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName2,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							// Disconnect the CD-ROM device and disallow guest control.
							AllowGuestControl: ptr.To(false),
							Connected:         ptr.To(false),
						},
					}
					curDevices = object.VirtualDeviceList{
						&vimtypes.VirtualCdrom{
							// CD-ROM to be updated (currently connected and allowed guest control).
							VirtualDevice: vimtypes.VirtualDevice{
								Key: 3000,
								Backing: &vimtypes.VirtualCdromIsoBackingInfo{
									VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
										FileName: vmiFileName,
									},
								},
								Connectable: &vimtypes.VirtualDeviceConnectInfo{
									AllowGuestControl: true,
									StartConnected:    true,
									Connected:         true,
								},
								ControllerKey: 200,
								UnitNumber:    new(int32),
							},
						},
					}
				})

				It("should update the existing CD-ROM device as expected", func() {
					Expect(result).To(HaveLen(1))
					verifyCdromDeviceConfigSpec(result[0], vimtypes.VirtualDeviceConfigSpecOperationEdit, false, false, 200, 0, vmiFileName)
				})
			})
		})

		Context("Error Path", func() {

			var k8sInitObjs []ctrlclient.Object

			JustBeforeEach(func() {
				k8sClient = builder.NewFakeClient(k8sInitObjs...)

				result, resultErr = virtualmachine.UpdateCdromDeviceChanges(vmCtx, restClient, k8sClient, curDevices)
				Expect(resultErr).To(HaveOccurred())
			})

			When("VM.Spec.Cdrom specifics a VMI cannot be found", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: "non-existent-vmi",
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("not found"))
				})
			})

			When("VM.Spec.Cdrom specifies a VMI without provider ref", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, false, false, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("provider ref is nil for VirtualMachineImage"))
				})
			})

			When("VM.Spec.Cdrom specifies a VMI with provider ref object not found", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, true, false, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("not found"))
				})
			})

			When("VM.Spec.Cdrom specifics a CVMI cannot be found", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: "non-existent-cvmi",
								Kind: cvmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("not found"))
				})
			})

			When("VM.Spec.Cdrom specifies a CVMI without provider ref", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(cvmiName, ns, cvmiKind, cvmiFileName, ctx.ContentLibraryIsoItemID, false, false, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: cvmiName,
								Kind: cvmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("provider ref is nil for ClusterVirtualMachineImage"))
				})
			})

			When("VM.Spec.Cdrom specifies a CVMI with provider ref object not found", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(cvmiName, ns, cvmiKind, cvmiFileName, ctx.ContentLibraryIsoItemID, true, false, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: cvmiName,
								Kind: cvmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("not found"))
				})
			})

			When("VM.Spec.Cdrom specifies an invalid image kind", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: "invalid-kind",
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("unsupported image kind: \"invalid-kind\""))
				})
			})

			When("VM.Spec.Cdrom specifies a non-ISO type image", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeOvf)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("expected ISO type image, got OVF"))
				})
			})

			When("VM.Spec.Cdrom specifies an image file with empty storage URI", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, "", ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("no storage URI found in the content library item status"))
				})
			})

			When("VM.Spec.Cdrom specifies a connected CD-ROM but cannot find the content library item to sync", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, "invalid-item-uuid", true, true, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("error getting library item invalid-item-uuid to sync"))
				})
			})

			When("VM.Spec.Cdrom specifies a connected CD-ROM but fails to sync the content library item", func() {

				var (
					libMgr *library.Manager
					tempCL library.Library
					itemID string
				)

				BeforeEach(func() {
					// Create a new local library without publication to force a sync error.
					libMgr = library.NewManager(restClient)
					tempCL = library.Library{
						Name: "temp-library",
						Type: "LOCAL",
						Storage: []library.StorageBacking{
							{
								DatastoreID: ctx.Datastore.Reference().Value,
								Type:        "DATASTORE",
							},
						},
					}
					clID, err := libMgr.CreateLibrary(ctx, tempCL)
					Expect(err).ToNot(HaveOccurred())
					Expect(clID).ToNot(BeEmpty())
					tempCL.ID = clID

					// Add a new item to the library to use as the backing for the CD-ROM device.
					libItem := library.Item{
						Name:      "temp-item",
						Type:      library.ItemTypeISO,
						LibraryID: clID,
					}
					itemID := builder.CreateContentLibraryItem(
						ctx,
						libMgr,
						libItem,
						path.Join(
							testutil.GetRootDirOrDie(),
							"test", "builder", "testdata",
							"images", "ttylinux-pc_i486-16.1.iso"),
					)
					Expect(itemID).ToNot(BeEmpty())

					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, itemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				AfterEach(func() {
					if tempCL.ID != "" {
						Expect(libMgr.DeleteLibrary(ctx, &tempCL)).To(Succeed())
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("error syncing library item " + itemID))
				})
			})

			When("VM.Spec.Cdrom specifies an image file backed by multiple CD-ROM devices", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}

					curDevices = object.VirtualDeviceList{
						&vimtypes.VirtualCdrom{
							VirtualDevice: vimtypes.VirtualDevice{
								Key: 3000,
								Backing: &vimtypes.VirtualCdromIsoBackingInfo{
									VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
										FileName: vmiFileName,
									},
								},
							},
						},
						&vimtypes.VirtualCdrom{
							VirtualDevice: vimtypes.VirtualDevice{
								Key: 3001,
								Backing: &vimtypes.VirtualCdromIsoBackingInfo{
									VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
										FileName: vmiFileName,
									},
								},
							},
						},
					}
				})

				It("should return an error", func() {
					Expect(resultErr.Error()).To(ContainSubstring("found multiple CD-ROMs with same backing file name"))
				})
			})
		})
	})

	Context("UpdateConfigSpecCdromDeviceConnection", func() {

		var (
			ctx        *builder.TestContextForVCSim
			vmCtx      pkgctx.VirtualMachineContext
			restClient *rest.Client
			k8sClient  ctrlclient.Client
			configInfo *vimtypes.VirtualMachineConfigInfo
			configSpec *vimtypes.VirtualMachineConfigSpec

			updateErr error
		)

		BeforeEach(func() {
			ctx = suite.NewTestContextForVCSim(builder.VCSimTestConfig{
				WithContentLibrary: true,
			})
			vcClient, err := pkgclient.NewClient(ctx, ctx.VCClientConfig)
			Expect(err).ToNot(HaveOccurred())
			restClient = vcClient.RestClient()

			vmCtx = pkgctx.VirtualMachineContext{
				Context: pkgcfg.NewContext(),
				Logger:  suite.GetLogger(),
				VM:      builder.DummyBasicVirtualMachine(vmName, ns),
				MoVM:    mo.VirtualMachine{},
			}
			configInfo = &vimtypes.VirtualMachineConfigInfo{}
			configSpec = &vimtypes.VirtualMachineConfigSpec{}
		})

		JustBeforeEach(func() {
			updateErr = virtualmachine.UpdateConfigSpecCdromDeviceConnection(vmCtx, restClient, k8sClient, configInfo, configSpec)
		})

		Context("Happy Path (no error occurs)", func() {

			BeforeEach(func() {
				// Create a fake K8s client with both namespace & cluster scope ISO type images and their content library item objects.
				k8sInitObjs := builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)
				k8sInitObjs = append(k8sInitObjs, builder.DummyImageAndItemObjectsForCdromBacking(cvmiName, ns, cvmiKind, cvmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)...)
				k8sClient = builder.NewFakeClient(k8sInitObjs...)
			})

			When("VM.Spec.Cdrom has no changes", func() {

				BeforeEach(func() {
					// VM has a CD-ROM device with the same backing file name as the image ref in VM.Spec.Cdrom.
					configInfo = &vimtypes.VirtualMachineConfigInfo{
						Hardware: vimtypes.VirtualHardware{
							Device: []vimtypes.BaseVirtualDevice{
								&vimtypes.VirtualCdrom{
									VirtualDevice: vimtypes.VirtualDevice{
										Key: 3000,
										Backing: &vimtypes.VirtualCdromIsoBackingInfo{
											VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
												FileName: vmiFileName,
											},
										},
										Connectable: &vimtypes.VirtualDeviceConnectInfo{
											StartConnected:    true,
											Connected:         true,
											AllowGuestControl: true,
										},
									},
								},
							},
						},
					}

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return no device changes", func() {
					Expect(updateErr).ToNot(HaveOccurred())
					Expect(configSpec.DeviceChange).To(BeEmpty())
				})
			})

			When("VM.Spec.Cdrom updates existing CD-ROM connection state", func() {

				BeforeEach(func() {
					// VM has a connected CD-ROM device with the same backing file name as the image ref in VM.Spec.Cdrom.
					configInfo = &vimtypes.VirtualMachineConfigInfo{
						Hardware: vimtypes.VirtualHardware{
							Device: []vimtypes.BaseVirtualDevice{
								&vimtypes.VirtualCdrom{
									VirtualDevice: vimtypes.VirtualDevice{
										Key: 3000,
										Backing: &vimtypes.VirtualCdromIsoBackingInfo{
											VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
												FileName: vmiFileName,
											},
										},
										Connectable: &vimtypes.VirtualDeviceConnectInfo{
											StartConnected:    true,
											Connected:         true,
											AllowGuestControl: true,
										},
										ControllerKey: ideControllerKey,
										UnitNumber:    new(int32),
									},
								},
							},
						},
					}

					// Update the CD-ROM device to be disconnected and disallow guest control.
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(false),
							Connected:         ptr.To(false),
						},
					}
				})

				It("should return a device change to update the CD-ROM device with expected connection state", func() {
					Expect(updateErr).ToNot(HaveOccurred())
					Expect(configSpec.DeviceChange).To(HaveLen(1))

					verifyCdromDeviceConfigSpec(configSpec.DeviceChange[0], vimtypes.VirtualDeviceConfigSpecOperationEdit, false, false, ideControllerKey, 0, vmiFileName)
				})
			})
		})

		Context("Error Path", func() {

			var k8sInitObjs []ctrlclient.Object

			JustBeforeEach(func() {
				k8sClient = builder.NewFakeClient(k8sInitObjs...)

				updateErr = virtualmachine.UpdateConfigSpecCdromDeviceConnection(vmCtx, restClient, k8sClient, configInfo, configSpec)
			})

			// These test cases are similar to those in UpdateCdromDeviceChanges.
			// Instead of checking specific error messages, we verify the general error messages returned by UpdateConfigSpecCdromDeviceConnection.
			When("error getting backing file name by image ref", func() {

				BeforeEach(func() {
					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: "non-existent-vmi",
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(updateErr.Error()).To(ContainSubstring("error getting backing file name by image ref"))
				})
			})

			When("error getting CD-ROM device by backing file name", func() {

				BeforeEach(func() {
					// Use multiple CD-ROM devices with the same backing file name to force an error.
					configInfo = &vimtypes.VirtualMachineConfigInfo{
						Hardware: vimtypes.VirtualHardware{
							Device: []vimtypes.BaseVirtualDevice{
								&vimtypes.VirtualCdrom{
									VirtualDevice: vimtypes.VirtualDevice{
										Key: 3000,
										Backing: &vimtypes.VirtualCdromIsoBackingInfo{
											VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
												FileName: vmiFileName,
											},
										},
									},
								},
								&vimtypes.VirtualCdrom{
									VirtualDevice: vimtypes.VirtualDevice{
										Key: 3001,
										Backing: &vimtypes.VirtualCdromIsoBackingInfo{
											VirtualDeviceFileBackingInfo: vimtypes.VirtualDeviceFileBackingInfo{
												FileName: vmiFileName,
											},
										},
									},
								},
							},
						},
					}

					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(updateErr.Error()).To(ContainSubstring("error getting CD-ROM device by backing file name"))
				})
			})

			When("Updating a CD-ROM device that doesn't exist in the VM", func() {

				BeforeEach(func() {
					k8sInitObjs = builder.DummyImageAndItemObjectsForCdromBacking(vmiName, ns, vmiKind, vmiFileName, ctx.ContentLibraryIsoItemID, true, true, imgregv1a1.ContentLibraryItemTypeIso)

					vmCtx.VM.Spec.Cdrom = []vmopv1.VirtualMachineCdromSpec{
						{
							Name: cdromName1,
							Image: vmopv1.VirtualMachineImageRef{
								Name: vmiName,
								Kind: vmiKind,
							},
							AllowGuestControl: ptr.To(true),
							Connected:         ptr.To(true),
						},
					}
				})

				It("should return an error", func() {
					Expect(updateErr.Error()).To(ContainSubstring("no CD-ROM is found for image ref"))
				})
			})
		})
	})
}

// verifyCdromDeviceConfigSpec is a helper function to verify the given device
// config spec is a CD-ROM device change with all the expected properties set.
func verifyCdromDeviceConfigSpec(
	deviceConfigSpec vimtypes.BaseVirtualDeviceConfigSpec,
	op vimtypes.VirtualDeviceConfigSpecOperation,
	connected, allowGuestControl bool,
	controllerKey int32,
	unitNumber int32,
	backingFileName string) {

	Expect(deviceConfigSpec.GetVirtualDeviceConfigSpec().Operation).To(Equal(op))

	Expect(deviceConfigSpec.GetVirtualDeviceConfigSpec().Device).To(BeAssignableToTypeOf(&vimtypes.VirtualCdrom{}))
	cdrom := deviceConfigSpec.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualCdrom)
	Expect(cdrom.Connectable).ToNot(BeNil())
	Expect(cdrom.Connectable.StartConnected).To(Equal(connected))
	Expect(cdrom.Connectable.Connected).To(Equal(connected))
	Expect(cdrom.Connectable.AllowGuestControl).To(Equal(allowGuestControl))
	Expect(cdrom.ControllerKey).To(Equal(controllerKey))
	Expect(cdrom.UnitNumber).ToNot(BeNil())
	Expect(*cdrom.UnitNumber).To(Equal(unitNumber))

	Expect(cdrom.Backing).To(BeAssignableToTypeOf(&vimtypes.VirtualCdromIsoBackingInfo{}))
	Expect(cdrom.Backing.(*vimtypes.VirtualCdromIsoBackingInfo).FileName).To(Equal(backingFileName))
}