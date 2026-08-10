// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Command wcp-testbed-setup automates day-1 setup of a WCP testbed: it
// ensures the "vmservice" content library exists, creates a new Supervisor
// namespace, and associates VM classes, a content library, and storage
// policies with it. It talks to vCenter directly via govmomi (vapi/library,
// vapi/namespace, pbm) -- no SSH or DCLI involved.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	vcclient "github.com/vmware-tanzu/vm-operator/pkg/util/vsphere/client"
)

const (
	vmServiceContentLibraryName = "vmservice"
	defaultVimPort              = "443"
	defaultSubscriptionURL      = "https://wp-content-pstg.broadcom.com/vmsvc/lib.json"
	defaultStoragePolicies      = "wcpglobal-storage-profile,vm-encryption-policy"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("wcp-testbed-setup: %v", err)
	}
}

func run() error {
	var (
		testbedSource      = flag.String("testbed", "", "path or http(s) URL to testbed.json (required)")
		namespaceName      = flag.String("namespace", "", "name of the namespace to create (required)")
		clusterName        = flag.String("cluster", "", "name of the Namespaces-enabled cluster to create the namespace on; empty means the first one found")
		datacenterName     = flag.String("datacenter", "", "name of the datacenter containing the cluster; empty means the first one found")
		zonesFlag          = flag.String("zones", "", "comma-separated vSphere Zone names to bind the namespace to; empty means all zones associated with the cluster, if it spans more than one")
		vmClassesFlag      = flag.String("vm-classes", "", "comma-separated VM class IDs (e.g. best-effort-small) to associate; empty means all VM classes")
		contentLibraryFlag = flag.String("content-library", "", "name of an existing content library to associate; empty means the ensured \""+vmServiceContentLibraryName+"\" library")
		storagePolicies    = flag.String("storage-policies", defaultStoragePolicies, "comma-separated storage policy names to associate")
		subscriptionURL    = flag.String("cl-subscription-url", defaultSubscriptionURL, "subscription URL for the \""+vmServiceContentLibraryName+"\" content library")
		datastoreName      = flag.String("datastore", "", "datastore to back the \""+vmServiceContentLibraryName+"\" content library; empty means auto-pick")
		insecure           = flag.Bool("insecure", true, "skip TLS certificate verification")
	)
	flag.Parse()

	var missing []string
	for name, val := range map[string]string{
		"testbed":   *testbedSource,
		"namespace": *namespaceName,
	} {
		if val == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		flag.Usage()
		return fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
	}

	ctx := context.Background()

	tb, err := LoadTestbedConfig(ctx, *testbedSource)
	if err != nil {
		return fmt.Errorf("failed to load testbed config: %w", err)
	}
	if tb.VCHost == "" {
		return fmt.Errorf("could not determine vCenter host from testbed config %q", *testbedSource)
	}

	baseCfg := vcclient.Config{
		Host:     tb.VCHost,
		Port:     defaultVimPort,
		Username: tb.VimUsername,
		Password: tb.VimPassword,
		Insecure: *insecure,
	}

	dcID, err := resolveDatacenterID(ctx, baseCfg, *datacenterName)
	if err != nil {
		return fmt.Errorf("failed to resolve datacenter: %w", err)
	}

	cfg := baseCfg
	cfg.Datacenter = dcID

	c, err := vcclient.NewClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to vCenter %q: %w", tb.VCHost, err)
	}
	defer c.Logout(ctx)

	// Only ensure the "vmservice" content library when it's actually going
	// to be used -- i.e. the caller didn't point -content-library at some
	// other, already-existing library.
	var clID string
	if *contentLibraryFlag == "" {
		clID, err = EnsureVMServiceContentLibrary(ctx, c, vmServiceContentLibraryName, *subscriptionURL, *datastoreName)
		if err != nil {
			return fmt.Errorf("failed to ensure %q content library: %w", vmServiceContentLibraryName, err)
		}
	} else {
		clID, err = ResolveContentLibraryID(ctx, c, *contentLibraryFlag)
		if err != nil {
			return err
		}
	}

	opts := NamespaceOptions{
		Name:             *namespaceName,
		ClusterName:      *clusterName,
		Zones:            splitAndTrim(*zonesFlag),
		VMClasses:        splitAndTrim(*vmClassesFlag),
		ContentLibraryID: clID,
		StoragePolicies:  splitAndTrim(*storagePolicies),
	}

	created, err := CreateAndAssociateNamespace(ctx, c, opts)
	if err != nil {
		return fmt.Errorf("failed to set up namespace %q: %w", *namespaceName, err)
	}

	if created {
		log.Printf("done: namespace %q was created with content library %q", *namespaceName, clID)
	} else {
		log.Printf("done: namespace %q already existed, no associations were changed", *namespaceName)
	}

	return nil
}

// splitAndTrim splits s on commas and trims whitespace from each element,
// dropping empty elements. An empty or all-whitespace s yields nil.
func splitAndTrim(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}
