// Copyright (c) 2023 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package vmlifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"
	"text/template"

	"github.com/vmware-tanzu/vm-operator/api/v1alpha1"
	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	"github.com/vmware-tanzu/vm-operator/pkg/context"
	"github.com/vmware-tanzu/vm-operator/pkg/vmprovider/providers/vsphere2/constants"
)

func GetTemplateRenderFunc(
	vmCtx context.VirtualMachineContextA2,
	bsArgs *BootstrapArgs,
) TemplateRenderFunc {

	networkDevicesStatus := toTemplateNetworkStatus(bsArgs)
	networkStatus := vmopv1.NetworkStatus{
		Devices:     networkDevicesStatus,
		Nameservers: bsArgs.DNSServers,
	}

	networkDevicesStatusV1A1 := toTemplateNetworkStatusV1A1(bsArgs)
	networkStatusV1A1 := v1alpha1.NetworkStatus{
		Devices:     networkDevicesStatusV1A1,
		Nameservers: bsArgs.DNSServers,
	}

	// Oh dear. The VM itself really should not have been included here.
	v1a1VM := &v1alpha1.VirtualMachine{}
	_ = v1a1VM.ConvertFrom(vmCtx.VM)

	templateData := struct {
		V1alpha1 v1alpha1.VirtualMachineTemplate
		V1alpha2 vmopv1.VirtualMachineTemplate
	}{
		V1alpha1: v1alpha1.VirtualMachineTemplate{
			Net: networkStatusV1A1,
			VM:  v1a1VM,
		},
		V1alpha2: vmopv1.VirtualMachineTemplate{
			Net: networkStatus,
			VM:  vmCtx.VM,
		},
	}

	// Get the first IP address from the first NIC.
	v1alpha1FirstIP := func() (string, error) {
		if len(networkDevicesStatus) == 0 {
			return "", errors.New("no available network device, check with VI admin")
		}
		return networkDevicesStatus[0].IPAddresses[0], nil
	}

	// Get the first NIC's MAC address.
	v1alpha1FirstNicMacAddr := func() (string, error) {
		if len(networkDevicesStatus) == 0 {
			return "", errors.New("no available network device, check with VI admin")
		}
		return networkDevicesStatus[0].MacAddress, nil
	}

	// Get the first IP address from the ith NIC.
	// if index out of bound, throw an error and template string won't be parsed
	v1alpha1FirstIPFromNIC := func(index int) (string, error) {
		if len(networkDevicesStatus) == 0 {
			return "", errors.New("no available network device, check with VI admin")
		}
		if index >= len(networkDevicesStatus) {
			return "", errors.New("index out of bound")
		}
		return networkDevicesStatus[index].IPAddresses[0], nil
	}

	// Get all IP addresses from the ith NIC.
	// if index out of bound, throw an error and template string won't be parsed
	v1alpha1IPsFromNIC := func(index int) ([]string, error) {
		if len(networkDevicesStatus) == 0 {
			return []string{""}, errors.New("no available network device, check with VI admin")
		}
		if index >= len(networkDevicesStatus) {
			return []string{""}, errors.New("index out of bound")
		}
		return networkDevicesStatus[index].IPAddresses, nil
	}

	// Get subnet mask from a CIDR notation IP address and prefix length
	// if IP address and prefix length not valid, throw an error and template string won't be parsed
	v1alpha1SubnetMask := func(cidr string) (string, error) {
		_, ipv4Net, err := net.ParseCIDR(cidr)
		if err != nil {
			return "", err
		}
		netmask := fmt.Sprintf("%d.%d.%d.%d", ipv4Net.Mask[0], ipv4Net.Mask[1], ipv4Net.Mask[2], ipv4Net.Mask[3])
		return netmask, nil
	}

	// Format an IP address with default netmask CIDR
	// if IP not valid, throw an error and template string won't be parsed
	v1alpha1IP := func(IP string) (string, error) {
		if net.ParseIP(IP) == nil {
			return "", errors.New("input IP address not valid")
		}
		defaultMask := net.ParseIP(IP).DefaultMask()
		ones, _ := defaultMask.Size()
		expectedCidrNotation := IP + "/" + fmt.Sprintf("%d", int32(ones))
		return expectedCidrNotation, nil
	}

	// Format an IP address with network length(eg. /24) or decimal
	// notation (eg. 255.255.255.0). Format an IP/CIDR with updated mask.
	// An empty mask causes just the IP to be returned.
	v1alpha1FormatIP := func(s string, mask string) (string, error) {

		// Get the IP address for the input string.
		ip, _, err := net.ParseCIDR(s)
		if err != nil {
			ip = net.ParseIP(s)
			if ip == nil {
				return "", fmt.Errorf("input IP address not valid")
			}
		}
		// Store the IP as a string back into s.
		s = ip.String()

		// If no mask was provided then return just the IP.
		if mask == "" {
			return s, nil
		}

		// The provided mask is a network length.
		if strings.HasPrefix(mask, "/") {
			s += mask
			if _, _, err := net.ParseCIDR(s); err != nil {
				return "", err
			}
			return s, nil
		}

		// The provided mask is subnet mask.
		maskIP := net.ParseIP(mask)
		if maskIP == nil {
			return "", fmt.Errorf("mask is an invalid IP")
		}

		maskIPBytes := maskIP.To4()
		if len(maskIPBytes) == 0 {
			maskIPBytes = maskIP.To16()
		}

		ipNet := net.IPNet{
			IP:   ip,
			Mask: net.IPMask(maskIPBytes),
		}
		s = ipNet.String()

		// Validate the ipNet is an IP/CIDR
		if _, _, err := net.ParseCIDR(s); err != nil {
			return "", fmt.Errorf("invalid ip net: %s", s)
		}

		return s, nil
	}

	// Format the first occurred count of nameservers with specific delimiter
	// A negative count number would mean format all nameservers
	v1alpha1FormatNameservers := func(count int, delimiter string) (string, error) {
		var nameservers []string
		if len(networkStatus.Nameservers) == 0 {
			return "", errors.New("no available nameservers, check with VI admin")
		}
		if count < 0 || count >= len(networkStatus.Nameservers) {
			nameservers = networkStatus.Nameservers
			return strings.Join(nameservers, delimiter), nil
		}
		nameservers = networkStatus.Nameservers[:count]
		return strings.Join(nameservers, delimiter), nil
	}

	funcMap := template.FuncMap{
		constants.V1alpha1FirstIPFromNIC:    v1alpha1FirstIPFromNIC,
		constants.V1alpha1FirstIP:           v1alpha1FirstIP,
		constants.V1alpha1FirstNicMacAddr:   v1alpha1FirstNicMacAddr,
		constants.V1alpha1IPsFromNIC:        v1alpha1IPsFromNIC,
		constants.V1alpha1FormatIP:          v1alpha1FormatIP,
		constants.V1alpha1IP:                v1alpha1IP,
		constants.V1alpha1SubnetMask:        v1alpha1SubnetMask,
		constants.V1alpha1FormatNameservers: v1alpha1FormatNameservers,
	}

	// skip parsing when encountering escape character('\{',"\}")
	normalizeStr := func(str string) string {
		if strings.Contains(str, "\\{") || strings.Contains(str, "\\}") {
			str = strings.ReplaceAll(str, "\\{", "{")
			str = strings.ReplaceAll(str, "\\}", "}")
		}
		return str
	}

	// TODO: Don't log, return errors instead.
	renderTemplate := func(name, templateStr string) string {
		templ, err := template.New(name).Funcs(funcMap).Parse(templateStr)
		if err != nil {
			vmCtx.Logger.Error(err, "failed to parse template", "templateStr", templateStr)
			return normalizeStr(templateStr)
		}
		var doc bytes.Buffer
		err = templ.Execute(&doc, &templateData)
		if err != nil {
			vmCtx.Logger.Error(err, "failed to execute template", "templateStr", templateStr)
			return normalizeStr(templateStr)
		}
		return normalizeStr(doc.String())
	}

	return renderTemplate
}

func toTemplateNetworkStatus(bsArgs *BootstrapArgs) []vmopv1.NetworkDeviceStatus {
	networkDevicesStatus := make([]vmopv1.NetworkDeviceStatus, 0, len(bsArgs.NetworkResults.Results))

	for _, result := range bsArgs.NetworkResults.Results {
		// When using Sysprep, the MAC address must be in the format of "-".
		// CloudInit normalizes it again to ":" when adding it to the netplan.
		macAddr := strings.ReplaceAll(result.MacAddress, ":", "-")

		status := vmopv1.NetworkDeviceStatus{
			MacAddress: macAddr,
		}

		for _, ipConfig := range result.IPConfigs {
			// We mostly only did IPv4 before so keep that going.
			if ipConfig.IsIPv4 {
				if status.Gateway4 == "" {
					status.Gateway4 = ipConfig.Gateway
				}

				status.IPAddresses = append(status.IPAddresses, ipConfig.IPCIDR)
			}
		}

		networkDevicesStatus = append(networkDevicesStatus, status)
	}

	return networkDevicesStatus
}

func toTemplateNetworkStatusV1A1(bsArgs *BootstrapArgs) []v1alpha1.NetworkDeviceStatus {
	networkDevicesStatus := make([]v1alpha1.NetworkDeviceStatus, 0, len(bsArgs.NetworkResults.Results))

	for _, result := range bsArgs.NetworkResults.Results {
		// When using Sysprep, the MAC address must be in the format of "-".
		// CloudInit normalizes it again to ":" when adding it to the netplan.
		macAddr := strings.ReplaceAll(result.MacAddress, ":", "-")

		status := v1alpha1.NetworkDeviceStatus{
			MacAddress: macAddr,
		}

		for _, ipConfig := range result.IPConfigs {
			// We mostly only did IPv4 before so keep that going.
			if ipConfig.IsIPv4 {
				if status.Gateway4 == "" {
					status.Gateway4 = ipConfig.Gateway
				}

				status.IPAddresses = append(status.IPAddresses, ipConfig.IPCIDR)
			}
		}

		networkDevicesStatus = append(networkDevicesStatus, status)
	}

	return networkDevicesStatus
}
