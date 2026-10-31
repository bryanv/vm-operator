// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package context

import "context"

type liveReadContextKey uint8

const liveReadContextValue liveReadContextKey = 0

// WithLiveRead returns a context that instructs the manager's client to read
// ConfigMaps and Secrets directly from the API server instead of the cache.
// Use this when a read must observe a write that was just made.
func WithLiveRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, liveReadContextValue, true)
}

// IsLiveRead returns true if the context was created with WithLiveRead.
func IsLiveRead(ctx context.Context) bool {
	v, _ := ctx.Value(liveReadContextValue).(bool)
	return v
}
