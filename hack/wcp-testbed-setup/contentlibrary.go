// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/vmware/govmomi/vapi/library"

	"github.com/vmware-tanzu/vm-operator/pkg/util/ptr"
	vcclient "github.com/vmware-tanzu/vm-operator/pkg/util/vsphere/client"
)

// candidateDatastoreNames lists datastore names tried, in order, when no
// explicit datastore is given for the content library's storage backing.
// This mirrors the candidate list used by the e2e framework's
// EnsureVMServiceContentLibrary.
var candidateDatastoreNames = []string{"vsanDatastore", "sharedVmfs-0", "nfs0-1"}

const (
	libraryItemPollInterval = 10 * time.Second
	libraryItemPollTimeout  = 5 * time.Minute
)

// EnsureVMServiceContentLibrary ensures a subscribed content library with
// the given name exists, creating and syncing it from subscriptionURL if it
// does not. It returns the library's ID.
func EnsureVMServiceContentLibrary(
	ctx context.Context,
	c *vcclient.Client,
	name string,
	subscriptionURL string,
	datastoreName string,
) (string, error) {
	libMgr := library.NewManager(c.RestClient())

	if existing, err := libMgr.GetLibraryByName(ctx, name); err == nil {
		log.Printf("content library %q already exists with ID %q, reusing it", name, existing.ID)
		return existing.ID, nil
	}

	log.Printf("creating content library %q subscribed to %q", name, subscriptionURL)

	dsID, err := resolveDatastoreID(ctx, c, datastoreName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve datastore for content library %q: %w", name, err)
	}

	newLib := library.Library{
		Name: name,
		Type: "SUBSCRIBED",
		Storage: []library.StorageBacking{
			{
				DatastoreID: dsID,
				Type:        "DATASTORE",
			},
		},
		Subscription: &library.Subscription{
			SubscriptionURL:      subscriptionURL,
			AuthenticationMethod: "NONE",
			OnDemand:             ptr.To(true),
			AutomaticSyncEnabled: ptr.To(true),
		},
	}

	// CreateLibrary automatically computes the subscription's SSL
	// thumbprint from the target host's certificate when the client's
	// transport is configured to skip TLS verification (-insecure, the
	// default), so no manual thumbprint fetch is needed here. This relies
	// on rest.NewClient reusing the vim25 client's *http.Transport
	// (govmomi/vapi/rest/client.go NewClient -> soap.Client.NewServiceClient),
	// which carries over the InsecureSkipVerify setting from client.Config.
	id, err := libMgr.CreateLibrary(ctx, newLib)
	if err != nil {
		return "", fmt.Errorf("failed to create content library %q: %w", name, err)
	}

	log.Printf("created content library %q with ID %q", name, id)

	if err := libMgr.SyncLibrary(ctx, &library.Library{ID: id}); err != nil {
		return "", fmt.Errorf("failed to sync content library %q: %w", name, err)
	}

	log.Printf("waiting for content library %q to finish synchronizing", name)
	if err := waitForLibraryItems(ctx, libMgr, id); err != nil {
		return "", fmt.Errorf("content library %q did not finish synchronizing: %w", name, err)
	}

	log.Printf("content library %q synchronization finished", name)

	return id, nil
}

// ResolveContentLibraryID looks up an existing content library by name and
// returns its ID.
func ResolveContentLibraryID(ctx context.Context, c *vcclient.Client, name string) (string, error) {
	libMgr := library.NewManager(c.RestClient())

	lib, err := libMgr.GetLibraryByName(ctx, name)
	if err != nil {
		return "", fmt.Errorf("failed to find content library %q: %w", name, err)
	}

	return lib.ID, nil
}

// resolveDatastoreID resolves the datastore to back a content library's
// storage. If datastoreName is non-empty, that datastore is used; otherwise
// the first match from candidateDatastoreNames is used.
func resolveDatastoreID(ctx context.Context, c *vcclient.Client, datastoreName string) (string, error) {
	if datastoreName != "" {
		ds, err := c.Finder().Datastore(ctx, datastoreName)
		if err != nil {
			return "", fmt.Errorf("failed to find datastore %q: %w", datastoreName, err)
		}
		return ds.Reference().Value, nil
	}

	datastores, err := c.Finder().DatastoreList(ctx, "*")
	if err != nil {
		return "", fmt.Errorf("failed to list datastores: %w", err)
	}

	byName := make(map[string]string, len(datastores))
	for _, ds := range datastores {
		byName[ds.Name()] = ds.Reference().Value
	}

	for _, name := range candidateDatastoreNames {
		if id, ok := byName[name]; ok {
			return id, nil
		}
	}

	return "", fmt.Errorf("no suitable datastore found among candidates %v", candidateDatastoreNames)
}

// waitForLibraryItems polls until the given content library has at least
// one synchronized item, or the timeout elapses.
func waitForLibraryItems(ctx context.Context, libMgr *library.Manager, libraryID string) error {
	deadline := time.Now().Add(libraryItemPollTimeout)

	for {
		items, err := libMgr.ListLibraryItems(ctx, libraryID)
		if err == nil && len(items) > 0 {
			return nil
		}

		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("timed out waiting for library items: %w", err)
			}
			return fmt.Errorf("timed out waiting for library items to appear")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(libraryItemPollInterval):
		}
	}
}
