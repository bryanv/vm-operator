// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vapi/rest"

	pkglog "github.com/vmware-tanzu/vm-operator/pkg/log"
)

// noReplayContextKey is the context key used by WithNoReplay. It is a struct
// type so only this package can populate it.
type noReplayContextKey struct{}

// WithNoReplay returns a context that suppresses request replay after an
// inline re-login. Re-login still happens; the original fault is still
// returned. Use it for callers that own session-scoped server state --
// property collectors, property filters, container and list views -- which
// a re-login destroys.
func WithNoReplay(ctx context.Context) context.Context {
	return context.WithValue(ctx, noReplayContextKey{}, struct{}{})
}

// isNoReplay reports whether ctx was marked with WithNoReplay.
func isNoReplay(ctx context.Context) bool {
	_, ok := ctx.Value(noReplayContextKey{}).(struct{})
	return ok
}

// sessionKeeper owns the re-authentication state shared by the SOAP, PBM and
// REST inline re-login round trippers. It serializes logins so that N
// goroutines faulting on the same dead session produce one login and one new
// vCenter session, and hands each wrapper a generation counter that lets a
// late arrival detect that someone else already re-authenticated.
//
// SOAP/vim25 and REST have independent generations because they are separate
// sessions with separate lifetimes.
type sessionKeeper struct {
	sm       *session.Manager
	rest     *rest.Client // set after the REST client is built.
	userInfo *url.Userinfo

	muSOAP  sync.Mutex
	genSOAP atomic.Uint64
	muREST  sync.Mutex
	genREST atomic.Uint64
}

// newSessionKeeper returns a session keeper for the given session manager and
// login credentials. The REST client is attached later, with setRestClient.
func newSessionKeeper(
	sm *session.Manager,
	userInfo *url.Userinfo) *sessionKeeper {

	return &sessionKeeper{
		sm:       sm,
		userInfo: userInfo,
	}
}

// setRestClient attaches the REST client used by the REST re-login paths.
func (k *sessionKeeper) setRestClient(c *rest.Client) {
	k.muREST.Lock()
	defer k.muREST.Unlock()
	k.rest = c
}

// soapGeneration returns the current SOAP session generation. Callers read it
// before their first attempt so that a login landing while the call was in
// flight is still observed.
func (k *sessionKeeper) soapGeneration() uint64 {
	return k.genSOAP.Load()
}

// restGeneration returns the current REST session generation. Callers read it
// before their first attempt so that a login landing while the call was in
// flight is still observed.
func (k *sessionKeeper) restGeneration() uint64 {
	return k.genREST.Load()
}

// reloginSOAP re-authenticates the vim25 session unless another goroutine
// already did so since gen was read. It returns nil without logging in when
// the generation has moved, and joins nothing: the caller is responsible for
// combining this error with the original fault.
func (k *sessionKeeper) reloginSOAP(
	ctx context.Context,
	gen uint64,
	trigger string) error {

	k.muSOAP.Lock()
	defer k.muSOAP.Unlock()

	// Another goroutine already refreshed while we were faulting.
	if k.genSOAP.Load() != gen {
		return nil
	}

	log := pkglog.FromContextOrDefault(ctx).WithName("vcSessionRelogin")
	log.Info("Re-authenticating vim client",
		"generation", gen, "method", trigger)

	if err := k.sm.Login(ctx, k.userInfo); err != nil {
		return err
	}
	k.genSOAP.Add(1)
	return nil
}

// reloginREST re-authenticates the REST session unless another goroutine
// already did so since gen was read. It is the REST counterpart of
// reloginSOAP.
func (k *sessionKeeper) reloginREST(
	ctx context.Context,
	gen uint64,
	trigger string) error {

	k.muREST.Lock()
	defer k.muREST.Unlock()

	// Another goroutine already refreshed while we were faulting.
	if k.genREST.Load() != gen {
		return nil
	}

	log := pkglog.FromContextOrDefault(ctx).WithName("vcSessionRelogin")
	log.Info("Re-authenticating REST client",
		"generation", gen, "path", trigger)

	if err := k.rest.Login(ctx, k.userInfo); err != nil {
		return err
	}
	k.genREST.Add(1)
	return nil
}

// restKeepAlive is the send func for the REST keepalive handler in inline
// mode. It probes the session and re-authenticates when it is gone.
//
// A transport failure returns nil on purpose: govmomi's keepalive handler
// stops its goroutine permanently the first time send returns an error, and a
// transport hiccup is not worth killing the ticker over. A re-login failure is
// returned, matching the behavior of the legacy RestKeepAliveHandlerFn.
func (k *sessionKeeper) restKeepAlive() error {
	ctx := context.Background()
	gen := k.restGeneration()

	s, err := k.rest.Session(ctx)
	if err != nil {
		// Transport hiccup; do not kill the ticker.
		return nil
	}
	if s != nil {
		return nil
	}
	return k.reloginREST(ctx, gen, "keepalive")
}
