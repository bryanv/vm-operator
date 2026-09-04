// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/pbm"
	_ "github.com/vmware/govmomi/pbm/simulator" // load PBM simulator
	pbmtypes "github.com/vmware/govmomi/pbm/types"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/session/keepalive"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/simulator/sim25"
	"github.com/vmware/govmomi/vapi/rest"
	_ "github.com/vmware/govmomi/vapi/simulator" // load VAPI simulator
	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/soap"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"
)

// This file is in package client, not client_test, because the re-login
// wrappers and the session keeper are package-private and the tests below
// splice spies between the wrapper and the raw soap client.

const (
	reloginSimUsername = "valid"
	reloginSimPassword = "valid"
	reloginSimBadPass  = "invalid"

	reloginSimCategoryPath = "/rest/com/vmware/cis/tagging/category"
)

// spyRT is a counting soap.RoundTripper spliced in as the re-login wrapper's
// underlying round tripper so the recorded sequence includes the re-login. The
// slice is mutex-guarded because the keepalive goroutine calls RoundTrip too.
type spyRT struct {
	inner soap.RoundTripper
	mu    sync.Mutex
	seen  []string
}

func (s *spyRT) RoundTrip(
	ctx context.Context,
	req, res soap.HasFault) error {

	s.mu.Lock()
	s.seen = append(s.seen, fmt.Sprintf("%T", req))
	s.mu.Unlock()
	return s.inner.RoundTrip(ctx, req, res)
}

func (s *spyRT) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = nil
}

func (s *spyRT) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func (s *spyRT) count(body string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.seen {
		if b == body {
			n++
		}
	}
	return n
}

// spyHTTPRequest records the session header of one REST round trip.
type spyHTTPRequest struct {
	method  string
	path    string
	session string
}

// spyHTTPRT is the REST counterpart of spyRT.
type spyHTTPRT struct {
	inner http.RoundTripper
	mu    sync.Mutex
	seen  []spyHTTPRequest
}

func (s *spyHTTPRT) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.seen = append(s.seen, spyHTTPRequest{
		method:  req.Method,
		path:    req.URL.Path,
		session: req.Header.Get(restSessionHeader),
	})
	s.mu.Unlock()
	return s.inner.RoundTrip(req)
}

func (s *spyHTTPRT) recorded() []spyHTTPRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]spyHTTPRequest(nil), s.seen...)
}

func (s *spyHTTPRT) countPath(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.seen {
		if r.path == path {
			n++
		}
	}
	return n
}

// reloginAdminSession returns a session manager over a second client that can
// terminate the primary client's session.
func reloginAdminSession(
	ctx context.Context,
	u *url.URL,
	username, password string) *session.Manager {

	c, err := vim25.NewClient(ctx, soap.NewClient(u, true))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	m := session.NewManager(c)
	ExpectWithOffset(1, m.Login(ctx, url.UserPassword(username, password))).To(Succeed())
	return m
}

// terminateSession terminates the primary client's current session from the
// admin session, emulating a NotAuthenticated fault the way the existing
// keepalive tests do.
func terminateSession(
	ctx context.Context,
	sm *session.Manager,
	admin *session.Manager) {

	sess, err := sm.UserSession(ctx)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, sess).NotTo(BeNil())
	ExpectWithOffset(1, admin.TerminateSession(ctx, []string{sess.Key})).To(Succeed())
}

// startReloginSimServer starts a VPX simulator that only accepts the given
// credentials. Used by the specs that need real credential failures or fault
// injection; the simulator.Test helper accepts any non-empty credentials.
func startReloginSimServer(
	username, password string) (*simulator.Model, *simulator.Server) {

	model := simulator.VPX()
	Expect(model.Create()).To(Succeed())
	model.Service.TLS = &tls.Config{}
	model.Service.RegisterEndpoints = true

	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	l, err := net.ListenTCP("tcp", addr)
	Expect(err).ToNot(HaveOccurred())
	Expect(l.Close()).To(Succeed())
	model.Service.Listen = &url.URL{
		Host: l.Addr().String(),
		User: url.UserPassword(username, password),
	}

	server := model.Service.NewServer()
	DeferCleanup(server.Close)
	DeferCleanup(model.Remove)

	return model, server
}

var _ = Describe("Relogin", Label(testlabels.VCSim), func() {

	Describe("session keeper", func() {
		It("short-circuits a stale generation without logging in", func() {
			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				c.RoundTripper = spy
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)

				// vcsim rejects Login while a valid session cookie is
				// attached, so terminate the session first.
				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				spy.reset()

				gen := keeper.soapGeneration()
				Expect(keeper.reloginSOAP(ctx, gen, "test")).To(Succeed())
				Expect(spy.count("*methods.LoginBody")).To(Equal(1))

				// Another goroutine already refreshed: no second login.
				Expect(keeper.reloginSOAP(ctx, gen, "test")).To(Succeed())
				Expect(spy.count("*methods.LoginBody")).To(Equal(1))
			})
		})

		It("produces one login for N concurrent faults", func() {
			const goroutines = 20

			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				c.RoundTripper = spy
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)

				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				spy.reset()

				gen := keeper.soapGeneration()

				var (
					wg   sync.WaitGroup
					errs = make([]error, goroutines)
				)
				for i := 0; i < goroutines; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						errs[i] = keeper.reloginSOAP(ctx, gen, "test")
					}(i)
				}
				wg.Wait()

				for _, err := range errs {
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(spy.count("*methods.LoginBody")).To(Equal(1))
			})
		})

		It("propagates a login error without bumping the generation", func() {
			ctx := context.Background()
			_, server := startReloginSimServer(reloginSimUsername, reloginSimPassword)

			c, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
			Expect(err).NotTo(HaveOccurred())

			spy := &spyRT{inner: c.RoundTripper}
			c.RoundTripper = spy
			sm := session.NewManager(c)
			keeper := newSessionKeeper(
				sm,
				url.UserPassword(reloginSimUsername, reloginSimPassword))
			Expect(sm.Login(ctx, url.UserPassword(reloginSimUsername, reloginSimPassword))).To(Succeed())
			spy.reset()

			keeper.userInfo = url.UserPassword(reloginSimUsername, reloginSimBadPass)
			gen := keeper.soapGeneration()

			Expect(keeper.reloginSOAP(ctx, gen, "test")).NotTo(Succeed())
			Expect(spy.count("*methods.LoginBody")).To(Equal(1))
			Expect(keeper.soapGeneration()).To(Equal(gen))
		})
	})

	Describe("SOAP wrapper", func() {
		It("re-logs in and replays once on a terminated session", func() {
			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)
				c.RoundTripper = newReloginSOAP(spy, keeper)

				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				spy.reset()

				_, err := methods.GetCurrentTime(ctx, c)
				Expect(err).NotTo(HaveOccurred())
				Expect(spy.recorded()).To(Equal([]string{
					"*methods.CurrentTimeBody",
					"*methods.LoginBody",
					"*methods.CurrentTimeBody",
				}))
			})
		})

		It("returns the retried result, not the stale fault", func() {
			// This is the resetResponse regression: without zeroing the
			// response, the stale fault from the first attempt makes the
			// successful replay report NotAuthenticated.
			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)
				c.RoundTripper = newReloginSOAP(spy, keeper)

				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				spy.reset()

				res, err := methods.GetCurrentTime(ctx, c)
				Expect(err).NotTo(HaveOccurred())
				Expect(res).NotTo(BeNil())
			})
		})

		It("does not recurse on a faulting login body", func() {
			ctx := context.Background()
			_, server := startReloginSimServer(reloginSimUsername, reloginSimPassword)

			c, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
			Expect(err).NotTo(HaveOccurred())

			spy := &spyRT{inner: c.RoundTripper}
			sm := session.NewManager(c)
			keeper := newSessionKeeper(
				sm,
				url.UserPassword(reloginSimUsername, reloginSimPassword))
			c.RoundTripper = newReloginSOAP(spy, keeper)
			Expect(sm.Login(ctx, url.UserPassword(reloginSimUsername, reloginSimPassword))).To(Succeed())
			spy.reset()

			admin := reloginAdminSession(ctx, server.URL, reloginSimUsername, reloginSimPassword)
			terminateSession(ctx, sm, admin)
			spy.reset()

			keeper.userInfo = url.UserPassword(reloginSimUsername, reloginSimBadPass)

			_, err = methods.GetCurrentTime(ctx, c)
			Expect(err).To(HaveOccurred())

			// The original fault and the login failure are both visible.
			Expect(IsNotAuthenticatedError(err)).To(BeTrue())
			Expect(IsInvalidLogin(err)).To(BeTrue())

			// Exactly one login attempt, with no recursion and no replay.
			Expect(spy.recorded()).To(Equal([]string{
				"*methods.CurrentTimeBody",
				"*methods.LoginBody",
			}))
		})

		It("re-logs in but does not replay WaitForUpdatesEx", func() {
			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)
				c.RoundTripper = newReloginSOAP(spy, keeper)

				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				spy.reset()

				_, err := methods.WaitForUpdatesEx(ctx, c, &vimtypes.WaitForUpdatesEx{
					This: vimtypes.ManagedObjectReference{
						Type:  "PropertyCollector",
						Value: "session[relogin-test]property-collector",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(IsNotAuthenticatedError(err)).To(BeTrue())

				// The login happened, but the fault is still returned and
				// there is no second WaitForUpdatesEx.
				Expect(spy.recorded()).To(Equal([]string{
					"*methods.WaitForUpdatesExBody",
					"*methods.LoginBody",
				}))
			})
		})

		It("re-logs in but does not replay under WithNoReplay", func() {
			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)
				c.RoundTripper = newReloginSOAP(spy, keeper)

				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				spy.reset()

				_, err := methods.GetCurrentTime(WithNoReplay(ctx), c)
				Expect(err).To(HaveOccurred())
				Expect(IsNotAuthenticatedError(err)).To(BeTrue())

				Expect(spy.recorded()).To(Equal([]string{
					"*methods.CurrentTimeBody",
					"*methods.LoginBody",
				}))
			})
		})

		It("produces one login for N concurrent replayed calls", func() {
			const goroutines = 20

			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)
				c.RoundTripper = newReloginSOAP(spy, keeper)

				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				spy.reset()

				var (
					wg   sync.WaitGroup
					errs = make([]error, goroutines)
				)
				for i := 0; i < goroutines; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						_, errs[i] = methods.GetCurrentTime(ctx, c)
					}(i)
				}
				wg.Wait()

				for _, err := range errs {
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(spy.count("*methods.LoginBody")).To(Equal(1))
			})
		})

		It("passes a NoPermission fault through untouched", func() {
			ctx := context.Background()
			model, server := startReloginSimServer(reloginSimUsername, reloginSimPassword)

			model.Service.AddFaultRule(&simulator.FaultInjectionRule{
				MethodName:  "CurrentTime",
				ObjectType:  "*",
				ObjectName:  "*",
				Probability: 1.0,
				FaultType:   simulator.FaultTypeNoPermission,
				Enabled:     true,
				MaxCount:    1,
			})

			c, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
			Expect(err).NotTo(HaveOccurred())

			spy := &spyRT{inner: c.RoundTripper}
			keeper := newSessionKeeper(session.NewManager(c), simulator.DefaultLogin)
			c.RoundTripper = newReloginSOAP(spy, keeper)

			// The client is unauthenticated; log in so the injected fault is
			// what surfaces, not the no-session fault.
			Expect(session.NewManager(c).Login(
				ctx,
				url.UserPassword(reloginSimUsername, reloginSimPassword))).To(Succeed())
			spy.reset()

			_, err = methods.GetCurrentTime(ctx, c)
			Expect(err).To(HaveOccurred())
			Expect(IsNotAuthenticatedError(err)).To(BeFalse())
			Expect(spy.recorded()).To(Equal([]string{"*methods.CurrentTimeBody"}))
		})

		It("passes an InvalidArgument fault through untouched", func() {
			ctx := context.Background()
			model, server := startReloginSimServer(reloginSimUsername, reloginSimPassword)

			model.Service.AddFaultRule(&simulator.FaultInjectionRule{
				MethodName:  "CurrentTime",
				ObjectType:  "*",
				ObjectName:  "*",
				Probability: 1.0,
				FaultType:   simulator.FaultTypeInvalidArgument,
				Enabled:     true,
				MaxCount:    1,
			})

			c, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
			Expect(err).NotTo(HaveOccurred())

			spy := &spyRT{inner: c.RoundTripper}
			keeper := newSessionKeeper(session.NewManager(c), simulator.DefaultLogin)
			c.RoundTripper = newReloginSOAP(spy, keeper)

			Expect(session.NewManager(c).Login(
				ctx,
				url.UserPassword(reloginSimUsername, reloginSimPassword))).To(Succeed())
			spy.reset()

			_, err = methods.GetCurrentTime(ctx, c)
			Expect(err).To(HaveOccurred())
			Expect(IsNotAuthenticatedError(err)).To(BeFalse())
			Expect(spy.recorded()).To(Equal([]string{"*methods.CurrentTimeBody"}))
		})

		It("passes a transport error through untouched", func() {
			ctx := context.Background()
			_, server := startReloginSimServer(reloginSimUsername, reloginSimPassword)

			c, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
			Expect(err).NotTo(HaveOccurred())

			spy := &spyRT{inner: c.RoundTripper}
			keeper := newSessionKeeper(session.NewManager(c), simulator.DefaultLogin)
			c.RoundTripper = newReloginSOAP(spy, keeper)

			server.Close()

			_, err = methods.GetCurrentTime(ctx, c)
			Expect(err).To(HaveOccurred())
			Expect(IsNotAuthenticatedError(err)).To(BeFalse())
			Expect(spy.count("*methods.LoginBody")).To(Equal(0))
		})
	})

	Describe("PBM", func() {
		It("recovers a PBM call on a terminated session", func() {
			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				spy := &spyRT{inner: c.RoundTripper}
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)
				c.RoundTripper = newReloginSOAP(spy, keeper)

				pbmClient, err := pbm.NewClient(ctx, c)
				Expect(err).NotTo(HaveOccurred())

				// The PBM round tripper dispatches through the derived
				// soap client, not the vim client's chain, so give it its
				// own spy.
				pbmSpy := &spyRT{inner: pbmClient.Client}
				pbmClient.RoundTripper = newReloginSOAP(pbmSpy, keeper)

				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)

				_, err = pbmClient.QueryProfile(
					ctx,
					pbmtypes.PbmProfileResourceType{
						ResourceType: string(pbmtypes.PbmProfileResourceTypeEnumSTORAGE),
					},
					"")
				Expect(err).NotTo(HaveOccurred())

				// One fault, one login, one replay. The re-login goes
				// through the vim client's chain; the fault and the replay
				// go through the PBM chain.
				Expect(pbmSpy.count("*methods.PbmQueryProfileBody")).To(Equal(2))
				Expect(spy.count("*methods.LoginBody")).To(Equal(1))
			})
		})
	})

	Describe("REST", func() {
		// newRestChain installs the spy, the re-login wrapper and the
		// keepalive handler on the REST client. The wrapper is returned so
		// specs can drive it directly for the branches rest.Client cannot
		// produce.
		newRestChain := func(ctx context.Context, vc *vim25.Client) (
			*spyHTTPRT, *rest.Client, *sessionKeeper, *reloginREST) {

			restClient := rest.NewClient(vc)
			keeper := newSessionKeeper(session.NewManager(vc), simulator.DefaultLogin)
			keeper.setRestClient(restClient)

			spy := &spyHTTPRT{inner: restClient.Transport}
			wrapper := newReloginREST(spy, keeper)
			restClient.Transport = wrapper
			restClient.Transport = keepalive.NewHandlerREST(
				restClient,
				keepAliveIdleTime,
				keeper.restKeepAlive)

			Expect(restClient.Login(ctx, simulator.DefaultLogin)).To(Succeed())
			return spy, restClient, keeper, wrapper
		}

		It("re-logs in and replays a GET with a rewritten session header", func() {
			simulator.Test(func(ctx context.Context, vc *vim25.Client) {
				spy, restClient, _, _ := newRestChain(ctx, vc)
				manager := tags.NewManager(restClient)

				Expect(restClient.Logout(ctx)).To(Succeed())

				_, err := manager.ListCategories(ctx)
				Expect(err).NotTo(HaveOccurred())

				gets := []spyHTTPRequest{}
				for _, r := range spy.recorded() {
					if r.path == reloginSimCategoryPath {
						gets = append(gets, r)
					}
				}
				Expect(gets).To(HaveLen(2))

				// The replay carried the new session id, not the old one.
				sid := restClient.SessionID()
				Expect(sid).NotTo(BeEmpty())
				Expect(gets[0].session).ToNot(Equal(sid))
				Expect(gets[1].session).To(Equal(sid))
			})
		})

		It("re-logs in and replays a JSON POST via GetBody", func() {
			simulator.Test(func(ctx context.Context, vc *vim25.Client) {
				spy, restClient, _, _ := newRestChain(ctx, vc)
				manager := tags.NewManager(restClient)

				Expect(restClient.Logout(ctx)).To(Succeed())

				id, err := manager.CreateCategory(ctx, &tags.Category{
					Name: "relogin-test",
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(id).NotTo(BeEmpty())

				posts := []spyHTTPRequest{}
				for _, r := range spy.recorded() {
					if r.method == http.MethodPost && r.path == reloginSimCategoryPath {
						posts = append(posts, r)
					}
				}
				Expect(posts).To(HaveLen(2))
				sid := restClient.SessionID()
				Expect(posts[0].session).ToNot(Equal(sid))
				Expect(posts[1].session).To(Equal(sid))
			})
		})

		It("replays a non-session DELETE", func() {
			simulator.Test(func(ctx context.Context, vc *vim25.Client) {
				spy, restClient, _, _ := newRestChain(ctx, vc)
				manager := tags.NewManager(restClient)

				Expect(restClient.Logout(ctx)).To(Succeed())

				// A DELETE to a nonexistent category: the replay happens
				// (the point of this spec), and the second attempt surfaces
				// the error the replayed request earns.
				err := manager.DeleteCategory(ctx, &tags.Category{ID: "does-not-exist"})
				Expect(err).To(HaveOccurred())

				deletes := []spyHTTPRequest{}
				for _, r := range spy.recorded() {
					if r.method == http.MethodDelete &&
						strings.HasPrefix(r.path, reloginSimCategoryPath) {
						deletes = append(deletes, r)
					}
				}
				Expect(deletes).To(HaveLen(2))
				sid := restClient.SessionID()
				Expect(deletes[0].session).ToNot(Equal(sid))
				Expect(deletes[1].session).To(Equal(sid))
			})
		})

		It("does not replay a streaming body", func() {
			simulator.Test(func(ctx context.Context, vc *vim25.Client) {
				spy, restClient, _, wrapper := newRestChain(ctx, vc)

				Expect(restClient.Logout(ctx)).To(Succeed())

				// An upload-shaped request: an arbitrary stream with
				// GetBody == nil and ContentLength > 0, like soap.Client
				// Upload builds. It must be surfaced unretried and never
				// buffered.
				req, err := http.NewRequest(
					http.MethodPut,
					restClient.Resource("/com/vmware/cis/tagging/category/id:test").String(),
					io.NopCloser(strings.NewReader("body")))
				Expect(err).NotTo(HaveOccurred())
				req.ContentLength = 4

				res, err := wrapper.RoundTrip(req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.StatusCode).To(Equal(http.StatusUnauthorized))
				Expect(spy.countPath(reloginSimCategoryPath + "/id:test")).To(Equal(1))
			})
		})

		It("replays an action POST with an empty body", func() {
			simulator.Test(func(ctx context.Context, vc *vim25.Client) {
				spy, restClient, _, wrapper := newRestChain(ctx, vc)

				Expect(restClient.Logout(ctx)).To(Succeed())

				// An action-style POST carrying the empty io.MultiReader
				// body: ContentLength is 0, so replayBody treats it as
				// provably empty and the request is replayed.
				req := restClient.Resource("/com/vmware/cis/tagging/tag-association/id:test").
					WithAction("attach").
					Request(http.MethodPost)

				res, err := wrapper.RoundTrip(req)
				Expect(err).NotTo(HaveOccurred())

				// The replayed request reaches the handler, which answers
				// 404 for the nonexistent tag.
				Expect(res.StatusCode).To(Equal(http.StatusNotFound))
				Expect(spy.countPath("/rest/com/vmware/cis/tagging/tag-association/id:test")).To(Equal(2))
			})
		})

		It("never retries the session path", func() {
			simulator.Test(func(ctx context.Context, vc *vim25.Client) {
				spy, restClient, _, _ := newRestChain(ctx, vc)

				Expect(restClient.Logout(ctx)).To(Succeed())

				// The session probe swallows the 401 and returns
				// (nil, nil); the wrapper must not retry it.
				s, err := restClient.Session(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(s).To(BeNil())

				// One login, one logout, one probe -- no second probe and
				// no additional login.
				Expect(spy.countPath(restSessionPath)).To(Equal(3))
			})
		})

		It("restKeepAlive heals a dead session with no application traffic", func() {
			simulator.Test(func(ctx context.Context, vc *vim25.Client) {
				_, restClient, keeper, _ := newRestChain(ctx, vc)

				Expect(restClient.Logout(ctx)).To(Succeed())

				Expect(keeper.restKeepAlive()).To(Succeed())

				s, err := restClient.Session(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(s).NotTo(BeNil())
			})
		})
	})

	Describe("Keepalive", func() {
		It("survives a session termination and heals the session with no application traffic", func() {
			var (
				sessionIdleTimeout = time.Second
				keepAliveIdle      = 250 * time.Millisecond
			)

			simulator.Test(func(ctx context.Context, c *vim25.Client) {
				Expect(sim25.SetSessionTimeout(ctx, c, sessionIdleTimeout)).To(Succeed())

				spy := &spyRT{inner: c.RoundTripper}
				sm := session.NewManager(c)
				keeper := newSessionKeeper(sm, simulator.DefaultLogin)
				c.RoundTripper = keepalive.NewHandlerSOAP(newReloginSOAP(spy, keeper), keepAliveIdle, nil)

				// vcsim rejects Login while a valid session cookie is
				// attached: terminate the pre-existing session first, then
				// log in to start the handler.
				admin := reloginAdminSession(ctx, c.URL(), reloginSimUsername, reloginSimPassword)
				terminateSession(ctx, sm, admin)
				Expect(sm.Login(ctx, simulator.DefaultLogin)).To(Succeed())

				terminateSession(ctx, sm, admin)
				spy.reset()
				spy.reset()

				// No application traffic from here on: the keepalive ping
				// travels through the re-login wrapper and heals the
				// session on its own.
				Eventually(func() int {
					return spy.count("*methods.LoginBody")
				}, 10*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 1))

				// The ping sequence is fault, login, replayed ping; the
				// ticker survived and no further login was needed.
				Expect(spy.recorded()[:3]).To(Equal([]string{
					"*methods.CurrentTimeBody",
					"*methods.LoginBody",
					"*methods.CurrentTimeBody",
				}))
				Expect(spy.count("*methods.LoginBody")).To(Equal(1))

				// The session is valid again.
				sess, err := sm.UserSession(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess).NotTo(BeNil())
			})
		})
	})

	Describe("NewClient", func() {
		// newAssembledClient assembles the full client against a simulator
		// that only accepts the given credentials.
		newAssembledClient := func(ctx context.Context, inline bool) *Client {
			model, server := startReloginSimServer(reloginSimUsername, reloginSimPassword)
			datacenter := model.Map().Any("Datacenter").Reference().Value
			caFile, err := server.CertificateFile()
			Expect(err).NotTo(HaveOccurred())

			c, err := NewClient(ctx, Config{
				Host:                 server.URL.Hostname(),
				Port:                 server.URL.Port(),
				Username:             reloginSimUsername,
				Password:             reloginSimPassword,
				CAFilePath:           caFile,
				Insecure:             false,
				Datacenter:           datacenter,
				InlineReloginEnabled: inline,
			})
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			return c
		}

		It("with the flag on recovers a terminated session inline", func() {
			ctx := context.Background()
			c := newAssembledClient(ctx, true)

			vc := c.VimClient()
			sm := session.NewManager(vc)
			admin := reloginAdminSession(ctx, vc.URL(), reloginSimUsername, reloginSimPassword)
			terminateSession(ctx, sm, admin)

			_, err := methods.GetCurrentTime(ctx, vc)
			Expect(err).NotTo(HaveOccurred())

			// The keepalive handler is outermost and wraps the re-login
			// wrapper, which wraps the raw soap client.
			h := reflect.ValueOf(vc.RoundTripper).Elem().FieldByName("roundTripper")
			Expect(h.Elem().Type().String()).To(Equal("*client.reloginSOAP"))
		})

		It("with the flag on recovers a dead REST session inline", func() {
			ctx := context.Background()
			c := newAssembledClient(ctx, true)

			Expect(c.RestClient().Logout(ctx)).To(Succeed())

			_, err := tags.NewManager(c.RestClient()).ListCategories(ctx)
			Expect(err).NotTo(HaveOccurred())
		})

		It("with the flag off keeps the legacy chain and does not recover inline", func() {
			ctx := context.Background()
			c := newAssembledClient(ctx, false)

			// The keepalive handler is outermost and wraps the raw soap
			// client; no re-login wrapper is in the chain.
			h := reflect.ValueOf(c.VimClient().RoundTripper).Elem().FieldByName("roundTripper")
			Expect(h.Elem().Type().String()).To(Equal("*soap.Client"))

			vc := c.VimClient()
			sm := session.NewManager(vc)
			admin := reloginAdminSession(ctx, vc.URL(), reloginSimUsername, reloginSimPassword)
			terminateSession(ctx, sm, admin)

			_, err := methods.GetCurrentTime(ctx, vc)
			Expect(err).To(HaveOccurred())
			Expect(IsNotAuthenticatedError(err)).To(BeTrue())
		})
	})
})
