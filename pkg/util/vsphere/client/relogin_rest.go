// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"io"
	"net/http"
)

const (
	// restSessionHeader mirrors vapi/internal.SessionCookieName, which is in
	// an internal package and cannot be imported from this repository.
	restSessionHeader = "vmware-api-session-id"

	// restSessionPath mirrors vapi/rest.Path + vapi/internal.SessionPath.
	// Login (POST), logout (DELETE) and the session probe
	// (POST ?~action=get) all share this path, so a single path check
	// excludes all three from the retry logic.
	restSessionPath = "/rest/com/vmware/cis/session"
)

// reloginREST is an http.RoundTripper wrapper that re-authenticates the REST
// session and replays the request in place when the first attempt is answered
// with a 401. It must be installed innermost, below the keepalive handler.
type reloginREST struct {
	rt     http.RoundTripper
	keeper *sessionKeeper
}

// newReloginREST returns the inline re-login round tripper for the given
// underlying round tripper.
func newReloginREST(
	rt http.RoundTripper,
	keeper *sessionKeeper) *reloginREST {

	return &reloginREST{
		rt:     rt,
		keeper: keeper,
	}
}

// RoundTrip implements http.RoundTripper. On a 401 for a request with a
// safely repeatable body, it re-authenticates the session and replays the
// request exactly once with a rewritten vmware-api-session-id header. Requests
// to the session path -- login, logout and the session probe -- are never
// retried, which is the re-entrancy guard.
func (r *reloginREST) RoundTrip(req *http.Request) (*http.Response, error) {
	// Exclude the session path first, before anything else. This preserves
	// rest.Client.Session's documented contract of returning (nil, nil) on
	// a 401, which the wrapper below must never turn into a retry.
	if isSessionPath(req.URL.Path) {
		return r.rt.RoundTrip(req)
	}

	// Decide repeatability before the first attempt. replayBody returns nil
	// when the body cannot be re-read, in which case a 401 is surfaced to
	// the caller instead of being retried.
	replay := replayBody(req)

	// Read the generation before the first attempt, not after the 401: a
	// login that lands while this call was in flight must still be
	// observed.
	gen := r.keeper.restGeneration()

	res, err := r.rt.RoundTrip(req)
	if err != nil || res.StatusCode != http.StatusUnauthorized || replay == nil {
		return res, err
	}

	// Drain and close the 401 response so the connection is reusable.
	drainResponse(res)

	trigger := req.Method + " " + req.URL.Path
	if lerr := r.keeper.reloginREST(req.Context(), gen, trigger); lerr != nil {
		// Re-login failed. Surface the original 401; there is nothing
		// better to return.
		return res, nil
	}

	// http.RoundTripper's contract forbids mutating the request it was
	// given, so clone before modifying. The header rewrite is not
	// optional: rest.Client.Do stamps the old session id before the
	// transport sees the request.
	req2 := req.Clone(req.Context())
	req2.Body = replay()
	if req.Body != nil && req2.Body == nil {
		// GetBody failed; do not replay with a missing body.
		return res, nil
	}
	req2.Header.Set(restSessionHeader, r.keeper.rest.SessionID())
	return r.rt.RoundTrip(req2)
}

// isSessionPath reports whether the request targets the vAPI session path.
func isSessionPath(path string) bool {
	return path == restSessionPath
}

// replayBody returns a func that produces a fresh body for a replayed request,
// or nil when the body cannot be safely repeated. A request with no body, or
// one built from a *bytes.Buffer -- which http.NewRequest equips with GetBody,
// and which rest.Resource.Request produces via encode -- replays cleanly. A
// streaming body, e.g. rest.Client.Upload of a content-library item, must not
// be buffered into memory and is not retried.
func replayBody(req *http.Request) func() io.ReadCloser {
	switch {
	case req.Body == nil || req.Body == http.NoBody:
		return func() io.ReadCloser { return req.Body }
	case req.GetBody != nil:
		return func() io.ReadCloser {
			b, err := req.GetBody()
			if err != nil {
				return nil
			}
			return b
		}
	}
	return nil
}

// drainResponse reads and closes the response body so the underlying
// connection can be reused.
func drainResponse(res *http.Response) {
	if res.Body != nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
}
