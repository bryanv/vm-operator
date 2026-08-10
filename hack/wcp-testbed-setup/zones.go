// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"

	"github.com/vmware/govmomi/vapi/vcenter/consumptiondomains/associations"
	"github.com/vmware/govmomi/vapi/vcenter/consumptiondomains/zones"

	vcclient "github.com/vmware-tanzu/vm-operator/pkg/util/vsphere/client"
)

// namespacesInstancesPath mirrors the unexported
// govmomi/vapi/namespace/internal.NamespacesPath constant, which can't be
// imported here since it's internal to that module's own package subtree.
const namespacesInstancesPath = "/api/vcenter/namespaces/instances"

// zoneUpdateSpec is a minimal partial-update body used to bind a namespace
// to a set of vSphere Zones. The "zones" field is an unreleased/experimental
// vAPI field -- the e2e DCLI client only reaches it via a "+show-unreleased"
// flag on the same namespace update command -- and isn't present in
// govmomi's typed NamespacesInstanceUpdateSpec, so it's sent as a
// standalone raw PATCH via bindNamespaceZones rather than through
// namespace.Manager.UpdateNamespace.
type zoneUpdateSpec struct {
	Zones []zoneSpec `json:"zones"`
}

type zoneSpec struct {
	Name string `json:"name"`
}

// resolveClusterZones returns the vSphere Zone names associated with the
// given cluster ID, via govmomi's consumption-domains zones/associations
// APIs. It returns an empty (nil) slice, not an error, when zones aren't
// configured or the cluster isn't associated with any -- the common case
// for a single-cluster (non-zoned) Supervisor.
//
// Note: govmomi's zones.Manager and associations.Manager methods take no
// context parameter (they use context.Background() internally), unlike the
// rest of the vapi clients used in this tool.
func resolveClusterZones(c *vcclient.Client, clusterID string) ([]string, error) {
	zonesMgr := zones.NewManager(c.RestClient())

	allZones, err := zonesMgr.ListZones()
	if err != nil {
		return nil, fmt.Errorf("failed to list vSphere Zones: %w", err)
	}

	assocMgr := associations.NewManager(c.RestClient())

	var matched []string
	for _, z := range allZones {
		clusterIDs, err := assocMgr.GetAssociations(z.Zone)
		if err != nil {
			log.Printf("warning: failed to get cluster associations for zone %q, skipping it: %v", z.Zone, err)
			continue
		}
		if slices.Contains(clusterIDs, clusterID) {
			matched = append(matched, z.Zone)
		}
	}

	return matched, nil
}

// bindNamespaceZones binds the given namespace to zoneNames. It is
// best-effort: since namespace-to-zone binding relies on an
// unreleased/experimental vAPI field (see zoneUpdateSpec) that may not be
// available on every vCenter build, a failure here is logged as a warning
// rather than failing the run -- the namespace is already fully set up via
// the stable API by the time this is called.
func bindNamespaceZones(ctx context.Context, c *vcclient.Client, name string, zoneNames []string) {
	if len(zoneNames) == 0 {
		return
	}

	specs := make([]zoneSpec, 0, len(zoneNames))
	for _, z := range zoneNames {
		specs = append(specs, zoneSpec{Name: z})
	}

	log.Printf("binding namespace %q to zone(s): %v", name, zoneNames)

	resource := c.RestClient().Resource(namespacesInstancesPath).WithSubpath(name)
	req := resource.Request(http.MethodPatch, zoneUpdateSpec{Zones: specs})
	if err := c.RestClient().Do(ctx, req, nil); err != nil {
		log.Printf("warning: failed to bind namespace %q to zone(s) %v "+
			"(this vCenter build may not support namespace zone binding): %v",
			name, zoneNames, err)
	}
}
