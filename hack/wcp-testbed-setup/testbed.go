// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// TestbedConfig holds the subset of testbed.json fields this tool needs to
// establish a vSphere API session: the vCenter host and vim (SSO)
// credentials. It deliberately omits SSH/root credentials, which this tool
// never uses.
type TestbedConfig struct {
	VCHost      string
	VimUsername string
	VimPassword string
}

// vcEntry models a single entry of the testbed.json `.vc` field, which may
// appear as either a JSON array or a JSON object keyed by run ID depending
// on how the testbed was provisioned. All fields are optional; callers fall
// back through a priority list, mirroring hack/e2e/setup-testbed-env.sh's
// _parse_vc_credentials.
type vcEntry struct {
	IP4             string `json:"ip4"`
	IP              string `json:"ip"`
	VCenterIP       string `json:"vcenter_ip"`
	Username        string `json:"username"`
	VCenterUsername string `json:"vcenter_username"`
	VimUsername     string `json:"vimUsername"`
	Password        string `json:"password"`
	VCenterPassword string `json:"vcenter_password"`
	VimPassword     string `json:"vimPassword"`
}

func (e vcEntry) host() string {
	return firstNonEmpty(e.IP4, e.IP, e.VCenterIP)
}

func (e vcEntry) username() string {
	return firstNonEmpty(e.VimUsername, e.Username, e.VCenterUsername, "administrator@vsphere.local")
}

func (e vcEntry) password() string {
	return firstNonEmpty(e.VimPassword, e.Password, e.VCenterPassword)
}

// testbedDoc models the top-level testbed.json fields relevant to VC
// credential resolution across all known testbed.json shapes.
type testbedDoc struct {
	DeliverableBlob json.RawMessage `json:"deliverable_blob"`

	VC json.RawMessage `json:"vc"`

	VCenterIP       string `json:"vcenter_ip"`
	VCenterUsername string `json:"vcenter_username"`
	VCenterPassword string `json:"vcenter_password"`

	VCIP       string `json:"vc_ip"`
	VCUsername string `json:"vc_username"`
	VCPassword string `json:"vc_password"`

	TestbedIP string `json:"testbed_ip"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// LoadTestbedConfig reads testbed.json from a local file path or an
// http(s) URL, unwraps an optional `.deliverable_blob` envelope (used when
// the file is fetched from UTS), and extracts the vCenter host and vSphere
// API credentials. This mirrors the parsing logic in
// hack/e2e/setup-testbed-env.sh's _load_testbed and _parse_vc_credentials,
// which is the most complete of the several ad hoc testbed.json parsers in
// this repo.
func LoadTestbedConfig(ctx context.Context, source string) (*TestbedConfig, error) {
	var (
		data []byte
		err  error
	)

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		data, err = fetchURL(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch testbed config from %q: %w", source, err)
		}
	} else {
		data, err = os.ReadFile(source) //nolint:gosec // G304: source is an operator-supplied CLI flag, not untrusted input.
		if err != nil {
			return nil, fmt.Errorf("failed to read testbed config file %q: %w", source, err)
		}
	}

	return ParseTestbedConfig(data)
}

func fetchURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return body, nil
}

// ParseTestbedConfig parses the raw (already-fetched) testbed.json body.
func ParseTestbedConfig(data []byte) (*TestbedConfig, error) {
	var doc testbedDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse testbed config: %w", err)
	}

	// UTS wraps the real testbed document in a `deliverable_blob` field.
	if len(doc.DeliverableBlob) > 0 && string(doc.DeliverableBlob) != "null" {
		var inner testbedDoc
		if err := json.Unmarshal(doc.DeliverableBlob, &inner); err != nil {
			return nil, fmt.Errorf("failed to parse deliverable_blob: %w", err)
		}
		doc = inner
	}

	if entry, ok := resolveVCEntry(doc.VC); ok {
		return &TestbedConfig{
			VCHost:      entry.host(),
			VimUsername: entry.username(),
			VimPassword: entry.password(),
		}, nil
	}

	if doc.VCenterIP != "" {
		return &TestbedConfig{
			VCHost:      doc.VCenterIP,
			VimUsername: firstNonEmpty(doc.VCenterUsername, "administrator@vsphere.local"),
			VimPassword: doc.VCenterPassword,
		}, nil
	}

	return &TestbedConfig{
		VCHost:      firstNonEmpty(doc.VCIP, doc.VCenterIP, doc.TestbedIP),
		VimUsername: firstNonEmpty(doc.VCUsername, doc.VCenterUsername, doc.Username, "administrator@vsphere.local"),
		VimPassword: firstNonEmpty(doc.VCPassword, doc.VCenterPassword, doc.Password),
	}, nil
}

// resolveVCEntry resolves the `.vc` field of a testbed.json document, which
// may be a non-empty JSON array (VDS testbeds) or a non-empty JSON object
// keyed by numeric run ID (VPC testbeds). It returns false if `.vc` is
// absent, null, or empty, in which case the caller falls back to other
// top-level fields.
func resolveVCEntry(raw json.RawMessage) (vcEntry, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return vcEntry{}, false
	}

	var arr []vcEntry
	if err := json.Unmarshal(raw, &arr); err == nil {
		if len(arr) == 0 {
			return vcEntry{}, false
		}
		return arr[0], true
	}

	var obj map[string]vcEntry
	if err := json.Unmarshal(raw, &obj); err == nil {
		if len(obj) == 0 {
			return vcEntry{}, false
		}

		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ni, erri := strconv.Atoi(keys[i])
			nj, errj := strconv.Atoi(keys[j])
			if erri == nil && errj == nil {
				return ni < nj
			}
			return keys[i] < keys[j]
		})

		return obj[keys[0]], true
	}

	return vcEntry{}, false
}

// firstNonEmpty returns the first non-empty string among vals.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
