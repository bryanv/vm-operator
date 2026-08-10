// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vmware/govmomi/pbm"
	"github.com/vmware/govmomi/vapi/namespace"

	vcclient "github.com/vmware-tanzu/vm-operator/pkg/util/vsphere/client"
)

const (
	namespacePollInterval = 5 * time.Second
	namespacePollTimeout  = 5 * time.Minute
	namespaceRunningState = "RUNNING"

	// storageLimitMiB is the per-policy storage quota applied to every
	// storage policy associated with the namespace, matching the e2e
	// framework's defaultNamespaceStorageQuotaMiB
	// (test/e2e/infrastructure/vsphere/wcp/api.go).
	storageLimitMiB = int64(500 * 1024)
)

// NamespaceOptions describes the desired state of the namespace to create.
type NamespaceOptions struct {
	Name             string
	ClusterName      string   // empty means "the first Namespaces-enabled cluster found"
	Zones            []string // empty means "all zones associated with the cluster, if more than one"
	VMClasses        []string // empty means "all VM classes"
	ContentLibraryID string   // ID of the content library to associate; already resolved by the caller
	StoragePolicies  []string
}

// CreateAndAssociateNamespace creates a WCP namespace per opts, associating
// it with the requested (or default) VM classes, content library, and
// storage policies. If the namespace already exists, this is a no-op. It
// returns whether it actually created the namespace (false if it already
// existed), so callers can report accurate status.
func CreateAndAssociateNamespace(ctx context.Context, c *vcclient.Client, opts NamespaceOptions) (bool, error) {
	nsMgr := namespace.NewManager(c.RestClient())

	if _, err := nsMgr.GetNamespace(ctx, opts.Name); err == nil {
		log.Printf("namespace %q already exists, skipping creation", opts.Name)
		return false, nil
	}

	clusterID, clusterName, err := resolveClusterID(ctx, nsMgr, opts.ClusterName)
	if err != nil {
		return false, err
	}

	zonesToBind := resolveZonesToBind(c, clusterID, clusterName, opts.Zones)

	vmClassIDs, err := resolveVMClasses(ctx, nsMgr, opts.VMClasses)
	if err != nil {
		return false, err
	}

	storageSpecs, err := resolveStorageSpecs(ctx, c, opts.StoragePolicies)
	if err != nil {
		return false, err
	}

	log.Printf("creating namespace %q on cluster %q with %d VM class(es), content library %q, and %d storage policy(ies)",
		opts.Name, clusterName, len(vmClassIDs), opts.ContentLibraryID, len(storageSpecs))

	spec := namespace.NamespacesInstanceCreateSpec{
		Cluster:   clusterID,
		Namespace: opts.Name,
		VmServiceSpec: namespace.VmServiceSpec{
			ContentLibraries: []string{opts.ContentLibraryID},
			VmClasses:        vmClassIDs,
		},
		StorageSpecs: storageSpecs,
	}

	if err := nsMgr.CreateNamespace(ctx, spec); err != nil {
		// Tolerate a namespace that appeared between our GetNamespace check
		// and this call (e.g. a concurrent run), mirroring the e2e DCLI
		// client's handling of the same race.
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			log.Printf("namespace %q was created concurrently, skipping the rest of setup", opts.Name)
			return false, nil
		}
		return false, fmt.Errorf("failed to create namespace %q: %w", opts.Name, err)
	}

	log.Printf("waiting for namespace %q to become %s", opts.Name, namespaceRunningState)
	if err := waitForNamespaceReady(ctx, nsMgr, opts.Name); err != nil {
		return false, fmt.Errorf("namespace %q did not become ready: %w", opts.Name, err)
	}

	log.Printf("namespace %q is ready", opts.Name)

	bindNamespaceZones(ctx, c, opts.Name, zonesToBind)

	return true, nil
}

// resolveClusterID looks up a Namespaces-enabled cluster by name, or, if
// clusterName is empty, picks the first Namespaces-enabled cluster found.
// Using ListClusters (rather than a raw inventory search) doubles as a
// check that vSphere Namespaces (WCP) is already enabled on the target
// cluster. It returns the cluster's ID and display name.
func resolveClusterID(ctx context.Context, nsMgr *namespace.Manager, clusterName string) (string, string, error) {
	clusters, err := nsMgr.ListClusters(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to list Namespaces-enabled clusters: %w", err)
	}

	if len(clusters) == 0 {
		return "", "", fmt.Errorf(
			"no Namespaces-enabled clusters found (is vSphere Namespaces/WCP enabled anywhere in this vCenter?)")
	}

	if clusterName == "" {
		chosen := clusters[0]
		log.Printf("no -cluster specified, defaulting to %q", chosen.Name)
		return chosen.ID, chosen.Name, nil
	}

	for _, cl := range clusters {
		if cl.Name == clusterName {
			return cl.ID, cl.Name, nil
		}
	}

	return "", "", fmt.Errorf(
		"cluster %q is not a Namespaces-enabled cluster (is vSphere Namespaces/WCP enabled on it?)",
		clusterName)
}

// resolveZonesToBind determines which vSphere Zones (if any) the namespace
// should be bound to: zoneNames if the caller specified any explicitly;
// otherwise all zones associated with the cluster, but only if there is
// more than one (a single- or non-zoned cluster needs no explicit binding,
// since a namespace is bound to its cluster's implicit default zone
// automatically). Failing to determine the cluster's zones is logged as a
// warning, not a hard error, since zone binding is best-effort overall (see
// bindNamespaceZones).
func resolveZonesToBind(c *vcclient.Client, clusterID, clusterName string, zoneNames []string) []string {
	if len(zoneNames) > 0 {
		return zoneNames
	}

	availableZones, err := resolveClusterZones(c, clusterID)
	if err != nil {
		log.Printf("warning: failed to determine vSphere Zones for cluster %q, skipping zone binding: %v", clusterName, err)
		return nil
	}

	if len(availableZones) <= 1 {
		return nil
	}

	log.Printf("cluster %q spans %d zones, defaulting to all of them: %v", clusterName, len(availableZones), availableZones)

	return availableZones
}

// resolveVMClasses returns the VM class IDs to associate with the
// namespace. If names is empty, every VM class known to vCenter is
// returned.
func resolveVMClasses(ctx context.Context, nsMgr *namespace.Manager, names []string) ([]string, error) {
	if len(names) > 0 {
		return names, nil
	}

	classes, err := nsMgr.ListVmClasses(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list VM classes: %w", err)
	}

	ids := make([]string, 0, len(classes))
	for _, class := range classes {
		ids = append(ids, class.Id)
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("no VM classes found in vCenter")
	}

	return ids, nil
}

// resolveStorageSpecs resolves each named storage policy to its PBM profile
// ID, warning and skipping (rather than failing) any policy that cannot be
// found. It fails only if none of the requested policies resolve.
func resolveStorageSpecs(ctx context.Context, c *vcclient.Client, policyNames []string) ([]namespace.StorageSpec, error) {
	pbmClient, err := pbm.NewClient(ctx, c.VimClient())
	if err != nil {
		return nil, fmt.Errorf("failed to create PBM client: %w", err)
	}

	specs := make([]namespace.StorageSpec, 0, len(policyNames))

	for _, name := range policyNames {
		id, err := pbmClient.ProfileIDByName(ctx, name)
		if err != nil {
			log.Printf("warning: storage policy %q not found, skipping: %v", name, err)
			continue
		}

		specs = append(specs, namespace.StorageSpec{
			Policy: id,
			Limit:  storageLimitMiB,
		})
	}

	if len(specs) == 0 {
		return nil, fmt.Errorf("none of the requested storage policies were found: %s", strings.Join(policyNames, ", "))
	}

	return specs, nil
}

// waitForNamespaceReady polls the namespace until its config status is
// RUNNING, or the timeout elapses.
func waitForNamespaceReady(ctx context.Context, nsMgr *namespace.Manager, name string) error {
	deadline := time.Now().Add(namespacePollTimeout)

	for {
		info, err := nsMgr.GetNamespace(ctx, name)
		if err == nil && info.ConfigStatus == namespaceRunningState {
			return nil
		}

		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("timed out waiting for namespace: %w", err)
			}
			return fmt.Errorf("timed out waiting for namespace, last config status: %q", info.ConfigStatus)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(namespacePollInterval):
		}
	}
}
