// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vsphere_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/uuid"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vapi/library"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/conditions"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgerr "github.com/vmware-tanzu/vm-operator/pkg/errors"
	"github.com/vmware-tanzu/vm-operator/pkg/providers"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func vmISOTests() {
	var (
		parentCtx   context.Context
		initObjects []client.Object
		testConfig  builder.VCSimTestConfig
		ctx         *builder.TestContextForVCSim
		vmProvider  providers.VirtualMachineProviderInterface
		nsInfo      builder.WorkloadNamespaceInfo

		vm      *vmopv1.VirtualMachine
		vmClass *vmopv1.VirtualMachineClass
	)

	BeforeEach(func() {
		parentCtx = newVMTestParentContext()
		pkgcfg.SetContext(parentCtx, func(c *pkgcfg.Config) { c.Features.CDROMPlacement = true })
		testConfig = newVMTestConfig()
	})

	JustBeforeEach(func() {
		ctx, vmProvider, nsInfo = newVMTestContext(
			parentCtx, testConfig, initObjects...)
	})

	AfterEach(func() {
		vmTestAfterEach(ctx, vm)

		vmClass = nil
		vm = nil

		ctx = nil
		initObjects = nil
		vmProvider = nil
		nsInfo = builder.WorkloadNamespaceInfo{}
	})

	BeforeEach(func() {
		vmClass, vm = newVMTestObjects("test-vm-iso")
	})

	JustBeforeEach(func() {
		vmClass.Namespace = nsInfo.Namespace
		Expect(ctx.Client.Create(ctx, vmClass)).To(Succeed())

		// Add required objects to get CD-ROM backing file name.
		cvmiName := "vmi-iso"
		objs := builder.DummyImageAndItemObjectsForCdromBacking(cvmiName, "", cvmiKind, "test-file.iso", ctx.ContentLibraryIsoItemID, true, true, resource.MustParse("100Mi"), true, true, "ISO")
		for _, obj := range objs {
			Expect(ctx.Client.Create(ctx, obj)).To(Succeed())
		}

		vm.Namespace = nsInfo.Namespace
		vm.Spec.ClassName = vmClass.Name
		vm.Spec.ImageName = cvmiName
		vm.Spec.Image.Kind = cvmiKind
		vm.Spec.Image.Name = cvmiName
		vm.Spec.StorageClass = ctx.StorageClassName
		vm.Spec.Hardware = &vmopv1.VirtualMachineHardwareSpec{
			Cdrom: []vmopv1.VirtualMachineCdromSpec{{
				Name: "cdrom0",
				Image: vmopv1.VirtualMachineImageRef{
					Name: cvmiName,
					Kind: cvmiKind,
				},
			}},
		}

		Expect(ctx.Client.Create(ctx, vm)).To(Succeed())
	})

	DescribeTable("includes media in placement and initial creation",
		func(fastDeploy, sharedDisks, enabled, pinnedZone bool) {
			pkgcfg.SetContext(ctx, func(c *pkgcfg.Config) {
				c.Features.FastDeploy = fastDeploy
				c.Features.VMSharedDisks = sharedDisks
				c.Features.CDROMPlacement = enabled
			})
			ctx.MarkImageCacheReady(ctx.ContentLibraryIsoItemCache)
			if pinnedZone {
				pinVMToFirstZone(ctx, vm)
			}
			cd := &vm.Spec.Hardware.Cdrom[0]
			cd.Connected = ptr.To(true)
			if sharedDisks {
				cd.ControllerType = vmopv1.VirtualControllerTypeIDE
				cd.ControllerBusNumber = ptr.To[int32](0)
				cd.UnitNumber = ptr.To[int32](1)
				vm.Spec.Hardware.IDEControllers = []vmopv1.IDEControllerSpec{{BusNumber: 0}}
			}
			Expect(ctx.Client.Update(ctx, vm)).To(Succeed())

			vcClient, err := vmProvider.VSphereClient(ctx)
			Expect(err).NotTo(HaveOccurred())
			vimClient := vcClient.VimClient()
			original := vimClient.RoundTripper
			capture := &cdromCreateRoundTripper{RoundTripper: original}
			vimClient.RoundTripper = capture
			defer func() { vimClient.RoundTripper = original }()

			Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
			Expect(capture.placements).NotTo(BeEmpty())
			Expect(capture.creates).To(HaveLen(1))
			for _, spec := range append(capture.placements, capture.creates[0].Config) {
				devices := object.VirtualDeviceList{}
				for _, change := range spec.DeviceChange {
					devices = append(devices, change.GetVirtualDeviceConfigSpec().Device)
				}
				media := devices.SelectByType((*vimtypes.VirtualCdrom)(nil))
				if !enabled {
					Expect(media).To(BeEmpty())
					continue
				}
				Expect(media).To(HaveLen(1))
				cdrom := media[0].(*vimtypes.VirtualCdrom)
				Expect(cdrom.Backing.(*vimtypes.VirtualCdromIsoBackingInfo).FileName).To(Equal("test-file.iso"))
				Expect(cdrom.Connectable.StartConnected).To(BeTrue())
				Expect(cdrom.Connectable.Connected).To(BeFalse())
				Expect(devices.FindByKey(cdrom.ControllerKey)).NotTo(BeNil())
				if sharedDisks {
					Expect(cdrom.UnitNumber).To(Equal(ptr.To[int32](1)))
				}
			}
			if enabled {
				Expect(capture.creates[0].Host).NotTo(BeNil())
				Expect(capture.cdromAddsAfterCreate).To(BeZero())
			}
		},
		Entry("fast deploy with automatic placement", true, false, true, false),
		Entry("legacy create with automatic placement", false, false, true, false),
		Entry("fast deploy with explicit placement", true, true, true, false),
		Entry("legacy create with explicit placement", false, true, true, false),
		Entry("pinned zone still requests ISO placement", false, false, true, true),
		Entry("rollback retains post-create attachment", true, false, false, false),
	)

	It("creates an OVF-backed VM with the requested ISO already attached", func() {
		ctx.MarkImageCacheReady(ctx.ContentLibraryItem1Cache)
		vm.Spec.ImageName = ctx.ContentLibraryItem1Name
		vm.Spec.Image.Name = ctx.ContentLibraryItem1Name
		Expect(ctx.Client.Update(ctx, vm)).To(Succeed())
		vcClient, err := vmProvider.VSphereClient(ctx)
		Expect(err).NotTo(HaveOccurred())
		vimClient := vcClient.VimClient()
		original := vimClient.RoundTripper
		capture := &cdromCreateRoundTripper{RoundTripper: original}
		vimClient.RoundTripper = capture
		defer func() { vimClient.RoundTripper = original }()
		Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
		Expect(capture.creates).To(HaveLen(1))
		var media []vimtypes.BaseVirtualDevice
		for _, change := range capture.creates[0].Config.DeviceChange {
			if cdrom, ok := change.GetVirtualDeviceConfigSpec().Device.(*vimtypes.VirtualCdrom); ok {
				media = append(media, cdrom)
			}
		}
		Expect(media).To(HaveLen(1))
		Expect(media[0].GetVirtualDevice().Backing.(*vimtypes.VirtualCdromIsoBackingInfo).FileName).To(Equal("test-file.iso"))
		Expect(capture.cdromAddsAfterCreate).To(BeZero())
	})

	It("does not create a VM when no host can access the ISO", func() {
		ctx.MarkImageCacheReady(ctx.ContentLibraryIsoItemCache)
		pinVMToFirstZone(ctx, vm)
		Expect(ctx.Client.Update(ctx, vm)).To(Succeed())
		ctx.SimulatorService().AddFaultRule(&simulator.FaultInjectionRule{
			MethodName: "PlaceVmsXCluster", ObjectType: "Folder", ObjectName: "*",
			FaultType: simulator.FaultTypeCustom, Fault: &vimtypes.NoCompatibleHost{},
			Probability: 1.0, Enabled: true,
		})
		defer ctx.SimulatorService().ClearFaultRules()
		vcClient, err := vmProvider.VSphereClient(ctx)
		Expect(err).NotTo(HaveOccurred())
		vimClient := vcClient.VimClient()
		original := vimClient.RoundTripper
		capture := &cdromCreateRoundTripper{RoundTripper: original}
		vimClient.RoundTripper = capture
		defer func() { vimClient.RoundTripper = original }()
		Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(MatchError(ContainSubstring("PlaceVmsXCluster failed")))
		Expect(capture.placements).To(HaveLen(1))
		Expect(capture.creates).To(BeEmpty())
		Expect(vm.Status.UniqueID).To(BeEmpty())
		Expect(conditions.IsFalse(vm, vmopv1.VirtualMachineConditionPlacementReady)).To(BeTrue())
	})

	Context("return config", func() {
		JustBeforeEach(func() {
			// Nothing in a provider test plays the part of the image cache
			// controller, so report the ISO item's files as cached. The specs
			// below that exercise the cache-not-ready paths build their own
			// VirtualMachineImageCache instead.
			ctx.MarkImageCacheReady(ctx.ContentLibraryIsoItemCache)

			Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
		})

		It("return config.files", func() {
			vmPathName := "config.files.vmPathName"
			props, err := vmProvider.GetVirtualMachineProperties(ctx, vm, []string{vmPathName})
			Expect(err).NotTo(HaveOccurred())
			var path object.DatastorePath
			path.FromString(props[vmPathName].(string))
			Expect(path.Datastore).NotTo(BeEmpty())
		})
	})

	When("Fast Deploy is enabled", func() {

		var (
			vmic vmopv1.VirtualMachineImageCache
		)

		BeforeEach(func() {
			testConfig.WithContentLibrary = true
			pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
				config.Features.FastDeploy = true
			})
			// Ensure the VM has a UID to verify the VM directory path
			// is different from the VM's Kubernetes UID on vSAN.
			vm.UID = "test-vm-iso-uid"
		})

		JustBeforeEach(func() {
			vmicName := pkgutil.VMIName(ctx.ContentLibraryIsoItemID)
			vmic = vmopv1.VirtualMachineImageCache{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pkgcfg.FromContext(ctx).PodNamespace,
					Name:      vmicName,
				},
			}
			Expect(ctx.Client.Create(ctx, &vmic)).To(Succeed())
		})

		assertVMICNotReady := func(err error, msg, name, dcID, dsID string) {
			var e pkgerr.VMICacheNotReadyError
			ExpectWithOffset(1, errors.As(err, &e)).To(BeTrue())
			ExpectWithOffset(1, e.Message).To(Equal(msg))
			ExpectWithOffset(1, e.Name).To(Equal(name))
			ExpectWithOffset(1, e.DatacenterID).To(Equal(dcID))
			ExpectWithOffset(1, e.DatastoreID).To(Equal(dsID))
		}

		When("cache files are not ready", func() {
			It("should fail", func() {
				err := createOrUpdateVM(ctx, vmProvider, vm)
				Expect(err).To(HaveOccurred())
				assertVMICNotReady(
					err,
					"cached files not ready",
					vmic.Name,
					ctx.Datacenter.Reference().Value,
					ctx.Datastore.Reference().Value)
			})
		})

		When("cache files are ready", func() {
			JustBeforeEach(func() {
				// Simulate vSAN datastore with TopLevelDirectoryCreateSupported disabled.
				sctx := ctx.SimulatorContext()
				for _, dsEnt := range sctx.Map.All("Datastore") {
					sctx.WithLock(
						dsEnt.Reference(),
						func() {
							ds := sctx.Map.Get(dsEnt.Reference()).(*simulator.Datastore)
							ds.Capability.TopLevelDirectoryCreateSupported = ptr.To(false)
							ds.Summary.Type = string(vimtypes.HostFileSystemVolumeFileSystemTypeVsan)
						})
				}

				// Set required fields for ISO VM creation in the VMIC.
				conditions.MarkTrue(
					&vmic,
					vmopv1.VirtualMachineImageCacheConditionFilesReady)
				vmic.Status.Locations = []vmopv1.VirtualMachineImageCacheLocationStatus{
					{
						DatacenterID: ctx.Datacenter.Reference().Value,
						DatastoreID:  ctx.Datastore.Reference().Value,
						ProfileID:    ctx.StorageProfileID,
						Files:        []vmopv1.VirtualMachineImageCacheFileStatus{},
						Conditions: []metav1.Condition{
							{
								Type:   vmopv1.ReadyConditionType,
								Status: metav1.ConditionTrue,
							},
						},
					},
				}
				Expect(ctx.Client.Status().Update(ctx, &vmic)).To(Succeed())

				libMgr := library.NewManager(ctx.RestClient)
				Expect(libMgr.SyncLibraryItem(ctx, &library.Item{ID: ctx.ContentLibraryIsoItemID}, true)).To(Succeed())
			})

			It("should successfully create the ISO VM in a different UUID-based directory", func() {
				vcVM, err := createOrUpdateAndGetVcVM(ctx, vmProvider, vm)
				Expect(err).NotTo(HaveOccurred())

				var moVM mo.VirtualMachine
				Expect(vcVM.Properties(ctx, vcVM.Reference(), nil, &moVM)).To(Succeed())

				var p object.DatastorePath
				p.FromString(moVM.Config.Files.VmPathName)
				Expect(p.Datastore).NotTo(BeEmpty())

				// The VM path should be something like: <uuid>/test-vm-iso.vmx
				// When TopLevelDirectoryCreateSupported is false,
				// DatastoreNamespaceManager.CreateDirectory creates a
				// new UUID-based directory that is different from the
				// VM's Kubernetes UID.
				pathParts := strings.Split(p.Path, "/")
				Expect(pathParts).To(HaveLen(2))
				_, err = uuid.Parse(pathParts[0])
				Expect(err).NotTo(HaveOccurred(), "expected directory to be a UUID, got: %s", pathParts[0])
				Expect(pathParts[0]).NotTo(Equal(string(vm.UID)), "expected directory to be different from VM's K8s UID")
			})
		})
	})
}

// cdromCreateRoundTripper records the requests sent to vCenter so tests can
// distinguish initial configuration from later reconciliation.
type cdromCreateRoundTripper struct {
	soap.RoundTripper
	placements           []vimtypes.VirtualMachineConfigSpec
	creates              []vimtypes.CreateVM_Task
	cdromAddsAfterCreate int
}

func (r *cdromCreateRoundTripper) RoundTrip(ctx context.Context, req, res soap.HasFault) error {
	switch body := req.(type) {
	case *methods.PlaceVmsXClusterBody:
		for _, placement := range body.Req.PlacementSpec.VmPlacementSpecs {
			requestCopy, err := vimtypes.DeepCopy(placement.ConfigSpec)
			if err != nil {
				return err
			}
			r.placements = append(r.placements, requestCopy)
		}
	case *methods.PlaceVmBody:
		if body.Req.PlacementSpec.ConfigSpec != nil {
			requestCopy, err := vimtypes.DeepCopy(*body.Req.PlacementSpec.ConfigSpec)
			if err != nil {
				return err
			}
			r.placements = append(r.placements, requestCopy)
		}
	case *methods.CreateVM_TaskBody:
		requestCopy, err := vimtypes.DeepCopy(*body.Req)
		if err != nil {
			return err
		}
		r.creates = append(r.creates, requestCopy)
	case *methods.ReconfigVM_TaskBody:
		for _, change := range body.Req.Spec.DeviceChange {
			dc := change.GetVirtualDeviceConfigSpec()
			if _, ok := dc.Device.(*vimtypes.VirtualCdrom); ok && dc.Operation == vimtypes.VirtualDeviceConfigSpecOperationAdd {
				r.cdromAddsAfterCreate++
			}
		}
	}
	return r.RoundTripper.RoundTrip(ctx, req, res)
}
