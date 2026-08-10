// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/vmware/govmomi/find"

	vcclient "github.com/vmware-tanzu/vm-operator/pkg/util/vsphere/client"
)

// resolveDatacenterID resolves the datacenter to connect to: datacenterName
// if given, else the first datacenter found (very few testbeds have more
// than one). It returns the datacenter's managed object ID, which is what
// vcclient.Config.Datacenter expects (not a name).
//
// This connects to vCenter once on its own, via vcclient.NewVimClient,
// which -- unlike vcclient.NewClient -- doesn't require a datacenter to
// already be known (vcclient.NewClient needs one up front to scope its
// Finder). The temporary session is logged out before returning; the
// caller makes its own real connection afterward with the resolved
// datacenter filled in.
func resolveDatacenterID(ctx context.Context, cfg vcclient.Config, datacenterName string) (string, error) {
	vimClient, sm, err := vcclient.NewVimClient(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to connect to vCenter %q to resolve datacenter: %w", cfg.Host, err)
	}
	defer func() {
		if err := sm.Logout(ctx); err != nil {
			log.Printf("warning: failed to log out temporary session used to resolve datacenter: %v", err)
		}
	}()

	// "/..." recurses through any inventory folders, in case a testbed
	// nests its datacenter(s) rather than placing them at the root. A bare
	// "..." (no leading slash) is not treated as a path by the finder --
	// it's looked up as a literal object name instead, which fails.
	datacenters, err := find.NewFinder(vimClient, false).DatacenterList(ctx, "/...")
	if err != nil {
		return "", fmt.Errorf("failed to list datacenters: %w", err)
	}

	if len(datacenters) == 0 {
		return "", fmt.Errorf("no datacenters found in vCenter %q", cfg.Host)
	}

	if datacenterName == "" {
		chosen := datacenters[0]
		log.Printf("no -datacenter specified, defaulting to %q", chosen.Name())
		return chosen.Reference().Value, nil
	}

	for _, dc := range datacenters {
		if dc.Name() == datacenterName {
			return dc.Reference().Value, nil
		}
	}

	return "", fmt.Errorf("datacenter %q not found", datacenterName)
}
