// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package main test file. Note: this uses the stdlib testing package with
// plain table-driven tests, rather than Ginkgo/Gomega with an external
// _test package, because package main has no meaningful external-test
// variant (it isn't importable) and this file exercises pure JSON-shape
// parsing logic with no controller/env-test machinery involved.
package main

import "testing"

func TestParseTestbedConfig(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    TestbedConfig
		wantErr bool
	}{
		{
			name: "vc array shape (VDS)",
			json: `{"vc":[{"ip4":"10.0.0.1","vimUsername":"vim-user","vimPassword":"vim-pass"}]}`,
			want: TestbedConfig{VCHost: "10.0.0.1", VimUsername: "vim-user", VimPassword: "vim-pass"},
		},
		{
			name: "vc array shape falls back to username/password and default vim user",
			json: `{"vc":[{"ip":"10.0.0.2","password":"root-pass"}]}`,
			want: TestbedConfig{VCHost: "10.0.0.2", VimUsername: "administrator@vsphere.local", VimPassword: "root-pass"},
		},
		{
			name: "vc object shape (VPC) picks lowest numeric key",
			json: `{"vc":{"2":{"ip4":"10.0.0.9","vimUsername":"u2","vimPassword":"p2"},"1":{"ip4":"10.0.0.1","vimUsername":"u1","vimPassword":"p1"}}}`,
			want: TestbedConfig{VCHost: "10.0.0.1", VimUsername: "u1", VimPassword: "p1"},
		},
		{
			name: "top-level vcenter_ip shape",
			json: `{"vcenter_ip":"10.0.0.5","vcenter_username":"admin@vsphere.local","vcenter_password":"pw"}`,
			want: TestbedConfig{VCHost: "10.0.0.5", VimUsername: "admin@vsphere.local", VimPassword: "pw"},
		},
		{
			name: "generic fallback shape",
			json: `{"vc_ip":"10.0.0.6","vc_username":"admin@vsphere.local","vc_password":"pw2"}`,
			want: TestbedConfig{VCHost: "10.0.0.6", VimUsername: "admin@vsphere.local", VimPassword: "pw2"},
		},
		{
			name: "deliverable_blob envelope is unwrapped",
			json: `{"deliverable_blob":{"vc":[{"ip4":"10.0.0.1","vimUsername":"u","vimPassword":"p"}]}}`,
			want: TestbedConfig{VCHost: "10.0.0.1", VimUsername: "u", VimPassword: "p"},
		},
		{
			name:    "invalid json",
			json:    `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTestbedConfig([]byte(tt.json))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *got != tt.want {
				t.Fatalf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "whitespace only", in: "   ", want: nil},
		{name: "single", in: "a", want: []string{"a"}},
		{name: "multiple with spaces", in: " a, b ,c", want: []string{"a", "b", "c"}},
		{name: "drops empty elements", in: "a,,b", want: []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAndTrim(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
