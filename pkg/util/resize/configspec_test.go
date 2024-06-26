// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package resize_test

import (
	"context"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-cmp/cmp"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	"github.com/vmware-tanzu/vm-operator/pkg/util/resize"
)

type ConfigSpec = vimtypes.VirtualMachineConfigSpec
type ConfigInfo = vimtypes.VirtualMachineConfigInfo

var _ = Describe("CreateResizeConfigSpec", func() {

	ctx := context.Background()
	truePtr, falsePtr := vimtypes.NewBool(true), vimtypes.NewBool(false)

	DescribeTable("ConfigInfo",
		func(
			ci vimtypes.VirtualMachineConfigInfo,
			cs, expectedCS vimtypes.VirtualMachineConfigSpec) {

			actualCS, err := resize.CreateResizeConfigSpec(ctx, ci, cs)
			Expect(err).ToNot(HaveOccurred())
			Expect(reflect.DeepEqual(actualCS, expectedCS)).To(BeTrue(), cmp.Diff(actualCS, expectedCS))
		},

		Entry("Empty needs no updating",
			ConfigInfo{},
			ConfigSpec{},
			ConfigSpec{}),

		Entry("Annotation is currently set",
			ConfigInfo{Annotation: "my-annotation"},
			ConfigSpec{},
			ConfigSpec{}),
		Entry("Annotation is currently unset",
			ConfigInfo{},
			ConfigSpec{Annotation: "my-annotation"},
			ConfigSpec{Annotation: "my-annotation"}),

		Entry("ManagedBy is currently set",
			ConfigInfo{ManagedBy: &vimtypes.ManagedByInfo{Type: "my-managed-by"}},
			ConfigSpec{},
			ConfigSpec{}),
		Entry("ManagedBy is currently unset",
			ConfigInfo{},
			ConfigSpec{ManagedBy: &vimtypes.ManagedByInfo{Type: "my-managed-by"}},
			ConfigSpec{ManagedBy: &vimtypes.ManagedByInfo{Type: "my-managed-by"}}),

		Entry("NumCPUs needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{NumCPU: 2}},
			ConfigSpec{NumCPUs: 4},
			ConfigSpec{NumCPUs: 4}),
		Entry("NumCpus does not need updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{NumCPU: 4}},
			ConfigSpec{NumCPUs: 4},
			ConfigSpec{}),

		Entry("NumCoresPerSocket needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{NumCoresPerSocket: 2}},
			ConfigSpec{NumCoresPerSocket: 4},
			ConfigSpec{NumCoresPerSocket: 4}),
		Entry("NumCoresPerSocket does not need updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{NumCoresPerSocket: 4}},
			ConfigSpec{NumCoresPerSocket: 4},
			ConfigSpec{}),

		Entry("MemoryMB needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{MemoryMB: 512}},
			ConfigSpec{MemoryMB: 1024},
			ConfigSpec{MemoryMB: 1024}),
		Entry("MemoryMB does not need updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{MemoryMB: 1024}},
			ConfigSpec{MemoryMB: 1024},
			ConfigSpec{}),

		Entry("VirtualICH7MPresent needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{VirtualICH7MPresent: truePtr}},
			ConfigSpec{VirtualICH7MPresent: falsePtr},
			ConfigSpec{VirtualICH7MPresent: falsePtr}),
		Entry("VirtualICH7MPresent needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{VirtualICH7MPresent: nil}},
			ConfigSpec{VirtualICH7MPresent: falsePtr},
			ConfigSpec{VirtualICH7MPresent: falsePtr}),
		Entry("VirtualICH7MPresent does not need updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{VirtualICH7MPresent: truePtr}},
			ConfigSpec{VirtualICH7MPresent: truePtr},
			ConfigSpec{}),

		Entry("VirtualSMCPresent needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{VirtualSMCPresent: truePtr}},
			ConfigSpec{VirtualSMCPresent: falsePtr},
			ConfigSpec{VirtualSMCPresent: falsePtr}),
		Entry("VirtualSMCPresent needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{VirtualSMCPresent: nil}},
			ConfigSpec{VirtualSMCPresent: falsePtr},
			ConfigSpec{VirtualSMCPresent: falsePtr}),
		Entry("VirtualSMCPresent does not need updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{VirtualSMCPresent: truePtr}},
			ConfigSpec{VirtualSMCPresent: truePtr},
			ConfigSpec{}),

		Entry("MotherboardLayout needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{MotherboardLayout: "foo"}},
			ConfigSpec{MotherboardLayout: "i440bxHostBridge"},
			ConfigSpec{MotherboardLayout: "i440bxHostBridge"}),
		Entry("MotherboardLayout does not needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{MotherboardLayout: "i440bxHostBridge"}},
			ConfigSpec{MotherboardLayout: "i440bxHostBridge"},
			ConfigSpec{}),

		Entry("SimultaneousThreads needs updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{SimultaneousThreads: 8}},
			ConfigSpec{SimultaneousThreads: 16},
			ConfigSpec{SimultaneousThreads: 16}),
		Entry("SimultaneousThreads does not need updating",
			ConfigInfo{Hardware: vimtypes.VirtualHardware{SimultaneousThreads: 8}},
			ConfigSpec{SimultaneousThreads: 8},
			ConfigSpec{}),

		Entry("CPU allocation (reservation, limit, shares) settings needs updating",
			ConfigInfo{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(100)),
					Limit:       ptr.To(int64(100)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(200)),
					Limit:       ptr.To(int64(200)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelCustom, Shares: 50},
				}},
			ConfigSpec{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(200)),
					Limit:       ptr.To(int64(200)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelCustom, Shares: 50},
				}}),
		Entry("CPU allocation (reservation, limit, shares) settings needs updating - empty to values set ",
			ConfigInfo{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{}},
			ConfigSpec{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(200)),
					Limit:       ptr.To(int64(200)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(200)),
					Limit:       ptr.To(int64(200)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}}),
		Entry("CPU allocation (reservation,limit,shares) settings does not need updating",
			ConfigInfo{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(100)),
					Limit:       ptr.To(int64(100)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{
				CpuAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(100)),
					Limit:       ptr.To(int64(100)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{}),

		Entry("CPU Hot Add/Remove needs updating false to true",
			ConfigInfo{CpuHotAddEnabled: falsePtr, CpuHotRemoveEnabled: falsePtr},
			ConfigSpec{CpuHotAddEnabled: truePtr, CpuHotRemoveEnabled: truePtr},
			ConfigSpec{CpuHotAddEnabled: truePtr, CpuHotRemoveEnabled: truePtr}),
		Entry("CPU Hot Add/Remove needs updating nil to true",
			ConfigInfo{},
			ConfigSpec{CpuHotAddEnabled: truePtr, CpuHotRemoveEnabled: truePtr},
			ConfigSpec{CpuHotAddEnabled: truePtr, CpuHotRemoveEnabled: truePtr}),
		Entry("CPU Hot Add/Remove does not need updating",
			ConfigInfo{CpuHotAddEnabled: falsePtr, CpuHotRemoveEnabled: falsePtr},
			ConfigSpec{CpuHotAddEnabled: falsePtr, CpuHotRemoveEnabled: falsePtr},
			ConfigSpec{}),

		Entry("CPU affinity settings needs updating value change",
			ConfigInfo{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{2, 3}}},
			ConfigSpec{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{1, 3}}},
			ConfigSpec{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{1, 3}}}),
		Entry("CPU affinity settings needs updating - remove existing",
			ConfigInfo{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{2, 3}}},
			ConfigSpec{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{}}},
			ConfigSpec{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{}}}),
		Entry("CPU affinity settings does not need updating",
			ConfigInfo{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{1, 2, 3}}},
			ConfigSpec{CpuAffinity: &vimtypes.VirtualMachineAffinityInfo{AffinitySet: []int32{3, 1, 2}}},
			ConfigSpec{}),

		Entry("CPU perf counter settings does not need updating",
			ConfigInfo{VPMCEnabled: falsePtr},
			ConfigSpec{VPMCEnabled: falsePtr},
			ConfigSpec{}),
		Entry("CPU perf counter settings needs updating",
			ConfigInfo{VPMCEnabled: falsePtr},
			ConfigSpec{VPMCEnabled: truePtr},
			ConfigSpec{VPMCEnabled: truePtr}),

		Entry("Latency sensitivity settings needs updating",
			ConfigInfo{LatencySensitivity: &vimtypes.LatencySensitivity{Level: vimtypes.LatencySensitivitySensitivityLevelLow}},
			ConfigSpec{LatencySensitivity: &vimtypes.LatencySensitivity{Level: vimtypes.LatencySensitivitySensitivityLevelMedium}},
			ConfigSpec{LatencySensitivity: &vimtypes.LatencySensitivity{Level: vimtypes.LatencySensitivitySensitivityLevelMedium}}),
		Entry("Latency sensitivity settings does not need updating",
			ConfigInfo{LatencySensitivity: &vimtypes.LatencySensitivity{Level: vimtypes.LatencySensitivitySensitivityLevelLow}},
			ConfigSpec{LatencySensitivity: &vimtypes.LatencySensitivity{Level: vimtypes.LatencySensitivitySensitivityLevelLow}},
			ConfigSpec{}),

		Entry("Extra Config setting needs updating -- existing key",
			ConfigInfo{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "foo", Value: "bar"}}},
			ConfigSpec{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "foo", Value: "bar1"}}},
			ConfigSpec{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "foo", Value: "bar1"}}}),
		Entry("Extra Config setting needs updating -- new key",
			ConfigInfo{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "foo", Value: "bar"}}},
			ConfigSpec{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "bat", Value: "man"}}},
			ConfigSpec{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "bat", Value: "man"}}}),
		Entry("Extra Config setting does not need updating",
			ConfigInfo{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "foo", Value: "bar"}}},
			ConfigSpec{ExtraConfig: []vimtypes.BaseOptionValue{&vimtypes.OptionValue{Key: "foo", Value: "bar"}}},
			ConfigSpec{}),

		Entry("Console Preferences needs updating -- configInfo console preferences nil",
			ConfigInfo{},
			ConfigSpec{
				ConsolePreferences: &vimtypes.VirtualMachineConsolePreferences{
					EnterFullScreenOnPowerOn: truePtr,
					PowerOnWhenOpened:        falsePtr,
				}},
			ConfigSpec{
				ConsolePreferences: &vimtypes.VirtualMachineConsolePreferences{
					EnterFullScreenOnPowerOn: truePtr,
					PowerOnWhenOpened:        falsePtr,
				}}),
		Entry("Console Preferences needs updating -- configInfo console preferences set",
			ConfigInfo{
				ConsolePreferences: &vimtypes.VirtualMachineConsolePreferences{
					PowerOnWhenOpened: truePtr,
				}},
			ConfigSpec{
				ConsolePreferences: &vimtypes.VirtualMachineConsolePreferences{
					EnterFullScreenOnPowerOn: truePtr,
					PowerOnWhenOpened:        falsePtr,
				}},
			ConfigSpec{
				ConsolePreferences: &vimtypes.VirtualMachineConsolePreferences{
					EnterFullScreenOnPowerOn: truePtr,
					PowerOnWhenOpened:        falsePtr,
				}}),
		Entry("Console Preferences does not need updating",
			ConfigInfo{
				ConsolePreferences: &vimtypes.VirtualMachineConsolePreferences{
					EnterFullScreenOnPowerOn: truePtr,
					PowerOnWhenOpened:        falsePtr,
				}},
			ConfigSpec{
				ConsolePreferences: &vimtypes.VirtualMachineConsolePreferences{
					EnterFullScreenOnPowerOn: truePtr,
					PowerOnWhenOpened:        falsePtr,
				}},
			ConfigSpec{}),

		Entry("Flags do not need updating",
			ConfigInfo{Flags: vimtypes.VirtualMachineFlagInfo{
				CbrcCacheEnabled:    truePtr,
				DisableAcceleration: truePtr,
				DiskUuidEnabled:     truePtr,
				EnableLogging:       truePtr,
				UseToe:              truePtr,
				VvtdEnabled:         truePtr,
				VbsEnabled:          truePtr,
				MonitorType:         "release",
				VirtualMmuUsage:     "on",
				VirtualExecUsage:    "hvAuto",
			}},
			ConfigSpec{Flags: &vimtypes.VirtualMachineFlagInfo{
				CbrcCacheEnabled:    truePtr,
				DisableAcceleration: truePtr,
				DiskUuidEnabled:     truePtr,
				EnableLogging:       truePtr,
				UseToe:              truePtr,
				VvtdEnabled:         truePtr,
				VbsEnabled:          truePtr,
				MonitorType:         "release",
				VirtualMmuUsage:     "on",
				VirtualExecUsage:    "hvAuto",
			}},
			ConfigSpec{}),
		Entry("Flags need updating -- configInfo has no flags set",
			ConfigInfo{},
			ConfigSpec{Flags: &vimtypes.VirtualMachineFlagInfo{
				CbrcCacheEnabled:    truePtr,
				DisableAcceleration: truePtr,
				DiskUuidEnabled:     truePtr,
				EnableLogging:       truePtr,
				UseToe:              truePtr,
				VvtdEnabled:         truePtr,
				VbsEnabled:          truePtr,
				MonitorType:         "release",
				VirtualMmuUsage:     "on",
				VirtualExecUsage:    "hvAuto",
			}},
			ConfigSpec{Flags: &vimtypes.VirtualMachineFlagInfo{
				CbrcCacheEnabled:    truePtr,
				DisableAcceleration: truePtr,
				DiskUuidEnabled:     truePtr,
				EnableLogging:       truePtr,
				UseToe:              truePtr,
				VvtdEnabled:         truePtr,
				VbsEnabled:          truePtr,
				MonitorType:         "release",
				VirtualMmuUsage:     "on",
				VirtualExecUsage:    "hvAuto",
			}}),
		Entry("Flags need updating",
			ConfigInfo{Flags: vimtypes.VirtualMachineFlagInfo{
				CbrcCacheEnabled:    falsePtr,
				DisableAcceleration: falsePtr,
				DiskUuidEnabled:     falsePtr,
				EnableLogging:       falsePtr,
				UseToe:              falsePtr,
				VvtdEnabled:         falsePtr,
				VbsEnabled:          falsePtr,
				MonitorType:         "release",
				VirtualMmuUsage:     "on",
				VirtualExecUsage:    "hvAuto",
			}},
			ConfigSpec{Flags: &vimtypes.VirtualMachineFlagInfo{
				CbrcCacheEnabled:    truePtr,
				DisableAcceleration: truePtr,
				DiskUuidEnabled:     truePtr,
				EnableLogging:       truePtr,
				UseToe:              truePtr,
				VvtdEnabled:         truePtr,
				VbsEnabled:          truePtr,
				MonitorType:         "debug",
				VirtualMmuUsage:     "off",
				VirtualExecUsage:    "hvOn",
			}},
			ConfigSpec{Flags: &vimtypes.VirtualMachineFlagInfo{
				CbrcCacheEnabled:    truePtr,
				DisableAcceleration: truePtr,
				DiskUuidEnabled:     truePtr,
				EnableLogging:       truePtr,
				UseToe:              truePtr,
				VvtdEnabled:         truePtr,
				VbsEnabled:          truePtr,
				MonitorType:         "debug",
				VirtualMmuUsage:     "off",
				VirtualExecUsage:    "hvOn",
			}}),

		Entry("Memory allocation (reservation, limit, shares) settings needs updating",
			ConfigInfo{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(1024)),
					Limit:       ptr.To(int64(1024)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(2048)),
					Limit:       ptr.To(int64(2048)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelCustom, Shares: 50},
				}},
			ConfigSpec{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(2048)),
					Limit:       ptr.To(int64(2048)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelCustom, Shares: 50},
				}}),
		Entry("Memory allocation (reservation, limit, shares) settings needs updating - empty to values set ",
			ConfigInfo{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{}},
			ConfigSpec{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(2048)),
					Limit:       ptr.To(int64(2048)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(2048)),
					Limit:       ptr.To(int64(2048)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}}),
		Entry("Memory allocation (reservation,limit,shares) settings does not need updating",
			ConfigInfo{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(1024)),
					Limit:       ptr.To(int64(1024)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{
				MemoryAllocation: &vimtypes.ResourceAllocationInfo{
					Reservation: ptr.To(int64(1024)),
					Limit:       ptr.To(int64(1024)),
					Shares:      &vimtypes.SharesInfo{Level: vimtypes.SharesLevelNormal},
				}},
			ConfigSpec{}),

		Entry("Memory Hot Add needs updating false to true",
			ConfigInfo{MemoryHotAddEnabled: falsePtr},
			ConfigSpec{MemoryHotAddEnabled: truePtr},
			ConfigSpec{MemoryHotAddEnabled: truePtr}),
		Entry("Memory Hot Add needs updating nil to true",
			ConfigInfo{},
			ConfigSpec{MemoryHotAddEnabled: truePtr},
			ConfigSpec{MemoryHotAddEnabled: truePtr}),
		Entry("Memory Hot Add does not need updating",
			ConfigInfo{MemoryHotAddEnabled: falsePtr},
			ConfigSpec{MemoryHotAddEnabled: falsePtr},
			ConfigSpec{}),

		Entry("Fixed pass-through hot plug enabled setting does not need updating",
			ConfigInfo{FixedPassthruHotPlugEnabled: falsePtr},
			ConfigSpec{FixedPassthruHotPlugEnabled: falsePtr},
			ConfigSpec{}),
		Entry("Fixed pass-through hot plug enabled setting needs updating",
			ConfigInfo{FixedPassthruHotPlugEnabled: falsePtr},
			ConfigSpec{FixedPassthruHotPlugEnabled: truePtr},
			ConfigSpec{FixedPassthruHotPlugEnabled: truePtr}),

		Entry("Nested hardware-assisted virtualization setting does not need updating",
			ConfigInfo{NestedHVEnabled: falsePtr},
			ConfigSpec{NestedHVEnabled: falsePtr},
			ConfigSpec{}),
		Entry("Nested hardware-assisted virtualization setting needs updating",
			ConfigInfo{NestedHVEnabled: falsePtr},
			ConfigSpec{NestedHVEnabled: truePtr},
			ConfigSpec{NestedHVEnabled: truePtr}),

		Entry("SEV (Secure Encryption Virtualization) setting does not need updating",
			ConfigInfo{SevEnabled: falsePtr},
			ConfigSpec{SevEnabled: falsePtr},
			ConfigSpec{}),
		Entry("SEV (Secure Encryption Virtualization) setting needs updating",
			ConfigInfo{SevEnabled: falsePtr},
			ConfigSpec{SevEnabled: truePtr},
			ConfigSpec{SevEnabled: truePtr}),

		Entry("VMX stats collection setting does not need updating",
			ConfigInfo{VmxStatsCollectionEnabled: falsePtr},
			ConfigSpec{VmxStatsCollectionEnabled: falsePtr},
			ConfigSpec{}),
		Entry("VMX stats collection setting needs updating",
			ConfigInfo{VmxStatsCollectionEnabled: falsePtr},
			ConfigSpec{VmxStatsCollectionEnabled: truePtr},
			ConfigSpec{VmxStatsCollectionEnabled: truePtr}),

		Entry("Memory Reservation Locked to Max needs updating",
			ConfigInfo{MemoryReservationLockedToMax: falsePtr},
			ConfigSpec{MemoryReservationLockedToMax: truePtr},
			ConfigSpec{MemoryReservationLockedToMax: truePtr}),
		Entry("Memory Reservation Locked to Max needs updating -- not set in config info",
			ConfigInfo{},
			ConfigSpec{MemoryReservationLockedToMax: truePtr},
			ConfigSpec{MemoryReservationLockedToMax: truePtr}),
		Entry("Memory Reservation Locked to Max does not need updating",
			ConfigInfo{MemoryReservationLockedToMax: falsePtr},
			ConfigSpec{MemoryReservationLockedToMax: falsePtr},
			ConfigSpec{}),
		Entry("Memory Reservation Locked to Max cannot be false with PCI pass-through devices in spec",
			ConfigInfo{},
			ConfigSpec{
				MemoryReservationLockedToMax: falsePtr,
				DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device: &vimtypes.VirtualPCIPassthrough{
							VirtualDevice: vimtypes.VirtualDevice{
								Key: -200,
								Backing: &vimtypes.VirtualPCIPassthroughVmiopBackingInfo{
									Vgpu: "profile-from-configspec",
								},
							},
						},
					},
				},
			},
			ConfigSpec{
				MemoryReservationLockedToMax: truePtr,
				DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device: &vimtypes.VirtualPCIPassthrough{
							VirtualDevice: vimtypes.VirtualDevice{
								Key: -200,
								Backing: &vimtypes.VirtualPCIPassthroughVmiopBackingInfo{
									Vgpu: "profile-from-configspec",
								},
							},
						},
					},
				},
			}),
		Entry("Memory Reservation Locked to Max can be unset with PCI pass-through devices in spec",
			ConfigInfo{MemoryReservationLockedToMax: truePtr},
			ConfigSpec{
				DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device: &vimtypes.VirtualPCIPassthrough{
							VirtualDevice: vimtypes.VirtualDevice{
								Key: -200,
								Backing: &vimtypes.VirtualPCIPassthroughVmiopBackingInfo{
									Vgpu: "profile-from-configspec",
								},
							},
						},
					},
				},
			},
			ConfigSpec{
				DeviceChange: []vimtypes.BaseVirtualDeviceConfigSpec{
					&vimtypes.VirtualDeviceConfigSpec{
						Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
						Device: &vimtypes.VirtualPCIPassthrough{
							VirtualDevice: vimtypes.VirtualDevice{
								Key: -200,
								Backing: &vimtypes.VirtualPCIPassthroughVmiopBackingInfo{
									Vgpu: "profile-from-configspec",
								},
							},
						},
					},
				},
			}),

		Entry("GMM needs updating -- config info GMM empty",
			ConfigInfo{},
			ConfigSpec{
				GuestMonitoringModeInfo: &vimtypes.VirtualMachineGuestMonitoringModeInfo{
					GmmFile:      "foo",
					GmmAppliance: "bar",
				}},
			ConfigSpec{
				GuestMonitoringModeInfo: &vimtypes.VirtualMachineGuestMonitoringModeInfo{
					GmmFile:      "foo",
					GmmAppliance: "bar",
				}}),
		Entry("GMM needs updating",
			ConfigInfo{
				GuestMonitoringModeInfo: &vimtypes.VirtualMachineGuestMonitoringModeInfo{
					GmmFile:      "bat",
					GmmAppliance: "man",
				}},
			ConfigSpec{
				GuestMonitoringModeInfo: &vimtypes.VirtualMachineGuestMonitoringModeInfo{
					GmmFile:      "foo",
					GmmAppliance: "bar",
				}},
			ConfigSpec{
				GuestMonitoringModeInfo: &vimtypes.VirtualMachineGuestMonitoringModeInfo{
					GmmFile:      "foo",
					GmmAppliance: "bar",
				}}),
		Entry("GMM does not need updating",
			ConfigInfo{
				GuestMonitoringModeInfo: &vimtypes.VirtualMachineGuestMonitoringModeInfo{
					GmmFile:      "foo",
					GmmAppliance: "bar",
				}},
			ConfigSpec{
				GuestMonitoringModeInfo: &vimtypes.VirtualMachineGuestMonitoringModeInfo{
					GmmFile:      "foo",
					GmmAppliance: "bar",
				}},
			ConfigSpec{}),

		Entry("Encrypted vMotion mode does not need updating",
			ConfigInfo{MigrateEncryption: "disabled"},
			ConfigSpec{MigrateEncryption: "disabled"},
			ConfigSpec{}),
		Entry("Encrypted vMotion mode needs updating",
			ConfigInfo{MigrateEncryption: "disabled"},
			ConfigSpec{MigrateEncryption: "opportunistic"},
			ConfigSpec{MigrateEncryption: "opportunistic"}),
		Entry("Encrypted vMotion mode needs updating -- configInfo migrate encryption unset",
			ConfigInfo{},
			ConfigSpec{MigrateEncryption: "required"},
			ConfigSpec{MigrateEncryption: "required"}),

		Entry("N_Port ID Virtualization temporarily disabled, non-RDM disk flags does not need updating",
			ConfigInfo{NpivTemporaryDisabled: falsePtr, NpivOnNonRdmDisks: falsePtr},
			ConfigSpec{NpivTemporaryDisabled: falsePtr, NpivOnNonRdmDisks: falsePtr},
			ConfigSpec{}),
		Entry("N_Port ID Virtualization temporarily disabled, non-RDM disk flags needs updating",
			ConfigInfo{NpivTemporaryDisabled: falsePtr, NpivOnNonRdmDisks: falsePtr},
			ConfigSpec{NpivTemporaryDisabled: truePtr, NpivOnNonRdmDisks: truePtr},
			ConfigSpec{NpivTemporaryDisabled: truePtr, NpivOnNonRdmDisks: truePtr}),

		Entry("N_Port ID Virtualization does not need updating -- remove",
			ConfigInfo{},
			ConfigSpec{NpivWorldWideNameOp: string(vimtypes.VirtualMachineConfigSpecNpivWwnOpRemove)},
			ConfigSpec{}),
		Entry("N_Port ID Virtualization does not need updating -- generate with desired WW names equal in length",
			ConfigInfo{
				NpivNodeWorldWideName: []int64{100, 200},
				NpivPortWorldWideName: []int64{300, 400},
			},
			ConfigSpec{
				NpivWorldWideNameOp: string(vimtypes.VirtualMachineConfigSpecNpivWwnOpGenerate),
				NpivDesiredNodeWwns: int16(2),
				NpivDesiredPortWwns: int16(2),
			},
			ConfigSpec{}),
		Entry("N_Port ID Virtualization does not need updating -- set",
			ConfigInfo{
				NpivNodeWorldWideName: []int64{100, 200},
				NpivPortWorldWideName: []int64{300, 400},
			},
			ConfigSpec{
				NpivWorldWideNameOp:   string(vimtypes.VirtualMachineConfigSpecNpivWwnOpSet),
				NpivDesiredPortWwns:   int16(2),
				NpivDesiredNodeWwns:   int16(2),
				NpivNodeWorldWideName: []int64{100, 200},
				NpivPortWorldWideName: []int64{300, 400},
			},
			ConfigSpec{}),

		Entry("N_Port ID Virtualization needs updating -- remove",
			ConfigInfo{
				NpivNodeWorldWideName: []int64{100, 200},
				NpivPortWorldWideName: []int64{300, 400},
			},
			ConfigSpec{NpivWorldWideNameOp: string(vimtypes.VirtualMachineConfigSpecNpivWwnOpRemove)},
			ConfigSpec{NpivWorldWideNameOp: string(vimtypes.VirtualMachineConfigSpecNpivWwnOpRemove)}),
		Entry("N_Port ID Virtualization needs updating -- generate with desired WW names greater in length",
			ConfigInfo{
				NpivNodeWorldWideName: []int64{100, 200},
				NpivPortWorldWideName: []int64{300, 400},
			},
			ConfigSpec{
				NpivWorldWideNameOp: string(vimtypes.VirtualMachineConfigSpecNpivWwnOpGenerate),
				NpivDesiredNodeWwns: int16(3),
				NpivDesiredPortWwns: int16(3),
			},
			ConfigSpec{
				NpivWorldWideNameOp: string(vimtypes.VirtualMachineConfigSpecNpivWwnOpGenerate),
				NpivDesiredNodeWwns: int16(3),
				NpivDesiredPortWwns: int16(3),
			}),
		Entry("N_Port ID Virtualization needs updating -- set with new values",
			ConfigInfo{
				NpivNodeWorldWideName: []int64{101, 201},
				NpivPortWorldWideName: []int64{301, 401},
			},
			ConfigSpec{
				NpivWorldWideNameOp:   string(vimtypes.VirtualMachineConfigSpecNpivWwnOpSet),
				NpivDesiredPortWwns:   int16(2),
				NpivDesiredNodeWwns:   int16(2),
				NpivNodeWorldWideName: []int64{100, 200},
				NpivPortWorldWideName: []int64{300, 400},
			},
			ConfigSpec{
				NpivWorldWideNameOp:   string(vimtypes.VirtualMachineConfigSpecNpivWwnOpSet),
				NpivDesiredPortWwns:   int16(2),
				NpivDesiredNodeWwns:   int16(2),
				NpivNodeWorldWideName: []int64{100, 200},
				NpivPortWorldWideName: []int64{300, 400},
			}),
	)

})
