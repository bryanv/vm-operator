# NIC unit numbers — govmomi research results (T001)

## Environment

| Property | Value |
|---|---|
| Run at | 2026-09-30T23:23:10Z |
| vCenter | vcf-10-158-42-0 (VMware vCenter Server 8.0.3 build-25197330) |
| vCenter version | 8.0.3 |
| vCenter build | 25197330 |
| vCenter API version | 8.0.3.0 |
| ESX host | sp-telco-srv-48 |
| ESX version | 8.0.3 |
| ESX build | 24280767 |
| VM hardware version | vmx-21 |
| Datacenter | /PNimbus |
| Resource pool | /PNimbus/host/nimbus01/Resources |
| Datastore | /PNimbus/datastore/datastore1 |
| Folder | /PNimbus/vm |
| Network | /PNimbus/network/vlan1868 |
| Support matrix covered | vCenter 8.0.3 / ESXi 8.0.3, Mellanox ConnectX-4 Lx SR-IOV PF (nmlx5_core), 8 VFs |
| govmomi | v0.57.0-alpha.0.0.20260908193317-e23a942e8f55 |

> A single-vCenter run does not answer cross-version stability. Treat every result below as characterising the builds named above only (R6).

## Summary

| Experiment | Question(s) | Status | Title |
|---|---|---|---|
| E14 | Q3 | RECORDED | SR-IOV ethernet cards: unit-number band, device keys, and VMX namespace |

## Results

### E14 — SR-IOV ethernet cards: unit-number band, device keys, and VMX namespace

**Answers**: Q3

**Status**: RECORDED

#### Step: Lifecycle VM: CreateVM with a vmxnet3 and an SR-IOV card, both UnitNumber nil

Requested:

```
unit=nil (auto) key=-1 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-2 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: ReconfigVM_Task Add, SR-IOV UnitNumber nil

Requested:

```
unit=nil (auto) key=-3 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: ReconfigVM_Task Add, SR-IOV explicit unit 13

Requested:

```
unit=13 key=-4 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: ReconfigVM_Task Add, SR-IOV explicit unit 40

Requested:

```
unit=40 key=-5 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: Collision: vmxnet3 at the first SR-IOV card's unit 62

Requested:

```
unit=62 key=-6 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

Error: `ReconfigVM_Task failed: A specified parameter was not correct: unitNumber`

Fault: `InvalidArgument`

> A specified parameter was not correct: unitNumber

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: Collision: SR-IOV card at the vmxnet3's unit 7

Requested:

```
unit=7 key=-7 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=61 key=13027 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:e0:49/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=183) sriovPF=0000:1a:00.1 sriovVF=
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru27.deviceId = "0"
pciPassthru27.dvs.portgroupId = "dvportgroup-75"
pciPassthru27.id = "00000:026:00.1"
pciPassthru27.pfId = "00000:026:00.1"
pciPassthru27.present = "TRUE"
pciPassthru27.systemId = "BYPASS"
pciPassthru27.vendorId = "0"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: powered on

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 pciSlot=1216 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=61 key=13027 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=160 mac=00:50:56:8c:e0:49/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=183) sriovPF=0000:1a:00.1 sriovVF=
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=192 mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=224 mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=256 mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=1184 mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.pciSlotNumber = "1216"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru27.deviceId = "0"
pciPassthru27.dvs.portgroupId = "dvportgroup-75"
pciPassthru27.id = "00000:026:00.1"
pciPassthru27.pciSlotNumber = "160"
pciPassthru27.pfId = "00000:026:00.1"
pciPassthru27.present = "TRUE"
pciPassthru27.systemId = "BYPASS"
pciPassthru27.vendorId = "0"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pciSlotNumber = "192"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pciSlotNumber = "224"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pciSlotNumber = "256"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pciSlotNumber = "1184"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: Hot-add an SR-IOV card while powered on (informational)

Requested:

```
unit=nil (auto) key=-8 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 pciSlot=1216 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=61 key=13027 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=160 mac=00:50:56:8c:e0:49/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=183) sriovPF=0000:1a:00.1 sriovVF=
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=192 mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=224 mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=256 mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=1184 mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

Error: `ReconfigVM_Task failed: The hot-plug operation failed. `

Fault: `GenericVmConfigFault`

> The hot-plug operation failed. 
>
> msg.devices.hotplug.failed: The hot-plug operation failed. 
>
> msg.devices.hotadd.failed: Hot-add of pciPassthru26 failed. 
>
> msg.migrate.save.error: An error occurred while saving the state for migration. 
>
> msg.pciPassthru.noSuspend.postedInterrupts: A virtual machine with a PCI DirectPath I/O device cannot be suspended when FPT devices in-use have posted interrupts enabled.
>
> msg.pciPassthru.noMigration: vMotion, Storage vMotion, Fault Tolerance, and PCI hot-plug cannot be used for a virtual machine with PCI DirectPath I/O device. 

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.pciSlotNumber = "1216"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru26.deviceId = "0"
pciPassthru26.dvs.portgroupId = "dvportgroup-75"
pciPassthru26.id = "00000:026:00.1"
pciPassthru26.pciSlotNumber = "-1"
pciPassthru26.pfId = "00000:026:00.1"
pciPassthru26.present = "FALSE"
pciPassthru26.systemId = "BYPASS"
pciPassthru26.vendorId = "0"
pciPassthru27.deviceId = "0"
pciPassthru27.dvs.portgroupId = "dvportgroup-75"
pciPassthru27.id = "00000:026:00.1"
pciPassthru27.pciSlotNumber = "160"
pciPassthru27.pfId = "00000:026:00.1"
pciPassthru27.present = "TRUE"
pciPassthru27.systemId = "BYPASS"
pciPassthru27.vendorId = "0"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pciSlotNumber = "192"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pciSlotNumber = "224"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pciSlotNumber = "256"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pciSlotNumber = "1184"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: powered back off

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 pciSlot=1216 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=61 key=13027 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=160 mac=00:50:56:8c:e0:49/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=183) sriovPF=0000:1a:00.1 sriovVF=
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=192 mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=224 mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=256 mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=1184 mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.pciSlotNumber = "1216"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru26.deviceId = "0"
pciPassthru26.dvs.portgroupId = "dvportgroup-75"
pciPassthru26.id = "00000:026:00.1"
pciPassthru26.pciSlotNumber = "-1"
pciPassthru26.pfId = "00000:026:00.1"
pciPassthru26.present = "FALSE"
pciPassthru26.systemId = "BYPASS"
pciPassthru26.vendorId = "0"
pciPassthru27.deviceId = "0"
pciPassthru27.dvs.portgroupId = "dvportgroup-75"
pciPassthru27.id = "00000:026:00.1"
pciPassthru27.pciSlotNumber = "160"
pciPassthru27.pfId = "00000:026:00.1"
pciPassthru27.present = "TRUE"
pciPassthru27.systemId = "BYPASS"
pciPassthru27.vendorId = "0"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pciSlotNumber = "192"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pciSlotNumber = "224"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pciSlotNumber = "256"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pciSlotNumber = "1184"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Lifecycle VM: remove the first SR-IOV card

Requested:

```
unit=65 key=13031 controllerKey=100 kind=remove VirtualSriovEthernetCard pciSlot=1184 mac=00:50:56:8c:d0:6d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 pciSlot=1216 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=61 key=13027 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=160 mac=00:50:56:8c:e0:49/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=183) sriovPF=0000:1a:00.1 sriovVF=
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=192 mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=224 mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=256 mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.pciSlotNumber = "1216"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru26.deviceId = "0"
pciPassthru26.dvs.portgroupId = "dvportgroup-75"
pciPassthru26.id = "00000:026:00.1"
pciPassthru26.pciSlotNumber = "-1"
pciPassthru26.pfId = "00000:026:00.1"
pciPassthru26.present = "FALSE"
pciPassthru26.systemId = "BYPASS"
pciPassthru26.vendorId = "0"
pciPassthru27.deviceId = "0"
pciPassthru27.dvs.portgroupId = "dvportgroup-75"
pciPassthru27.id = "00000:026:00.1"
pciPassthru27.pciSlotNumber = "160"
pciPassthru27.pfId = "00000:026:00.1"
pciPassthru27.present = "TRUE"
pciPassthru27.systemId = "BYPASS"
pciPassthru27.vendorId = "0"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pciSlotNumber = "192"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pciSlotNumber = "224"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pciSlotNumber = "256"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
```

#### Step: Lifecycle VM: add a new SR-IOV card, UnitNumber nil

Requested:

```
unit=nil (auto) key=-9 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 pciSlot=1216 mac=00:50:56:8c:8d:b6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=178)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=61 key=13027 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=160 mac=00:50:56:8c:e0:49/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=183) sriovPF=0000:1a:00.1 sriovVF=
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=192 mac=00:50:56:8c:87:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=182) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=224 mac=00:50:56:8c:2f:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=181) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard pciSlot=256 mac=00:50:56:8c:0b:51/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=180) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:03:f6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=179) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.pciSlotNumber = "1216"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru26.deviceId = "0"
pciPassthru26.dvs.portgroupId = "dvportgroup-75"
pciPassthru26.id = "00000:026:00.1"
pciPassthru26.pciSlotNumber = "-1"
pciPassthru26.pfId = "00000:026:00.1"
pciPassthru26.present = "FALSE"
pciPassthru26.systemId = "BYPASS"
pciPassthru26.vendorId = "0"
pciPassthru27.deviceId = "0"
pciPassthru27.dvs.portgroupId = "dvportgroup-75"
pciPassthru27.id = "00000:026:00.1"
pciPassthru27.pciSlotNumber = "160"
pciPassthru27.pfId = "00000:026:00.1"
pciPassthru27.present = "TRUE"
pciPassthru27.systemId = "BYPASS"
pciPassthru27.vendorId = "0"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pciSlotNumber = "192"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-75"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pciSlotNumber = "224"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pciSlotNumber = "256"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 8

Requested:

```
unit=7 key=-10 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=8 key=-11 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:ad:a6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=184)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ca:93/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=185) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 18

Requested:

```
unit=7 key=-12 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=18 key=-13 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:24:cc/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=186)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:33:cb/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=187) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 40

Requested:

```
unit=7 key=-14 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=40 key=-15 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:04:d3/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=188)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:6e:36/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=189) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 3

Requested:

```
unit=7 key=-16 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=3 key=-17 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:73:0a/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=190)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ba:77/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=93) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 200

Requested:

```
unit=7 key=-18 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=200 key=-19 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:ff:da/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=94)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:2c:5f/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=95) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: NIC limit: CreateVM with ten vmxnet3 cards plus one SR-IOV card, all UnitNumber nil

Requested:

```
unit=nil (auto) key=-20 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-21 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-22 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-23 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-24 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-25 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-26 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-27 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-28 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-29 controllerKey=0 kind=add VirtualVmxnet3 backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=)
unit=nil (auto) key=-30 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=7 key=4000 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:56:f5/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=96)
unit=8 key=4001 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:04:87/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=191)
unit=9 key=4002 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:bb:75/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=192)
unit=10 key=4003 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:73:d2/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=193)
unit=11 key=4004 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:12:e8/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=194)
unit=12 key=4005 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:57:f6/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=195)
unit=13 key=4006 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:d7:87/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=196)
unit=14 key=4007 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:da:d2/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=197)
unit=15 key=4008 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:d8:04/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=198)
unit=16 key=4009 controllerKey=100 kind=VirtualVmxnet3 mac=00:50:56:8c:2e:b5/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=199)
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:51:95/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=200) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
ethernet0.addressType = "vpx"
ethernet0.dvs.portgroupId = "dvportgroup-75"
ethernet0.present = "TRUE"
ethernet0.virtualDev = "vmxnet3"
ethernet1.addressType = "vpx"
ethernet1.dvs.portgroupId = "dvportgroup-75"
ethernet1.present = "TRUE"
ethernet1.virtualDev = "vmxnet3"
ethernet2.addressType = "vpx"
ethernet2.dvs.portgroupId = "dvportgroup-75"
ethernet2.present = "TRUE"
ethernet2.virtualDev = "vmxnet3"
ethernet3.addressType = "vpx"
ethernet3.dvs.portgroupId = "dvportgroup-75"
ethernet3.present = "TRUE"
ethernet3.virtualDev = "vmxnet3"
ethernet4.addressType = "vpx"
ethernet4.dvs.portgroupId = "dvportgroup-75"
ethernet4.present = "TRUE"
ethernet4.virtualDev = "vmxnet3"
ethernet5.addressType = "vpx"
ethernet5.dvs.portgroupId = "dvportgroup-75"
ethernet5.present = "TRUE"
ethernet5.virtualDev = "vmxnet3"
ethernet6.addressType = "vpx"
ethernet6.dvs.portgroupId = "dvportgroup-75"
ethernet6.present = "TRUE"
ethernet6.virtualDev = "vmxnet3"
ethernet7.addressType = "vpx"
ethernet7.dvs.portgroupId = "dvportgroup-75"
ethernet7.present = "TRUE"
ethernet7.virtualDev = "vmxnet3"
ethernet8.addressType = "vpx"
ethernet8.dvs.portgroupId = "dvportgroup-75"
ethernet8.present = "TRUE"
ethernet8.virtualDev = "vmxnet3"
ethernet9.addressType = "vpx"
ethernet9.dvs.portgroupId = "dvportgroup-75"
ethernet9.present = "TRUE"
ethernet9.virtualDev = "vmxnet3"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Ordering VM: CreateVM with 3 SR-IOV cards on [/PNimbus/network/vlan1868 /PNimbus/network/vlan-1869 /PNimbus/network/trunk-dvpg], in that ConfigSpec order, all UnitNumber nil

Requested:

```
unit=nil (auto) key=-31 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
unit=nil (auto) key=-32 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2635 port=) sriovPF=0000:1a:00.1 sriovVF=
unit=nil (auto) key=-33 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:23:93/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:cc:d1/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2635 port=104) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ff:7e/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201) sriovPF=0000:1a:00.1 sriovVF=
```

config.hardware.device order (unsorted): key=13029 unit=63 VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112); key=13030 unit=64 VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2635 port=104); key=13031 unit=65 VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201)

VMX device lines:

```
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-2637"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-2635"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Ordering VM: remove the middle SR-IOV card (key 13030)

Requested:

```
unit=64 key=13030 controllerKey=100 kind=remove VirtualSriovEthernetCard mac=00:50:56:8c:cc:d1/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2635 port=104) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:23:93/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ff:7e/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-2637"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Ordering VM: add a new SR-IOV card, UnitNumber nil

Requested:

```
unit=nil (auto) key=-34 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:23:93/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ec:88/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=202) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ff:7e/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-2637"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Ordering VM: add an SR-IOV card with explicit device Key 13010, UnitNumber nil

Requested:

```
unit=nil (auto) key=13010 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:19:f9/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=203) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:23:93/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ec:88/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=202) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ff:7e/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-2637"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Ordering VM: add a non-SR-IOV PCI passthrough device DirectPath 0000:1a:01.2 (MT27710 Family [ConnectX-4 Lx Virtual Function]) (config only, never powered on)

Requested:

```
unit=nil (auto) key=-36 controllerKey=0 kind=add VirtualPCIPassthrough backing=VirtualPCIPassthroughDeviceBackingInfo
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=18 key=13000 controllerKey=100 kind=VirtualPCIPassthrough backing=VirtualPCIPassthroughDeviceBackingInfo
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:19:f9/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=203) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:23:93/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ec:88/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=202) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ff:7e/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
pciPassthru0.deviceId = "0x1016"
pciPassthru0.id = "00000:026:01.2"
pciPassthru0.present = "TRUE"
pciPassthru0.systemId = "6a987c42-1f55-b314-09f7-b48351006ea6"
pciPassthru0.vendorId = "0x15b3"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-2637"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

#### Step: Ordering VM: add another SR-IOV card after the passthrough device

Requested:

```
unit=nil (auto) key=-37 controllerKey=0 kind=add VirtualSriovEthernetCard backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=) sriovPF=0000:1a:00.1 sriovVF=
```

Observed:

```
unit=0 key=500 controllerKey=100 kind=VirtualMachineVideoCard
unit=17 key=12000 controllerKey=100 kind=VirtualMachineVMCIDevice
unit=18 key=13000 controllerKey=100 kind=VirtualPCIPassthrough backing=VirtualPCIPassthroughDeviceBackingInfo
unit=61 key=13027 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:07:9d/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=204) sriovPF=0000:1a:00.1 sriovVF=
unit=62 key=13028 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:19:f9/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=203) sriovPF=0000:1a:00.1 sriovVF=
unit=63 key=13029 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:23:93/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112) sriovPF=0000:1a:00.1 sriovVF=
unit=64 key=13030 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ec:88/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=202) sriovPF=0000:1a:00.1 sriovVF=
unit=65 key=13031 controllerKey=100 kind=VirtualSriovEthernetCard mac=00:50:56:8c:ff:7e/assigned backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201) sriovPF=0000:1a:00.1 sriovVF=
```

VMX device lines:

```
pciPassthru0.deviceId = "0x1016"
pciPassthru0.id = "00000:026:01.2"
pciPassthru0.present = "TRUE"
pciPassthru0.systemId = "6a987c42-1f55-b314-09f7-b48351006ea6"
pciPassthru0.vendorId = "0x15b3"
pciPassthru27.deviceId = "0"
pciPassthru27.dvs.portgroupId = "dvportgroup-75"
pciPassthru27.id = "00000:026:00.1"
pciPassthru27.pfId = "00000:026:00.1"
pciPassthru27.present = "TRUE"
pciPassthru27.systemId = "BYPASS"
pciPassthru27.vendorId = "0"
pciPassthru28.deviceId = "0"
pciPassthru28.dvs.portgroupId = "dvportgroup-75"
pciPassthru28.id = "00000:026:00.1"
pciPassthru28.pfId = "00000:026:00.1"
pciPassthru28.present = "TRUE"
pciPassthru28.systemId = "BYPASS"
pciPassthru28.vendorId = "0"
pciPassthru29.deviceId = "0"
pciPassthru29.dvs.portgroupId = "dvportgroup-2637"
pciPassthru29.id = "00000:026:00.1"
pciPassthru29.pfId = "00000:026:00.1"
pciPassthru29.present = "TRUE"
pciPassthru29.systemId = "BYPASS"
pciPassthru29.vendorId = "0"
pciPassthru30.deviceId = "0"
pciPassthru30.dvs.portgroupId = "dvportgroup-75"
pciPassthru30.id = "00000:026:00.1"
pciPassthru30.pfId = "00000:026:00.1"
pciPassthru30.present = "TRUE"
pciPassthru30.systemId = "BYPASS"
pciPassthru30.vendorId = "0"
pciPassthru31.deviceId = "0"
pciPassthru31.dvs.portgroupId = "dvportgroup-75"
pciPassthru31.id = "00000:026:00.1"
pciPassthru31.pfId = "00000:026:00.1"
pciPassthru31.present = "TRUE"
pciPassthru31.systemId = "BYPASS"
pciPassthru31.vendorId = "0"
```

**Findings**:

- Resolved SR-IOV PF from the host ConfigTarget: pnic=vmnic1 id=0000:1a:00.1 deviceId=1015 vendorId=15b3 deviceName="MT27710 Family [ConnectX-4 Lx]".
- CreateVM, SR-IOV UnitNumber nil: platform assigned key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil.
- ReconfigVM_Task Add, SR-IOV UnitNumber nil: platform assigned key=13030 (PCI passthrough keys 13000-13127) unit=64 (PCI passthrough band 38-161) pciSlot=nil.
- ReconfigVM_Task Add, SR-IOV explicit unit 13: explicit unit 13 (ethernet band 7-16) was silently RENUMBERED — key=13029 (PCI passthrough keys 13000-13127) unit=63 (PCI passthrough band 38-161) pciSlot=nil.
- ReconfigVM_Task Add, SR-IOV explicit unit 40: explicit unit 40 (PCI passthrough band 38-161, inside govmomi's SR-IOV naming band 36-45) was silently RENUMBERED — key=13028 (PCI passthrough keys 13000-13127) unit=62 (PCI passthrough band 38-161) pciSlot=nil.
- Collision: vmxnet3 at the first SR-IOV card's unit 62: REJECTED [InvalidArgument] — `ReconfigVM_Task failed: A specified parameter was not correct: unitNumber`.
- Collision: SR-IOV card at the vmxnet3's unit 7: silently RENUMBERED — kind=VirtualSriovEthernetCard key=13027 (PCI passthrough keys 13000-13127) unit=61 (PCI passthrough band 38-161) pciSlot=nil.
- Power cycle (powered off -> powered on): every NIC's key and unit number were unchanged, and no already-assigned PCI slot moved (slots are first assigned at power-on).
- Powered on: SR-IOV card key=13027 unit=61 pciSlot=160, PF "0000:1a:00.1", VF (virtualFunctionBacking not populated in config.hardware).
- Powered on: SR-IOV card key=13028 unit=62 pciSlot=192, PF "0000:1a:00.1", VF (virtualFunctionBacking not populated in config.hardware).
- Powered on: SR-IOV card key=13029 unit=63 pciSlot=224, PF "0000:1a:00.1", VF (virtualFunctionBacking not populated in config.hardware).
- Powered on: SR-IOV card key=13030 unit=64 pciSlot=256, PF "0000:1a:00.1", VF (virtualFunctionBacking not populated in config.hardware).
- Powered on: SR-IOV card key=13031 unit=65 pciSlot=1184, PF "0000:1a:00.1", VF (virtualFunctionBacking not populated in config.hardware).
- Hot-add an SR-IOV card while powered on (informational): REJECTED [GenericVmConfigFault] — `ReconfigVM_Task failed: The hot-plug operation failed. `.
- Power cycle (powered on -> powered off): every NIC's key and unit number were unchanged, and no already-assigned PCI slot moved (slots are first assigned at power-on).
- Remove/re-add: removed SR-IOV card had key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=1184; the new nil-unit card got key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil (freed unit reused: true).
- CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 8: explicit unit 8 (ethernet band 7-16) was silently RENUMBERED — key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil.
- CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 18: explicit unit 18 (PCI passthrough band 18-21) was silently RENUMBERED — key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil.
- CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 40: explicit unit 40 (PCI passthrough band 38-161, inside govmomi's SR-IOV naming band 36-45) was silently RENUMBERED — key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil.
- CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 3: explicit unit 3 (outside every documented band) was silently RENUMBERED — key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil.
- CreateVM, vmxnet3 at unit 7 plus SR-IOV explicit unit 200: explicit unit 200 (outside every documented band) was silently RENUMBERED — key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil.
- NIC limit: ten vmxnet3 + one SR-IOV was ACCEPTED (10 non-SR-IOV NICs observed); SR-IOV card got key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil — SR-IOV does not count against the ten-ethernet-card limit.
- Ordering: SR-IOV card #0 by unit is key=13029 (PCI passthrough keys 13000-13127) unit=63 (PCI passthrough band 38-161) pciSlot=nil backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2637 port=112).
- Ordering: SR-IOV card #1 by unit is key=13030 (PCI passthrough keys 13000-13127) unit=64 (PCI passthrough band 38-161) pciSlot=nil backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-2635 port=104).
- Ordering: SR-IOV card #2 by unit is key=13031 (PCI passthrough keys 13000-13127) unit=65 (PCI passthrough band 38-161) pciSlot=nil backing=VirtualEthernetCardDistributedVirtualPortBackingInfo(portgroup=dvportgroup-75 port=201).
- Gap fill: removed middle card key=13030 (PCI passthrough keys 13000-13127) unit=64 (PCI passthrough band 38-161) pciSlot=nil; the new nil-unit card got key=13030 (PCI passthrough keys 13000-13127) unit=64 (PCI passthrough band 38-161) pciSlot=nil (gap filled: true).
- Explicit Key: explicit device Key 13010, UnitNumber nil -> key=13028 (PCI passthrough keys 13000-13127) unit=62 (PCI passthrough band 38-161) pciSlot=nil (Key honoured: false).
- Passthrough pool: non-SR-IOV passthrough DirectPath 0000:1a:01.2 (MT27710 Family [ConnectX-4 Lx Virtual Function]) got kind=VirtualPCIPassthrough key=13000 (PCI passthrough keys 13000-13127) unit=18 (PCI passthrough band 18-21) pciSlot=nil.
- Passthrough pool: the next SR-IOV card got key=13027 (PCI passthrough keys 13000-13127) unit=61 (PCI passthrough band 38-161) pciSlot=nil.
- Summary across 16 distinct SR-IOV cards (per VM and device key): 0 inside the 7-16 band, 16 outside it; 0 with 4000-series keys, 16 with other keys.
- VERDICT: SR-IOV cards do NOT share the 7-16 ethernet unit band or the 4000-series keys; the spec's Q3 assumption is wrong and the plan must treat SR-IOV separately.
