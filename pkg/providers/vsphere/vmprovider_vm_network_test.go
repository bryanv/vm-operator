// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vsphere_test

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	netopv1alpha1 "github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	vpcv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	ncpv1alpha1 "github.com/vmware-tanzu/vm-operator/external/ncp/api/v1alpha1"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/pkg/conditions"
	pkgcfg "github.com/vmware-tanzu/vm-operator/pkg/config"
	pkgconst "github.com/vmware-tanzu/vm-operator/pkg/constants"
	pkgerr "github.com/vmware-tanzu/vm-operator/pkg/errors"
	"github.com/vmware-tanzu/vm-operator/pkg/providers"
	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/network"
	"github.com/vmware-tanzu/vm-operator/pkg/util"
	netsetutil "github.com/vmware-tanzu/vm-operator/pkg/util/kube/networksettings"
	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	vmopv1util "github.com/vmware-tanzu/vm-operator/pkg/util/vmopv1"
	"github.com/vmware-tanzu/vm-operator/test/builder"
)

func vmNetworkTests() {

	const (
		networkName0   = "my-network-0"
		interfaceName0 = "eth0"
		networkName1   = "my-network-1"
		interfaceName1 = "eth1"
		networkName2   = "my-network-2"

		bsCloudInit = "cloudInit"
		bsSysprep   = "sysPrep"
		bsLinuxPrep = "linuxPrep"
	)

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
		pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
			config.MaxDeployThreadsOnProvider = 1
		})
		testConfig = newVMTestConfig()

		vmClass, vm = newVMTestObjects("test-vm")

		// Networking is the behavior under test here.
		vm.Spec.Network.Disabled = false
	})

	JustBeforeEach(func() {
		ctx, vmProvider, nsInfo = setupVMTest(
			parentCtx, testConfig, vmClass, vm, initObjects...)

		pinVMToFirstZone(ctx, vm)
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

	When("spec.network.disabled is true", func() {
		BeforeEach(func() {
			vm.Spec.Network.Disabled = true
		})

		It("should not have a nic", func() {
			Expect(vm.Spec.Network.Disabled).To(BeTrue())

			vcVM, err := createOrUpdateAndGetVcVM(ctx, vmProvider, vm)
			Expect(err).ToNot(HaveOccurred())

			Expect(conditions.Has(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeFalse())

			var o mo.VirtualMachine
			Expect(vcVM.Properties(ctx, vcVM.Reference(), nil, &o)).To(Succeed())

			devList := object.VirtualDeviceList(o.Config.Hardware.Device)
			l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
			Expect(l).To(BeEmpty())
		})
	})

	When("multiple NICs are specified", func() {
		BeforeEach(func() {
			testConfig.NumNetworks = 0
			testConfig.WithNetworkEnv = builder.NetworkEnvNamed

			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: &vmopv1common.PartialObjectRef{Name: "VM Network"},
				},
				{
					Name:    "eth1",
					Network: &vmopv1common.PartialObjectRef{Name: dvpgName},
				},
			}
		})

		It("Has expected devices", func() {
			vcVM, err := createOrUpdateAndGetVcVM(ctx, vmProvider, vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())

			var o mo.VirtualMachine
			Expect(vcVM.Properties(ctx, vcVM.Reference(), nil, &o)).To(Succeed())

			devList := object.VirtualDeviceList(o.Config.Hardware.Device)
			l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
			Expect(l).To(HaveLen(2))

			dev1 := l[0].GetVirtualDevice()
			backing1, ok := dev1.Backing.(*vimtypes.VirtualEthernetCardNetworkBackingInfo)
			Expect(ok).Should(BeTrue())
			Expect(backing1.DeviceName).To(Equal("VM Network"))

			dev2 := l[1].GetVirtualDevice()
			backing2, ok := dev2.Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo)
			Expect(ok).Should(BeTrue())
			_, dvpg := getDVPG(ctx, dvpgName)
			Expect(backing2.Port.PortgroupKey).To(Equal(dvpg.Reference().Value))
		})
	})

	When("multiple NICs mix an explicit Type with class-provided cards", func() {
		BeforeEach(func() {
			testConfig.NumNetworks = 0
			testConfig.WithNetworkEnv = builder.NetworkEnvNamed

			// The VM Class provides two Ethernet devices, positionally zipped
			// with the two VM Spec interfaces below. eth0 has an explicit
			// Type, so it must not reuse its class device at position 0 (that
			// device is simply dropped, not handed to eth1). eth1 has no
			// Type, so it is zipped with its own class device at position 1.
			// The class device types (PCNet32, E1000e) are intentionally
			// different from the VMXNet3 default CreateVirtualEthernetCard
			// creates, so the resulting devices can be told apart.
			configSpec := vimtypes.VirtualMachineConfigSpec{
				DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device:    &vimtypes.VirtualPCNet32{},
					},
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device:    &vimtypes.VirtualE1000e{},
					},
				},
			}
			jsonConfigSpec, err := util.MarshalConfigSpecToJSON(configSpec)
			Expect(err).ToNot(HaveOccurred())
			vmClass.Spec.ConfigSpec = jsonConfigSpec

			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Type:    vmopv1.VirtualMachineNetworkInterfaceTypeE1000, // TODO: Will be vmxnet3 until honor Type
					Network: &vmopv1common.PartialObjectRef{Name: "VM Network"},
				},
				{
					Name:    "eth1",
					Network: &vmopv1common.PartialObjectRef{Name: dvpgName},
				},
			}
		})

		It("preserves interface order and pairs each interface with its own positional class device", func() {
			vcVM, err := createOrUpdateAndGetVcVM(ctx, vmProvider, vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())

			var o mo.VirtualMachine
			Expect(vcVM.Properties(ctx, vcVM.Reference(), nil, &o)).To(Succeed())

			devList := object.VirtualDeviceList(o.Config.Hardware.Device)
			l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})

			// Only 2 devices: eth0's newly created card, and eth1's own
			// positional class device. The class device at position 0
			// (PCNet32) is dropped, not reassigned to eth1.
			Expect(l).To(HaveLen(2))

			By("eth0 gets a newly created card, not its own class device", func() {
				_, ok := l[0].(*vimtypes.VirtualVmxnet3)
				Expect(ok).To(BeTrue())
				backing, ok := l[0].GetVirtualDevice().Backing.(*vimtypes.VirtualEthernetCardNetworkBackingInfo)
				Expect(ok).To(BeTrue())
				Expect(backing.DeviceName).To(Equal("VM Network"))
			})

			By("eth1 reuses its own positional class device", func() {
				_, ok := l[1].(*vimtypes.VirtualE1000e)
				Expect(ok).To(BeTrue())
				backing, ok := l[1].GetVirtualDevice().Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo)
				Expect(ok).To(BeTrue())
				_, dvpg := getDVPG(ctx, dvpgName)
				Expect(backing.Port.PortgroupKey).To(Equal(dvpg.Reference().Value))
			})
		})
	})

	When("the VM Class ConfigSpec has more Ethernet cards than the VM has network interfaces", func() {
		BeforeEach(func() {
			testConfig.NumNetworks = 0
			testConfig.WithNetworkEnv = builder.NetworkEnvNamed

			// The VM Class provides three Ethernet devices, but the VM Spec
			// only has one interface. The first class device is positionally
			// zipped with that interface; the other two have no corresponding
			// VM Spec interface and are dropped from the ConfigSpec.
			configSpec := vimtypes.VirtualMachineConfigSpec{
				DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device:    &vimtypes.VirtualE1000e{},
					},
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device:    &vimtypes.VirtualPCNet32{},
					},
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device:    &vimtypes.VirtualVmxnet2{},
					},
				},
			}
			jsonConfigSpec, err := util.MarshalConfigSpecToJSON(configSpec)
			Expect(err).ToNot(HaveOccurred())
			vmClass.Spec.ConfigSpec = jsonConfigSpec

			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: &vmopv1common.PartialObjectRef{Name: "VM Network"},
				},
			}
		})

		It("drops the unmatched class devices and keeps the matched one", func() {
			vcVM, err := createOrUpdateAndGetVcVM(ctx, vmProvider, vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())

			var o mo.VirtualMachine
			Expect(vcVM.Properties(ctx, vcVM.Reference(), nil, &o)).To(Succeed())

			devList := object.VirtualDeviceList(o.Config.Hardware.Device)
			l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})

			// Only 1 device: eth0's own positional class device. The other
			// two class devices (PCNet32, Vmxnet2) have no corresponding VM
			// Spec interface and are dropped.
			Expect(l).To(HaveLen(1))

			_, ok := l[0].(*vimtypes.VirtualE1000e)
			Expect(ok).To(BeTrue())
			backing, ok := l[0].GetVirtualDevice().Backing.(*vimtypes.VirtualEthernetCardNetworkBackingInfo)
			Expect(ok).To(BeTrue())
			Expect(backing.DeviceName).To(Equal("VM Network"))
		})
	})

	// Spec 017 T018: status names guest-reported interfaces from the shared
	// hardware match.
	When("two NICs are on the same network", func() {
		var vcVM *object.VirtualMachine

		cardsByUnitNumber := func() map[int32]ethCardInfo {
			return ethCardsByUnitNumber(ctx, vcVM)
		}

		statusNames := func() map[int32]string {
			return statusInterfaceNames(vm)
		}

		setUnitNumbers := func(unitNumbers ...*int32) {
			for i := range vm.Spec.Network.Interfaces {
				vm.Spec.Network.Interfaces[i].UnitNumber = unitNumbers[i]
			}
		}

		BeforeEach(func() {
			testConfig.NumNetworks = 0
			testConfig.WithNetworkEnv = builder.NetworkEnvNamed

			pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
				config.Features.VMNetworkUnitNumbers = true
			})

			// No schema upgrade sets the NIC unit numbers feature version yet,
			// so mark the VM as already upgraded to it. Otherwise every
			// reconcile stops at the schema upgrade.
			if vm.Annotations == nil {
				vm.Annotations = map[string]string{}
			}
			vm.Annotations[pkgconst.UpgradedToFeatureVersionAnnotationKey] =
				vmopv1util.FeatureVersionNICUnitNumbers.String()

			vm.Spec.Network.Interfaces = []vmopv1.VirtualMachineNetworkInterfaceSpec{
				{
					Name:    "eth0",
					Network: &vmopv1common.PartialObjectRef{Name: "VM Network"},
				},
				{
					Name:    "eth1",
					Network: &vmopv1common.PartialObjectRef{Name: "VM Network"},
				},
			}
		})

		JustBeforeEach(func() {
			var err error
			vcVM, err = createOrUpdateAndGetVcVM(ctx, vmProvider, vm)
			Expect(err).ToNot(HaveOccurred())

			// Reconcile again so status is built from the created VM.
			Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
		})

		It("names the cards consistently for numbered, unnumbered, and mixed interfaces", func() {
			cards := cardsByUnitNumber()
			Expect(cards).To(HaveLen(2))
			Expect(cards).To(HaveKey(int32(7)))
			Expect(cards).To(HaveKey(int32(8)))
			card7, card8 := cards[7], cards[8]

			By("unnumbered interfaces are named in spec and device order", func() {
				Expect(statusNames()).To(Equal(map[int32]string{
					card7.key: "eth0",
					card8.key: "eth1",
				}))
				Expect(statusInterfaceMACs(vm)).To(Equal(map[int32]string{
					card7.key: strings.ToLower(card7.mac),
					card8.key: strings.ToLower(card8.mac),
				}))
			})

			By("numbered interfaces are named by unit number, not position", func() {
				setUnitNumbers(ptr.To[int32](8), ptr.To[int32](7))
				Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
				Expect(statusNames()).To(Equal(map[int32]string{
					card8.key: "eth0",
					card7.key: "eth1",
				}))
			})

			By("reordering numbered spec entries keeps each interface's card", func() {
				ifaces := vm.Spec.Network.Interfaces
				ifaces[0], ifaces[1] = ifaces[1], ifaces[0]
				Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
				Expect(vm.Spec.Network.Interfaces[0].Name).To(Equal("eth1"))
				Expect(statusNames()).To(Equal(map[int32]string{
					card8.key: "eth0",
					card7.key: "eth1",
				}))
			})

			By("a numbered interface whose unit number has no card is not given another card", func() {
				// eth1 is first in the spec.
				setUnitNumbers(ptr.To[int32](9), ptr.To[int32](8))
				Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
				Expect(statusNames()).To(Equal(map[int32]string{
					card8.key: "eth0",
					card7.key: "",
				}))
			})

			By("mixed numbered and unnumbered interfaces", func() {
				// eth1 is unnumbered and takes the card eth0 leaves.
				setUnitNumbers(nil, ptr.To[int32](8))
				Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
				Expect(statusNames()).To(Equal(map[int32]string{
					card8.key: "eth0",
					card7.key: "eth1",
				}))
			})

			By("the hardware was not changed", func() {
				Expect(cardsByUnitNumber()).To(Equal(cards))
			})
		})
	})

	Context("simulate", func() {
		var (
			cloudInitSecret *corev1.Secret
			sysprepSecret   *corev1.Secret
		)

		BeforeEach(func() {
			testConfig.NumNetworks = 3

			// Speed up tests until we Watch the network interface types. Sigh.
			network.RetryTimeout = 1 * time.Millisecond

			pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
				config.Features.MutableNetworks = true
			})
		})

		JustBeforeEach(func() {
			if vm.Spec.Network != nil &&
				len(vm.Spec.Network.Interfaces) > 0 {

				vm.Spec.Network.Interfaces[0].Nameservers = []string{"1.1.1.1", "8.8.8.8"}
				vm.Spec.Network.Interfaces[0].SearchDomains = []string{"vmware.local"}
			}

			cloudInitSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cloud-init-secret",
					Namespace: nsInfo.Namespace,
				},
				Data: map[string][]byte{
					"user-value": []byte(""),
				},
			}

			sysprepSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-sysprep-secret",
					Namespace: nsInfo.Namespace,
				},
				Data: map[string][]byte{
					"unattend": []byte("foo"),
				},
			}
		})

		AfterEach(func() {
			cloudInitSecret = nil
			sysprepSecret = nil
		})

		DescribeTableSubtree("simulate vpc backup/restore",
			func(powerState vmopv1.VirtualMachinePowerState, newLSUUID bool) {
				var np fakeNetworkProvider

				BeforeEach(func() {
					testConfig.WithNetworkEnv = builder.NetworkEnvVPC
					np = vpcNetworkProvider{}

					vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
						Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
							{
								Name: interfaceName0,
								Network: &vmopv1common.PartialObjectRef{
									TypeMeta: np.getTypeMeta(),
									Name:     networkName0,
								},
							},
							{
								Name: interfaceName1,
								Network: &vmopv1common.PartialObjectRef{
									TypeMeta: np.getTypeMeta(),
									Name:     networkName1,
								},
							},
						},
					}
				})

				It("should succeed", func() {
					err := createOrUpdateVM(ctx, vmProvider, vm)
					Expect(err).To(HaveOccurred())
					Expect(err).To(MatchError(network.ErrNetworkInterfaceNotReady))
					Expect(conditions.IsFalse(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())

					By("simulate successful network provider reconcile", func() {
						np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 0)
						np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 1)
					})

					vm.Spec.PowerState = powerState
					Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())

					Expect(vm.Status.UniqueID).ToNot(BeEmpty())
					vcVM := ctx.GetVMFromMoID(vm.Status.UniqueID)

					By("created with expected NIC device types and backings", func() {
						devList, err := vcVM.Device(ctx)
						Expect(err).ToNot(HaveOccurred())
						l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
						Expect(l).To(HaveLen(2))

						dev0 := l[0]
						_, ok := dev0.(*vimtypes.VirtualVmxnet3)
						Expect(ok).To(BeTrue())
						np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], 0)

						dev1 := l[1]
						_, ok = dev1.(*vimtypes.VirtualVmxnet3)
						Expect(ok).To(BeTrue())
						np.assertEthernetCard(ctx, dev1, vm.Spec.Network.Interfaces[1], 1)
					})

					By("Verify VM power state", func() {
						ps, err := vcVM.PowerState(ctx)
						Expect(err).ToNot(HaveOccurred())
						if powerState == vmopv1.VirtualMachinePowerStateOff {
							Expect(ps).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
						} else {
							Expect(ps).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						}
					})

					restoredNetworkIdx := 0
					if newLSUUID {
						// Restore can have two behaviors: if the network existed before the backup,
						// then we expect just a new ExtID. If the network was created between backup
						// and restore, we expect both a new ExtID and LSUUID. Use the third network
						// to simulate the restore creating a new network.
						restoredNetworkIdx = 2
					}
					restoredExtID := ""

					By("simulate VPC restore", func() {
						interfaceSpec := vm.Spec.Network.Interfaces[0]
						interfaceName, networkName := interfaceSpec.Name, interfaceSpec.Network.Name

						subnetPort := &vpcv1alpha1.SubnetPort{}
						objKey := client.ObjectKey{
							Name:      network.VPCCRName(vm.Name, networkName, interfaceName),
							Namespace: vm.Namespace,
						}
						Expect(ctx.Client.Get(ctx, objKey, subnetPort)).To(Succeed())

						subnetPort.Status.Attachment.ID += "-restored"
						subnetPort.Status.NetworkInterfaceConfig.LogicalSwitchUUID = ctx.GetNetwork(restoredNetworkIdx).LogicalSwitchUUID
						Expect(ctx.Client.Status().Update(ctx, subnetPort)).To(Succeed())

						restoredExtID = subnetPort.Status.Attachment.ID
					})

					vm.Annotations[network.VPCInterfaceRestoredAnnotation] = interfaceName0
					Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
					Expect(vm.Annotations).ToNot(HaveKey(network.VPCInterfaceRestoredAnnotation))

					By("restore NIC with expected device types and backings", func() {
						devList, err := vcVM.Device(ctx)
						Expect(err).ToNot(HaveOccurred())
						l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
						Expect(l).To(HaveLen(2))

						// Sometimes even an edit operation will cause vcsim to reorder the devices.
						sort.Slice(l, func(i, j int) bool {
							return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
						})

						dev0 := l[0]
						ethCard, ok := dev0.(*vimtypes.VirtualVmxnet3)
						Expect(ok).To(BeTrue())
						{
							// Assert here that the interface has its expected new ExtID, but then
							// restore the original one so we can use assertEthernetCard() as-is.
							Expect(ethCard.ExternalId).To(Equal(restoredExtID))
							ethCard.ExternalId = strings.TrimSuffix(ethCard.ExternalId, "-restored")
						}
						np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], restoredNetworkIdx)

						dev1 := l[1]
						_, ok = dev1.(*vimtypes.VirtualVmxnet3)
						Expect(ok).To(BeTrue())
						np.assertEthernetCard(ctx, dev1, vm.Spec.Network.Interfaces[1], 1)
					})
				})
			},
			Entry("PoweredOn - Same Network", vmopv1.VirtualMachinePowerStateOn, false),
			Entry("PoweredOn - New Network", vmopv1.VirtualMachinePowerStateOn, true),
			Entry("PoweredOff - Same Network", vmopv1.VirtualMachinePowerStateOff, false),
			Entry("PoweredOff - New Network", vmopv1.VirtualMachinePowerStateOff, true),
		)

		Context("simulate mixed interface provider types", func() {
			BeforeEach(func() {
				pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
					config.Features.PerNamespaceNetworkProvider = true
					config.Features.BringYourOwnNetworkInterface = true
				})
			})

			DescribeTableSubtree("with two different provider types",
				func(
					netCfg []builder.VCSimNetworkConfig,
				) {
					var np0, np1 fakeNetworkProvider

					BeforeEach(func() {
						Expect(netCfg).To(HaveLen(2))

						testConfig.NumNetworks = 0
						testConfig.WithNetworkEnv = ""
						testConfig.WithNetworkConfig = netCfg

						np0 = fakeNPForType(netCfg[0].Provider)
						np1 = fakeNPForType(netCfg[1].Provider)
					})

					JustBeforeEach(func() {
						p, err := netsetutil.TypeToNetworkProvider(netCfg[0].Provider)
						Expect(err).ToNot(HaveOccurred())
						lp, err := netsetutil.TypeToNetworkProvider(netCfg[1].Provider)
						Expect(err).ToNot(HaveOccurred())

						By("Set legacy network provider", func() {
							ns := &netopv1alpha1.NetworkSettings{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "default",
									Namespace: vm.Namespace,
								},
							}
							Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(ns), ns)).To(Succeed())
							Expect(ns.Provider).To(Equal(p))
							Expect(ns.LegacyProvider).To(BeEmpty())
							ns.LegacyProvider = lp
							Expect(ctx.Client.Update(ctx, ns)).To(Succeed())
						})
					})

					It("configures both NICs when powered off, does not reconfigure when powered on", func() {
						vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
							Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
								{
									Name: interfaceName0,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np0.getTypeMeta(),
										Name:     networkName0,
									},
								},
								{
									Name: interfaceName1,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np1.getTypeMeta(),
										Name:     networkName1,
									},
								},
							},
						}

						By("first reconcile: both NICs not yet ready", func() {
							err := createOrUpdateVM(ctx, vmProvider, vm)
							Expect(err).To(HaveOccurred())
							Expect(err).To(MatchError(network.ErrNetworkInterfaceNotReady))
						})

						By("simulate np0 interface ready", func() {
							np0.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 0)
						})

						By("simulate np1 interface ready", func() {
							np1.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 1)
						})

						By("create VM powered off with both NICs configured", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						})

						Expect(vm.Status.UniqueID).ToNot(BeEmpty())
						vcVM := ctx.GetVMFromMoID(vm.Status.UniqueID)

						By("VM is powered off with expected NICs", func() {
							ps, err := vcVM.PowerState(ctx)
							Expect(err).ToNot(HaveOccurred())
							Expect(ps).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))

							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							sort.Slice(l, func(i, j int) bool {
								return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
							})

							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)
							np1.assertEthernetCard(ctx, l[1], vm.Spec.Network.Interfaces[1], 1)
						})

						By("power on VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						})

						By("powered-on VM has same NICs - ethernet cards are not reconfigured when powered on", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							sort.Slice(l, func(i, j int) bool {
								return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
							})

							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)
							np1.assertEthernetCard(ctx, l[1], vm.Spec.Network.Interfaces[1], 1)
						})
					})

					It("uses, then releases, pre-created external network interfaces", func() {
						// The same name is used for both CRs: only their type differs.
						const externalName = "my-external-interface"

						externalRef := func(np fakeNetworkProvider) *vmopv1common.LocalObjectRef {
							tm, _ := np.newInterfaceObject()
							return &vmopv1common.LocalObjectRef{
								APIVersion: tm.APIVersion,
								Kind:       tm.Kind,
								Name:       externalName,
							}
						}

						vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
							Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
								{
									Name:        interfaceName0,
									ExternalRef: externalRef(np0),
								},
								{
									Name:        interfaceName1,
									ExternalRef: externalRef(np1),
								},
							},
						}

						getInterface := func(np fakeNetworkProvider) client.Object {
							_, obj := np.newInterfaceObject()
							ExpectWithOffset(1, ctx.Client.Get(
								ctx,
								client.ObjectKey{Namespace: vm.Namespace, Name: externalName},
								obj)).To(Succeed())
							return obj
						}

						assertAssociated := func(obj client.Object, interfaceName string) {
							ExpectWithOffset(1, obj.GetOwnerReferences()).To(BeEmpty())
							ExpectWithOffset(1, obj.GetLabels()).To(HaveKeyWithValue(pkgconst.NetworkInterfaceVMNameLabel, vm.Name))
							ExpectWithOffset(1, obj.GetLabels()).To(HaveKeyWithValue(network.VMInterfaceNameLabel, interfaceName))
							ExpectWithOffset(1, obj.GetAnnotations()).To(HaveKey(pkgconst.ExternalNetworkInterfaceAnnotation))
						}

						assertReleased := func(obj client.Object) {
							ExpectWithOffset(1, obj.GetLabels()).ToNot(HaveKey(pkgconst.NetworkInterfaceVMNameLabel))
							ExpectWithOffset(1, obj.GetLabels()).ToNot(HaveKey(network.VMInterfaceNameLabel))
							ExpectWithOffset(1, obj.GetAnnotations()).ToNot(HaveKey(pkgconst.ExternalNetworkInterfaceAnnotation))
							ExpectWithOffset(1, obj.GetAnnotations()).ToNot(HaveKey(pkgconst.VPCAttachmentRef))
						}

						By("simulate the pre-created external interfaces being ready", func() {
							np0.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 0)
							np1.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 1)
						})

						By("create VM powered off with both NICs configured", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						})

						Expect(vm.Status.UniqueID).ToNot(BeEmpty())
						vcVM := ctx.GetVMFromMoID(vm.Status.UniqueID)

						By("NICs are backed by the external interfaces", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							sort.Slice(l, func(i, j int) bool {
								return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
							})

							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)
							np1.assertEthernetCard(ctx, l[1], vm.Spec.Network.Interfaces[1], 1)
						})

						By("external interfaces are associated with the VM", func() {
							assertAssociated(getInterface(np0), interfaceName0)
							assertAssociated(getInterface(np1), interfaceName1)
						})

						By("remove the np1 interface and power off the VM", func() {
							vm.Spec.Network.Interfaces = vm.Spec.Network.Interfaces[:1]
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						})

						By("np1 NIC is removed and its external interface is released, not deleted", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(1))
							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)

							assertAssociated(getInterface(np0), interfaceName0)
							assertReleased(getInterface(np1))
						})

						By("delete the VM releases the remaining external interface", func() {
							Expect(vmProvider.DeleteVirtualMachine(ctx, vm)).To(Succeed())
							assertReleased(getInterface(np0))
						})
					})

					It("add and remove a network interface of different provider type", func() {
						// Start with np0 (eth0) only.
						vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
							Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
								{
									Name: interfaceName0,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np0.getTypeMeta(),
										Name:     networkName0,
									},
								},
							},
						}

						By("first reconcile: np0 NIC not yet ready", func() {
							err := createOrUpdateVM(ctx, vmProvider, vm)
							Expect(err).To(HaveOccurred())
							Expect(err).To(MatchError(network.ErrNetworkInterfaceNotReady))
						})

						By("simulate np0 interface ready", func() {
							np0.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 0)
						})

						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						Expect(vm.Status.UniqueID).ToNot(BeEmpty())
						vcVM := ctx.GetVMFromMoID(vm.Status.UniqueID)

						By("VM has np0 NIC", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(1))
							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)
						})

						By("add np1 network interface", func() {
							vm.Spec.Network.Interfaces = append(
								vm.Spec.Network.Interfaces,
								vmopv1.VirtualMachineNetworkInterfaceSpec{
									Name: interfaceName1,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np1.getTypeMeta(),
										Name:     networkName1,
									},
								},
							)
						})

						// Spec 017 T020 (G2): the pending np1 interface no longer
						// blocks the power-off. The pass ends with a bounded
						// requeue while the interface is pending.
						By("power off: np1 NIC not yet ready", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							err := createOrUpdateVM(ctx, vmProvider, vm)
							Expect(pkgerr.IsRequeueError(err)).To(BeTrue(), "err: %v", err)
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
						})

						By("simulate np1 interface ready", func() {
							np1.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 1)
						})

						By("power off with both NICs", func() {
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))

							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							sort.Slice(l, func(i, j int) bool {
								return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
							})

							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)
							np1.assertEthernetCard(ctx, l[1], vm.Spec.Network.Interfaces[1], 1)
						})

						By("power on VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						})

						By("remove np1 network interface", func() {
							vm.Spec.Network.Interfaces = vm.Spec.Network.Interfaces[:1]
						})

						By("power off VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
						})

						By("np1 NIC has been removed", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(1))
							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)
						})

						By("np1 network interface CR has been deleted", func() {
							np1.assertNetworkInterfacesDNE(ctx, vm, networkName1, interfaceName1)
						})

						By("power on VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						})

						By("np0 NIC is unaffected by powering on", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(1))
							np0.assertEthernetCard(ctx, l[0], vm.Spec.Network.Interfaces[0], 0)
						})
					})
				},
				Entry("VPC and VDS",
					[]builder.VCSimNetworkConfig{
						{Provider: pkgcfg.NetworkProviderTypeVPC},
						{Provider: pkgcfg.NetworkProviderTypeVDS},
					},
				),
				Entry("VPC and NSXT",
					[]builder.VCSimNetworkConfig{
						{Provider: pkgcfg.NetworkProviderTypeVPC},
						{Provider: pkgcfg.NetworkProviderTypeNSXT},
					},
				),
			)
		})

		Context("simulate vm power on/off", func() {
			DescribeTableSubtree("with adding/removing network interfaces ",
				func(networkEnv builder.NetworkEnv, bootstrap string, standardPortGroup bool) {
					var np fakeNetworkProvider

					BeforeEach(func() {
						var providerType pkgcfg.NetworkProviderType

						switch networkEnv {
						case builder.NetworkEnvVDS:
							providerType = pkgcfg.NetworkProviderTypeVDS
							np = vdsNetworkProvider{isStandardPG: standardPortGroup}
						case builder.NetworkEnvNSXT:
							providerType = pkgcfg.NetworkProviderTypeNSXT
							np = nsxtNetworkProvider{}
						case builder.NetworkEnvVPC:
							providerType = pkgcfg.NetworkProviderTypeVPC
							np = vpcNetworkProvider{}
						default:
							// The named network environment is not tested here.
						}

						if standardPortGroup {
							// Standard PG are only supported with PerNS network providers.
							pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
								config.Features.PerNamespaceNetworkProvider = true
							})
						}

						testConfig.NumNetworks = 0
						testConfig.WithNetworkConfig = []builder.VCSimNetworkConfig{
							{Provider: providerType, StandardPortGroup: standardPortGroup},
							{Provider: providerType, StandardPortGroup: standardPortGroup},
						}
					})

					JustBeforeEach(func() {
						switch bootstrap {
						case bsCloudInit:
							Expect(ctx.Client.Create(ctx, cloudInitSecret)).To(Succeed())
							vm.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
								CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
									RawCloudConfig: &vmopv1common.SecretKeySelector{
										Name: cloudInitSecret.Name,
									},
								},
							}
						case bsSysprep:
							Expect(ctx.Client.Create(ctx, sysprepSecret)).To(Succeed())
							vm.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
								Sysprep: &vmopv1.VirtualMachineBootstrapSysprepSpec{
									RawSysprep: &vmopv1common.SecretKeySelector{
										Name: sysprepSecret.Name,
										Key:  "unattend",
									},
								},
							}
						case bsLinuxPrep:
							vm.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
								LinuxPrep: &vmopv1.VirtualMachineBootstrapLinuxPrepSpec{},
							}
						}

						vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
							Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
								{
									Name: interfaceName0,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np.getTypeMeta(),
										Name:     networkName0,
									},
								},
							},
						}
					})

					It("should succeed", func() {
						err := createOrUpdateVM(ctx, vmProvider, vm)
						Expect(err).To(HaveOccurred())
						Expect(err).To(MatchError(network.ErrNetworkInterfaceNotReady))
						Expect(conditions.IsFalse(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())

						By("simulate successful network provider reconcile", func() {
							np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 0)
						})

						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())

						By("has expected conditions", func() {
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionClassReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionImageReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionStorageReady)).To(BeTrue())
							if bootstrap != "" {
								Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionBootstrapReady)).To(BeTrue())
							} else {
								Expect(conditions.Get(vm, vmopv1.VirtualMachineConditionBootstrapReady)).To(BeNil())
							}
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionPlacementReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionCreated)).To(BeTrue())
						})

						Expect(vm.Status.UniqueID).ToNot(BeEmpty())
						vcVM := ctx.GetVMFromMoID(vm.Status.UniqueID)

						By("has expected NIC backing", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(1))

							dev0 := l[0]
							np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], 0)
						})

						By("add network interface", func() {
							vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces, vm.Spec.Network.Interfaces[0])
							vm.Spec.Network.Interfaces[1].Name = interfaceName1
							vm.Spec.Network.Interfaces[1].Network = ptr.To(*vm.Spec.Network.Interfaces[1].Network)
							vm.Spec.Network.Interfaces[1].Network.Name = networkName1
						})

						// Spec 017 T020 (G2): the pending interface no longer
						// blocks the power-off. The pass ends with a bounded
						// requeue while the interface is pending.
						By("power off VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							err = createOrUpdateVM(ctx, vmProvider, vm)
							Expect(pkgerr.IsRequeueError(err)).To(BeTrue(), "err: %v", err)
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
							Expect(conditions.IsFalse(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())
						})

						By("simulate successful network provider reconcile on added interface", func() {
							np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 1)
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						})

						By("power on VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						})

						By("Added interface has expected NIC backing", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							dev1 := l[1]
							np.assertEthernetCard(ctx, dev1, vm.Spec.Network.Interfaces[1], 1)
						})

						By("remove just added network interface", func() {
							vm.Spec.Network.Interfaces = vm.Spec.Network.Interfaces[:1]
						})

						By("power off and on VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))

							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						})

						By("interface has been removed", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(1))

							dev0 := l[0]
							np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], 0)

							By("network interface has been deleted", func() {
								np.assertNetworkInterfacesDNE(ctx, vm, networkName1, interfaceName1)
							})
						})
					})
				},
				Entry("VDS with CloudInit", builder.NetworkEnvVDS, bsCloudInit, false),
				Entry("VDS Standard Portgroup with CloudInit", builder.NetworkEnvVDS, bsCloudInit, true),
				Entry("NSX-T with CloudInit", builder.NetworkEnvNSXT, bsCloudInit, false),
				Entry("VPC with Sysprep", builder.NetworkEnvVPC, bsSysprep, false),
			)

			DescribeTableSubtree("with modifying network interface",
				func(networkEnv builder.NetworkEnv, bootstrap string, standardPortGroup bool) {
					var np fakeNetworkProvider

					BeforeEach(func() {
						var providerType pkgcfg.NetworkProviderType

						switch networkEnv {
						case builder.NetworkEnvVDS:
							providerType = pkgcfg.NetworkProviderTypeVDS
							np = vdsNetworkProvider{isStandardPG: standardPortGroup}
						case builder.NetworkEnvNSXT:
							providerType = pkgcfg.NetworkProviderTypeNSXT
							np = nsxtNetworkProvider{}
						case builder.NetworkEnvVPC:
							providerType = pkgcfg.NetworkProviderTypeVPC
							np = vpcNetworkProvider{}
						default:
							// The named network environment is not tested here.
						}

						if standardPortGroup {
							// Standard PG are only supported with PerNS network providers.
							pkgcfg.SetContext(parentCtx, func(config *pkgcfg.Config) {
								config.Features.PerNamespaceNetworkProvider = true
							})
						}

						testConfig.NumNetworks = 0
						testConfig.WithNetworkConfig = []builder.VCSimNetworkConfig{
							{Provider: providerType, StandardPortGroup: standardPortGroup},
							{Provider: providerType, StandardPortGroup: standardPortGroup},
							{Provider: providerType, StandardPortGroup: standardPortGroup},
						}

						// We assert the device type is preserved when editing an interface spec.
						configSpec := vimtypes.VirtualMachineConfigSpec{
							DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
								&vimtypes.VirtualDeviceConfigSpec{
									Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
									Device:    &vimtypes.VirtualE1000e{},
								},
								&vimtypes.VirtualDeviceConfigSpec{
									Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
									Device:    &vimtypes.VirtualVmxnet2{},
								},
							},
						}

						jsonConfigSpec, err := util.MarshalConfigSpecToJSON(configSpec)
						Expect(err).ToNot(HaveOccurred())
						vmClass.Spec.ConfigSpec = jsonConfigSpec
					})

					JustBeforeEach(func() {
						switch bootstrap {
						case bsCloudInit:
							Expect(ctx.Client.Create(ctx, cloudInitSecret)).To(Succeed())
							vm.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
								CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
									RawCloudConfig: &vmopv1common.SecretKeySelector{
										Name: cloudInitSecret.Name,
									},
								},
							}
						case bsSysprep:
							Expect(ctx.Client.Create(ctx, sysprepSecret)).To(Succeed())
							vm.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
								Sysprep: &vmopv1.VirtualMachineBootstrapSysprepSpec{
									RawSysprep: &vmopv1common.SecretKeySelector{
										Name: sysprepSecret.Name,
										Key:  "unattend",
									},
								},
							}
						case bsLinuxPrep:
							vm.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
								LinuxPrep: &vmopv1.VirtualMachineBootstrapLinuxPrepSpec{},
							}
						}

						vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
							Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
								{
									Name: interfaceName0,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np.getTypeMeta(),
										Name:     networkName0,
									},
								},
								{
									Name: interfaceName1,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np.getTypeMeta(),
										Name:     networkName1,
									},
								},
							},
						}
					})

					It("should succeed", func() {
						err := createOrUpdateVM(ctx, vmProvider, vm)
						Expect(err).To(HaveOccurred())
						Expect(err).To(MatchError(network.ErrNetworkInterfaceNotReady))
						Expect(conditions.IsFalse(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())

						By("simulate successful network provider reconcile", func() {
							np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 0)
							np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 1)
						})

						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())

						By("has expected conditions", func() {
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionClassReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionImageReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionStorageReady)).To(BeTrue())
							if bootstrap != "" {
								Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionBootstrapReady)).To(BeTrue())
							} else {
								Expect(conditions.Get(vm, vmopv1.VirtualMachineConditionBootstrapReady)).To(BeNil())
							}
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionPlacementReady)).To(BeTrue())
							Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionCreated)).To(BeTrue())
						})

						Expect(vm.Status.UniqueID).ToNot(BeEmpty())
						vcVM := ctx.GetVMFromMoID(vm.Status.UniqueID)

						By("created with expected NIC device types and backings", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							dev0 := l[0]
							_, ok := dev0.(*vimtypes.VirtualE1000e)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], 0)

							dev1 := l[1]
							_, ok = dev1.(*vimtypes.VirtualVmxnet2)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev1, vm.Spec.Network.Interfaces[1], 1)
						})

						By("edit second network interface to use different network", func() {
							vm.Spec.Network.Interfaces[1].Network.Name = networkName2
						})

						// Spec 017 T020 (G2): the pending interface no longer
						// blocks the power-off. The pass ends with a bounded
						// requeue while the interface is pending.
						By("power off VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							err = createOrUpdateVM(ctx, vmProvider, vm)
							Expect(pkgerr.IsRequeueError(err)).To(BeTrue(), "err: %v", err)
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
							Expect(conditions.IsFalse(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())
						})

						By("simulate successful network provider reconcile on updated interface", func() {
							np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 2)
						})

						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())

						By("network interface has been deleted", func() {
							np.assertNetworkInterfacesDNE(ctx, vm, networkName1, interfaceName1)
						})

						By("powered off VM has expected NIC device types and backings", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							// Sometimes even an edit operation will cause vcsim to reorder the devices.
							sort.Slice(l, func(i, j int) bool {
								return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
							})

							dev0 := l[0]
							_, ok := dev0.(*vimtypes.VirtualE1000e)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], 0)

							dev1 := l[1]
							_, ok = dev1.(*vimtypes.VirtualVmxnet2)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev1, vm.Spec.Network.Interfaces[1], 2)
						})

						By("power on VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						})

						By("still has expected NIC device types and backings", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							sort.Slice(l, func(i, j int) bool {
								return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
							})

							dev0 := l[0]
							_, ok := dev0.(*vimtypes.VirtualE1000e)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], 0)

							dev1 := l[1]
							_, ok = dev1.(*vimtypes.VirtualVmxnet2)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev1, vm.Spec.Network.Interfaces[1], 2)
						})

						By("power off VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
							Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
						})

						By("update first network interface to use different network", func() {
							vm.Spec.Network.Interfaces[0].Network.Name = networkName2
						})

						// Spec 017 T020: power-on stays blocked by the explicit
						// network gate; the pass ends with a bounded requeue
						// instead of a NoRequeueError.
						By("try to power on VM", func() {
							vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
							err = createOrUpdateVM(ctx, vmProvider, vm)
							Expect(pkgerr.IsRequeueError(err)).To(BeTrue(), "err: %v", err)
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
							Expect(conditions.IsFalse(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())
						})

						By("simulate successful network provider reconcile on updated interface", func() {
							np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 2)
						})

						By("power on VM", func() {
							err := createOrUpdateVM(ctx, vmProvider, vm)
							Expect(err).ToNot(HaveOccurred())
							Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						})

						By("has expected NIC device types and backings", func() {
							devList, err := vcVM.Device(ctx)
							Expect(err).ToNot(HaveOccurred())
							l := devList.SelectByType(&vimtypes.VirtualEthernetCard{})
							Expect(l).To(HaveLen(2))

							sort.Slice(l, func(i, j int) bool {
								return l[i].GetVirtualDevice().Key < l[j].GetVirtualDevice().Key
							})

							dev0 := l[0]
							_, ok := dev0.(*vimtypes.VirtualE1000e)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev0, vm.Spec.Network.Interfaces[0], 2)

							dev1 := l[1]
							_, ok = dev1.(*vimtypes.VirtualVmxnet2)
							Expect(ok).To(BeTrue())
							np.assertEthernetCard(ctx, dev1, vm.Spec.Network.Interfaces[1], 2)
						})
					})
				},
				Entry("VDS with CloudInit", builder.NetworkEnvVDS, bsCloudInit, false),
				Entry("VDS Standard Portgroup with CloudInit", builder.NetworkEnvVDS, bsCloudInit, true),
				Entry("VPC with CloudInit", builder.NetworkEnvVPC, bsCloudInit, false),
				Entry("VDS with LinuxPrep", builder.NetworkEnvVDS, bsLinuxPrep, false),
				Entry("NSX-T with Sysprep", builder.NetworkEnvNSXT, bsSysprep, false),
			)
		})

		// Characterization (spec 017 T012): pins pre-restructuring behavior.
		Context("pending network interface characterization", func() {
			DescribeTableSubtree("with an added interface whose provider CR is not ready",
				func(networkEnv builder.NetworkEnv) {
					var np fakeNetworkProvider

					BeforeEach(func() {
						var providerType pkgcfg.NetworkProviderType
						switch networkEnv {
						case builder.NetworkEnvVDS:
							providerType = pkgcfg.NetworkProviderTypeVDS
							np = vdsNetworkProvider{}
						case builder.NetworkEnvNSXT:
							providerType = pkgcfg.NetworkProviderTypeNSXT
							np = nsxtNetworkProvider{}
						case builder.NetworkEnvVPC:
							providerType = pkgcfg.NetworkProviderTypeVPC
							np = vpcNetworkProvider{}
						default:
							Fail(fmt.Sprintf("unexpected network env: %v", networkEnv))
						}

						testConfig.NumNetworks = 0
						testConfig.WithNetworkConfig = []builder.VCSimNetworkConfig{
							{Provider: providerType},
							{Provider: providerType},
						}

						vm.Spec.Network = &vmopv1.VirtualMachineNetworkSpec{
							Interfaces: []vmopv1.VirtualMachineNetworkInterfaceSpec{
								{
									Name: interfaceName0,
									Network: &vmopv1common.PartialObjectRef{
										TypeMeta: np.getTypeMeta(),
										Name:     networkName0,
									},
								},
							},
						}
					})

					createPoweredOnVM := func() *object.VirtualMachine {
						GinkgoHelper()

						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(MatchError(network.ErrNetworkInterfaceNotReady))
						np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[0], 0)

						vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn
						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						Expect(vm.Status.UniqueID).ToNot(BeEmpty())

						vcVM := ctx.GetVMFromMoID(vm.Status.UniqueID)
						Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						return vcVM
					}

					addPendingInterface := func() {
						vm.Spec.Network.Interfaces = append(vm.Spec.Network.Interfaces,
							vmopv1.VirtualMachineNetworkInterfaceSpec{
								Name: interfaceName1,
								Network: &vmopv1common.PartialObjectRef{
									TypeMeta: np.getTypeMeta(),
									Name:     networkName1,
								},
							})
					}

					// Spec 017 T020 (G2): before T020 a pending interface
					// returned a NoRequeueError from the config step, so
					// updateVirtualMachine returned before reconcilePowerState
					// and the requested power-off did not happen. Now the
					// pending interface is data: the VM powers off and the
					// pass ends with a bounded RequeueError, not a
					// NoRequeueError. This characterization test was
					// intentionally expected to flip.
					It("Spec 017 T020 (G2): pending interface does not block power-off", func() {
						vcVM := createPoweredOnVM()

						addPendingInterface()
						vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff

						err := createOrUpdateVM(ctx, vmProvider, vm)
						Expect(pkgerr.IsRequeueError(err)).To(BeTrue(), "err: %v", err)
						Expect(pkgerr.IsNoRequeueError(err)).To(BeFalse())
						Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
					})

					// Power-on remains gated on a pending interface after T020,
					// so only the outcome is asserted here, not the error.
					It("pending interface prevents power-on", func() {
						vcVM := createPoweredOnVM()

						vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))

						addPendingInterface()
						vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn

						_ = createOrUpdateVM(ctx, vmProvider, vm)
						Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))
					})

					// Spec 017 T020: the explicit power-on gate blocks only
					// while the interface is pending, the update path reports
					// the interface on VirtualMachineNetworkReady, and the
					// pass ends with a bounded requeue rather than a
					// NoRequeueError.
					It("Spec 017 T020: reports the pending interface and powers on once it is ready", func() {
						vcVM := createPoweredOnVM()

						vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOff
						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))

						addPendingInterface()
						vm.Spec.PowerState = vmopv1.VirtualMachinePowerStateOn

						err := createOrUpdateVM(ctx, vmProvider, vm)
						Expect(pkgerr.IsRequeueError(err)).To(BeTrue(), "err: %v", err)
						Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOff))

						c := conditions.Get(vm, vmopv1.VirtualMachineConditionNetworkReady)
						Expect(c).ToNot(BeNil())
						Expect(c.Status).To(Equal(metav1.ConditionFalse))
						Expect(c.Reason).To(Equal(network.PlanReasonProviderPending))
						Expect(c.Message).To(HavePrefix(interfaceName1 + ": ProviderPending"))
						Expect(conditions.IsFalse(vm, vmopv1.VirtualMachinePowerStateSynced)).To(BeTrue())

						np.simulateInterfaceReconcile(ctx, vm, vm.Spec.Network.Interfaces[1], 1)

						Expect(createOrUpdateVM(ctx, vmProvider, vm)).To(Succeed())
						Expect(vcVM.PowerState(ctx)).To(Equal(vimtypes.VirtualMachinePowerStatePoweredOn))
						Expect(conditions.IsTrue(vm, vmopv1.VirtualMachineConditionNetworkReady)).To(BeTrue())

						devList, err := vcVM.Device(ctx)
						Expect(err).ToNot(HaveOccurred())
						Expect(devList.SelectByType(&vimtypes.VirtualEthernetCard{})).To(HaveLen(2))
					})
				},
				Entry("VDS", builder.NetworkEnvVDS),
				Entry("NSX-T", builder.NetworkEnvNSXT),
				Entry("VPC", builder.NetworkEnvVPC),
			)
		})
	})
}

type fakeNetworkProvider interface {
	getTypeMeta() metav1.TypeMeta
	simulateInterfaceReconcile(ctx *builder.TestContextForVCSim, vm *vmopv1.VirtualMachine, interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec, networkIdx int)
	assertEthernetCard(ctx *builder.TestContextForVCSim, dev vimtypes.BaseVirtualDevice, interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec, networkIdx int)
	assertNetworkInterfacesDNE(ctx *builder.TestContextForVCSim, vm *vmopv1.VirtualMachine, networkName, interfaceName string)

	// newInterfaceObject returns the TypeMeta and an empty object of the
	// provider's network interface CR type for use as an external ref.
	newInterfaceObject() (metav1.TypeMeta, client.Object)
}

func fakeNPForType(providerType pkgcfg.NetworkProviderType) fakeNetworkProvider {
	switch providerType {
	case pkgcfg.NetworkProviderTypeVDS:
		return &vdsNetworkProvider{}
	case pkgcfg.NetworkProviderTypeNSXT:
		return &nsxtNetworkProvider{}
	case pkgcfg.NetworkProviderTypeVPC:
		return &vpcNetworkProvider{}
	default:
		Fail(fmt.Sprintf("unknown network provider type: %v", providerType))
	}
	return nil
}

// interfaceNetworkName returns the name of the interface's network or, for an
// external interface, the name of its network interface CR.
func interfaceNetworkName(interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec) string {
	if ref := interfaceSpec.ExternalRef; ref != nil {
		return ref.Name
	}
	return interfaceSpec.Network.Name
}

func extID(networkName, interfaceName string) string {
	return networkName + "-" + interfaceName
}

var ifaceIdxRegex = regexp.MustCompile(`(\d+)$`)

func idxFromInterfaceName(s string) int {
	m := ifaceIdxRegex.FindStringSubmatch(s)
	Expect(m).To(HaveLen(2))
	i, _ := strconv.ParseInt(m[1], 10, 32)
	return int(i)
}

type vdsNetworkProvider struct {
	isStandardPG bool
}

func (vdsNetworkProvider) getTypeMeta() metav1.TypeMeta {
	return metav1.TypeMeta{
		APIVersion: netopv1alpha1.SchemeGroupVersion.String(),
		Kind:       "Network",
	}
}

func (vdsNetworkProvider) newInterfaceObject() (metav1.TypeMeta, client.Object) {
	return metav1.TypeMeta{
		APIVersion: netopv1alpha1.SchemeGroupVersion.String(),
		Kind:       "NetworkInterface",
	}, &netopv1alpha1.NetworkInterface{}
}

func (v vdsNetworkProvider) simulateInterfaceReconcile(
	ctx *builder.TestContextForVCSim,
	vm *vmopv1.VirtualMachine,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	networkIdx int) {

	interfaceName := interfaceSpec.Name
	ifaceIdx := idxFromInterfaceName(interfaceName)

	var networkName string
	netInterface := &netopv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Namespace: vm.Namespace},
	}
	if ref := interfaceSpec.ExternalRef; ref != nil {
		// The user creates the external interface CR.
		networkName = ref.Name
		netInterface.Name = ref.Name
		netInterface.Spec.NetworkName = networkName
		Expect(ctx.Client.Create(ctx, netInterface)).To(Succeed())
	} else {
		networkName = interfaceSpec.Network.Name
		netInterface.Name = network.NetOPCRName(vm.Name, networkName, interfaceName, false)
		Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)).To(Succeed())
		Expect(netInterface.Spec.NetworkName).To(Equal(networkName))
	}

	netInterface.Status.NetworkID = ctx.GetNetwork(networkIdx).Backing.Reference().Value
	netInterface.Status.ExternalID = extID(networkName, interfaceName)
	netInterface.Status.MacAddress = "" // NetOP doesn't set this.
	netInterface.Status.IPConfigs = []netopv1alpha1.IPConfig{
		{
			IP:         fmt.Sprintf("192.168.1.2%d", ifaceIdx),
			IPFamily:   corev1.IPv4Protocol,
			Gateway:    "192.168.1.1",
			SubnetMask: "255.255.255.0",
		},
	}
	netInterface.Status.Conditions = []netopv1alpha1.NetworkInterfaceCondition{
		{
			Type:   netopv1alpha1.NetworkInterfaceReady,
			Status: corev1.ConditionTrue,
		},
	}
	Expect(ctx.Client.Status().Update(ctx, netInterface)).To(Succeed())
}

func (v vdsNetworkProvider) assertEthernetCard(
	ctx *builder.TestContextForVCSim,
	dev vimtypes.BaseVirtualDevice,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	networkIdx int) {

	interfaceName, networkName := interfaceSpec.Name, interfaceNetworkName(interfaceSpec)

	ethCard := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	if v.isStandardPG {
		backingInfo, ok := ethCard.Backing.(*vimtypes.VirtualEthernetCardNetworkBackingInfo)
		Expect(ok).Should(BeTrue())
		expectedBacking, err := ctx.GetNetwork(networkIdx).Backing.EthernetCardBackingInfo(ctx)
		Expect(err).ToNot(HaveOccurred())
		expectedBackingInfo, ok := expectedBacking.(*vimtypes.VirtualEthernetCardNetworkBackingInfo)
		Expect(ok).Should(BeTrue())
		Expect(backingInfo.DeviceName).To(Equal(expectedBackingInfo.DeviceName))
	} else {
		backingInfo, ok := ethCard.Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo)
		Expect(ok).Should(BeTrue())
		Expect(backingInfo.Port.PortgroupKey).To(Equal(ctx.GetNetwork(networkIdx).Backing.Reference().Value))
	}

	Expect(ethCard.MacAddress).ToNot(BeEmpty())
	Expect(ethCard.ExternalId).To(Equal(extID(networkName, interfaceName)))
}

func (v vdsNetworkProvider) assertNetworkInterfacesDNE(
	ctx *builder.TestContextForVCSim,
	vm *vmopv1.VirtualMachine,
	networkName, interfaceName string) {

	netInterface := &netopv1alpha1.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name:      network.NetOPCRName(vm.Name, networkName, interfaceName, false),
			Namespace: vm.Namespace,
		},
	}
	err := ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)
	Expect(err).To(HaveOccurred())
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

type nsxtNetworkProvider struct{}

func (nsxtNetworkProvider) getTypeMeta() metav1.TypeMeta {
	return metav1.TypeMeta{
		APIVersion: ncpv1alpha1.SchemeGroupVersion.String(),
		Kind:       "VirtualNetwork",
	}
}

func (nsxtNetworkProvider) newInterfaceObject() (metav1.TypeMeta, client.Object) {
	return metav1.TypeMeta{
		APIVersion: ncpv1alpha1.SchemeGroupVersion.String(),
		Kind:       "VirtualNetworkInterface",
	}, &ncpv1alpha1.VirtualNetworkInterface{}
}

func (n nsxtNetworkProvider) simulateInterfaceReconcile(
	ctx *builder.TestContextForVCSim,
	vm *vmopv1.VirtualMachine,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	networkIdx int) {

	interfaceName := interfaceSpec.Name
	ifaceIdx := idxFromInterfaceName(interfaceName)

	var networkName string
	netInterface := &ncpv1alpha1.VirtualNetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Namespace: vm.Namespace},
	}
	if ref := interfaceSpec.ExternalRef; ref != nil {
		// The user creates the external interface CR.
		networkName = ref.Name
		netInterface.Name = ref.Name
		netInterface.Spec.VirtualNetwork = networkName
		Expect(ctx.Client.Create(ctx, netInterface)).To(Succeed())
	} else {
		networkName = interfaceSpec.Network.Name
		netInterface.Name = network.NCPCRName(vm.Name, networkName, interfaceName, false)
		Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)).To(Succeed())
		Expect(netInterface.Spec.VirtualNetwork).To(Equal(networkName))
	}

	netInterface.Status.InterfaceID = extID(networkName, interfaceName)
	netInterface.Status.MacAddress = fmt.Sprintf("01-23-45-67-89-%02X", ifaceIdx)
	netInterface.Status.ProviderStatus = &ncpv1alpha1.VirtualNetworkInterfaceProviderStatus{
		NsxLogicalSwitchID: ctx.GetNetwork(networkIdx).LogicalSwitchUUID,
	}
	netInterface.Status.IPAddresses = []ncpv1alpha1.VirtualNetworkInterfaceIP{
		{
			IP:         fmt.Sprintf("192.168.1.2%d", ifaceIdx),
			Gateway:    "192.168.1.1",
			SubnetMask: "255.255.255.0",
		},
	}
	netInterface.Status.Conditions = []ncpv1alpha1.VirtualNetworkCondition{
		{
			Type:   "Ready",
			Status: "True",
		},
	}
	Expect(ctx.Client.Status().Update(ctx, netInterface)).To(Succeed())
}

func (n nsxtNetworkProvider) assertEthernetCard(
	ctx *builder.TestContextForVCSim,
	dev vimtypes.BaseVirtualDevice,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	networkIdx int) {

	interfaceName, networkName := interfaceSpec.Name, interfaceNetworkName(interfaceSpec)
	ifaceIdx := idxFromInterfaceName(interfaceName)

	ethCard := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	backingInfo, ok := ethCard.Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo)
	Expect(ok).Should(BeTrue())
	Expect(backingInfo.Port.PortgroupKey).To(Equal(ctx.GetNetwork(networkIdx).Backing.Reference().Value))
	Expect(ethCard.MacAddress).To(Equal(fmt.Sprintf("01-23-45-67-89-%02X", ifaceIdx)))
	Expect(ethCard.ExternalId).To(Equal(extID(networkName, interfaceName)))
}

func (n nsxtNetworkProvider) assertNetworkInterfacesDNE(
	ctx *builder.TestContextForVCSim,
	vm *vmopv1.VirtualMachine,
	networkName, interfaceName string) {

	netInterface := &ncpv1alpha1.VirtualNetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Name:      network.NCPCRName(vm.Name, networkName, interfaceName, false),
			Namespace: vm.Namespace,
		},
	}
	err := ctx.Client.Get(ctx, client.ObjectKeyFromObject(netInterface), netInterface)
	Expect(err).To(HaveOccurred())
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

type vpcNetworkProvider struct{}

func (vpcNetworkProvider) getTypeMeta() metav1.TypeMeta {
	return metav1.TypeMeta{
		APIVersion: vpcv1alpha1.SchemeGroupVersion.String(),
		Kind:       "Subnet",
	}
}

func (vpcNetworkProvider) newInterfaceObject() (metav1.TypeMeta, client.Object) {
	return metav1.TypeMeta{
		APIVersion: vpcv1alpha1.SchemeGroupVersion.String(),
		Kind:       "SubnetPort",
	}, &vpcv1alpha1.SubnetPort{}
}

func (v vpcNetworkProvider) simulateInterfaceReconcile(
	ctx *builder.TestContextForVCSim,
	vm *vmopv1.VirtualMachine,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	networkIdx int) {

	interfaceName := interfaceSpec.Name
	ifaceIdx := idxFromInterfaceName(interfaceName)

	var networkName string
	subnetPort := &vpcv1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{Namespace: vm.Namespace},
	}
	if ref := interfaceSpec.ExternalRef; ref != nil {
		// The user creates the external interface CR.
		networkName = ref.Name
		subnetPort.Name = ref.Name
		subnetPort.Spec.Subnet = networkName
		Expect(ctx.Client.Create(ctx, subnetPort)).To(Succeed())
	} else {
		networkName = interfaceSpec.Network.Name
		subnetPort.Name = network.VPCCRName(vm.Name, networkName, interfaceName)
		Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(subnetPort), subnetPort)).To(Succeed())
		Expect(subnetPort.Spec.SubnetSet).To(BeEmpty())
		Expect(subnetPort.Spec.Subnet).To(Equal(networkName))
	}

	subnetPort.Status.Attachment.ID = extID(networkName, interfaceName)
	subnetPort.Status.NetworkInterfaceConfig.MACAddress = fmt.Sprintf("01-23-45-67-89-%02X", ifaceIdx)
	subnetPort.Status.NetworkInterfaceConfig.LogicalSwitchUUID = ctx.GetNetwork(networkIdx).LogicalSwitchUUID
	subnetPort.Status.NetworkInterfaceConfig.IPAddresses = []vpcv1alpha1.NetworkInterfaceIPAddress{
		{
			IPAddress: fmt.Sprintf("192.168.1.11%d/24", ifaceIdx),
			Gateway:   "192.168.1.1",
		},
	}
	subnetPort.Status.Conditions = []vpcv1alpha1.Condition{
		{
			Type:   vpcv1alpha1.Ready,
			Status: corev1.ConditionTrue,
		},
	}
	Expect(ctx.Client.Status().Update(ctx, subnetPort)).To(Succeed())
}

func (v vpcNetworkProvider) assertEthernetCard(
	ctx *builder.TestContextForVCSim,
	dev vimtypes.BaseVirtualDevice,
	interfaceSpec vmopv1.VirtualMachineNetworkInterfaceSpec,
	networkIdx int) {

	interfaceName, networkName := interfaceSpec.Name, interfaceNetworkName(interfaceSpec)
	ifaceIdx := idxFromInterfaceName(interfaceName)

	ethCard := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	backingInfo, ok := ethCard.Backing.(*vimtypes.VirtualEthernetCardDistributedVirtualPortBackingInfo)
	Expect(ok).Should(BeTrue())
	Expect(backingInfo.Port.PortgroupKey).To(Equal(ctx.GetNetwork(networkIdx).Backing.Reference().Value))
	Expect(ethCard.MacAddress).To(Equal(fmt.Sprintf("01-23-45-67-89-%02X", ifaceIdx)))
	Expect(ethCard.ExternalId).To(Equal(extID(networkName, interfaceName)))
}

func (v vpcNetworkProvider) assertNetworkInterfacesDNE(
	ctx *builder.TestContextForVCSim,
	vm *vmopv1.VirtualMachine,
	networkName, interfaceName string) {

	subnetPort := &vpcv1alpha1.SubnetPort{
		ObjectMeta: metav1.ObjectMeta{
			Name:      network.VPCCRName(vm.Name, networkName, interfaceName),
			Namespace: vm.Namespace,
		},
	}
	err := ctx.Client.Get(ctx, client.ObjectKeyFromObject(subnetPort), subnetPort)
	Expect(err).To(HaveOccurred())
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

// ethCardInfo describes an Ethernet card for the shared match status tests.
type ethCardInfo struct {
	key        int32
	unitNumber int32
	mac        string
}

// ethCardsByUnitNumber returns the VM's Ethernet cards keyed by unit number.
func ethCardsByUnitNumber(
	ctx *builder.TestContextForVCSim,
	vcVM *object.VirtualMachine) map[int32]ethCardInfo {

	devList, err := vcVM.Device(ctx)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	out := map[int32]ethCardInfo{}
	for _, dev := range devList.SelectByType(&vimtypes.VirtualEthernetCard{}) {
		ethCard := dev.(vimtypes.BaseVirtualEthernetCard).GetVirtualEthernetCard()
		ExpectWithOffset(1, ethCard.UnitNumber).ToNot(BeNil())
		out[*ethCard.UnitNumber] = ethCardInfo{
			key:        ethCard.Key,
			unitNumber: *ethCard.UnitNumber,
			mac:        ethCard.MacAddress,
		}
	}
	return out
}

// statusInterfaceNames returns the status interface name of each device key.
func statusInterfaceNames(vm *vmopv1.VirtualMachine) map[int32]string {
	ExpectWithOffset(1, vm.Status.Network).ToNot(BeNil())
	out := map[int32]string{}
	for _, iface := range vm.Status.Network.Interfaces {
		out[iface.DeviceKey] = iface.Name
	}
	return out
}

// statusInterfaceMACs returns the lower-case status interface MAC address of
// each device key.
func statusInterfaceMACs(vm *vmopv1.VirtualMachine) map[int32]string {
	ExpectWithOffset(1, vm.Status.Network).ToNot(BeNil())
	out := map[int32]string{}
	for _, iface := range vm.Status.Network.Interfaces {
		if iface.IP != nil {
			out[iface.DeviceKey] = strings.ToLower(iface.IP.MACAddr)
		}
	}
	return out
}
