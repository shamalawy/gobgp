// Package bgp wraps the GoBGP protocol engine in a service layer that owns
// the persistent configuration, exposes the RIB for browsing and injects
// routes in bulk.
package bgp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/metrics"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/server"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/shamalawy/gobgp/internal/config"
)

// Secret replaces stored secrets in API output. Sending it back on an update
// keeps the stored value.
const Secret = "********"

var stateNames = []string{"Idle", "Connect", "Active", "OpenSent", "OpenConfirm", "Established"}

// Event is a neighbor FSM state transition.
type Event struct {
	Time   time.Time `json:"time"`
	Peer   string    `json:"peer"`
	ASN    uint32    `json:"asn"`
	State  string    `json:"state"`
	Reason string    `json:"reason,omitempty"`
}

type Engine struct {
	store    *config.Store
	log      *slog.Logger
	grpcAddr string
	onEvent  func(Event)
	started  time.Time

	mu        sync.RWMutex // guards the fields below across an engine restart
	srv       *server.BgpServer
	collector prometheus.Collector
	stopWatch context.CancelFunc

	rib ribCache
	inj injector
}

// New creates an engine. grpcAddr, when not empty, serves GoBGP's native gRPC
// API (the one the gobgp CLI speaks).
func New(store *config.Store, log *slog.Logger, grpcAddr string) *Engine {
	return &Engine{store: store, log: log, grpcAddr: grpcAddr, onEvent: func(Event) {}, started: time.Now()}
}

// OnEvent registers the receiver of FSM transitions. Call it before Start.
func (e *Engine) OnEvent(fn func(Event)) { e.onEvent = fn }

// Start brings up the BGP speaker with the stored configuration.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.start(ctx, e.store.Get())
}

func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stop()
}

func (e *Engine) start(ctx context.Context, cfg *config.Config) error {
	opts := []server.ServerOption{server.LoggerOption(e.log, nil)}
	if e.grpcAddr != "" {
		opts = append(opts, server.GrpcListenAddress(e.grpcAddr))
	}
	srv := server.NewBgpServer(opts...)
	go srv.Serve()
	if err := configure(ctx, srv, cfg); err != nil {
		srv.Stop()
		return err
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	err := srv.WatchEvent(watchCtx, server.WatchEventMessageCallbacks{
		OnPeerUpdate: func(ev *apiutil.WatchEventMessage_PeerEvent, ts time.Time) {
			if ev.Type != apiutil.PEER_EVENT_STATE {
				return
			}
			st := ev.Peer.State
			e.onEvent(Event{
				Time: ts, Peer: st.NeighborAddress.String(), ASN: st.PeerASN,
				State: stateNames[st.SessionState], Reason: st.DisconnectMessage,
			})
		},
	}, server.WatchPeer())
	if err != nil {
		cancel()
		srv.Stop()
		return err
	}
	e.srv, e.collector, e.stopWatch = srv, metrics.NewBgpCollector(srv), cancel
	return nil
}

func (e *Engine) stop() {
	if e.srv == nil {
		return
	}
	e.stopWatch()
	e.srv.Stop()
	e.srv = nil
	e.rib.reset()
}

func (e *Engine) server() *server.BgpServer {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.srv
}

// configure applies a whole configuration to a fresh server.
func configure(ctx context.Context, srv *server.BgpServer, cfg *config.Config) error {
	if err := srv.StartBgp(ctx, &api.StartBgpRequest{Global: apiGlobal(cfg.Global)}); err != nil {
		return err
	}
	if err := setPolicy(ctx, srv, cfg); err != nil {
		return err
	}
	for _, s := range cfg.Rpki {
		if err := srv.AddRpki(ctx, &api.AddRpkiRequest{Address: s.Address, Port: s.Port}); err != nil {
			return fmt.Errorf("rpki %s: %w", s.Address, err)
		}
	}
	for _, s := range cfg.Bmp {
		if err := addBmp(ctx, srv, s); err != nil {
			return err
		}
	}
	for _, n := range cfg.Neighbors {
		if err := addPeer(ctx, srv, n); err != nil {
			return err
		}
	}
	return addRoutes(srv, cfg.Routes)
}

func setPolicy(ctx context.Context, srv *server.BgpServer, cfg *config.Config) error {
	if err := srv.SetPolicies(ctx, apiPolicies(cfg.Policy)); err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	for dir, ref := range map[api.PolicyDirection]config.PolicyRef{
		api.PolicyDirection_POLICY_DIRECTION_IMPORT: cfg.Global.Import,
		api.PolicyDirection_POLICY_DIRECTION_EXPORT: cfg.Global.Export,
	} {
		if ref.Default == "" {
			ref.Default = "accept"
		}
		if err := srv.SetPolicyAssignment(ctx, &api.SetPolicyAssignmentRequest{Assignment: apiAssignment("", dir, ref)}); err != nil {
			return fmt.Errorf("global policy: %w", err)
		}
	}
	return nil
}

func addBmp(ctx context.Context, srv *server.BgpServer, s config.BmpServer) error {
	err := srv.AddBmp(ctx, &api.AddBmpRequest{Address: s.Address, Port: s.Port, Policy: bmpPolicies[s.Policy], StatisticsTimeout: 60})
	if err != nil {
		return fmt.Errorf("bmp %s: %w", s.Address, err)
	}
	return nil
}

func addPeer(ctx context.Context, srv *server.BgpServer, n config.Neighbor) error {
	if n.TcpAo != nil {
		if err := srv.AddTcpAoKeychain(ctx, &api.AddTcpAoKeychainRequest{Keychain: apiKeychain(n)}); err != nil {
			return fmt.Errorf("neighbor %s: %w", n.Address, err)
		}
	}
	if err := srv.AddPeer(ctx, &api.AddPeerRequest{Peer: apiPeer(n)}); err != nil {
		return fmt.Errorf("neighbor %s: %w", n.Address, err)
	}
	return nil
}

func deletePeer(ctx context.Context, srv *server.BgpServer, n config.Neighbor) error {
	if err := srv.DeletePeer(ctx, &api.DeletePeerRequest{Address: n.Address}); err != nil {
		return err
	}
	if n.TcpAo != nil {
		return srv.DeleteTcpAoKeychain(ctx, &api.DeleteTcpAoKeychainRequest{Name: keychainName(n.Address)})
	}
	return nil
}

func addRoutes(srv *server.BgpServer, routes []config.Route) error {
	if len(routes) == 0 {
		return nil
	}
	paths := make([]*apiutil.Path, 0, len(routes))
	for _, r := range routes {
		pfx, nh, attrs, err := routeAttrs(r)
		if err != nil {
			return err
		}
		paths = append(paths, newPath(pfx, nexthopAttr(nh), attrs))
	}
	_, err := srv.AddPath(apiutil.AddPathRequest{Paths: paths})
	return err
}

// Config returns the configuration with secrets masked.
func (e *Engine) Config() *config.Config {
	cfg := e.store.Get()
	for i := range cfg.Neighbors {
		maskSecrets(&cfg.Neighbors[i])
	}
	return cfg
}

func maskSecrets(n *config.Neighbor) {
	if n.Password != "" {
		n.Password = Secret
	}
	if n.TcpAo != nil {
		n.TcpAo.Key = Secret
	}
}

// keepSecrets restores the secrets a client echoed back masked.
func keepSecrets(n *config.Neighbor, old *config.Neighbor) {
	if old == nil {
		return
	}
	if n.Password == Secret {
		n.Password = old.Password
	}
	if n.TcpAo != nil && n.TcpAo.Key == Secret && old.TcpAo != nil {
		n.TcpAo.Key = old.TcpAo.Key
	}
}

// SetGlobal changes the speaker settings. A change of the global policy
// assignment is applied in place. ASN, router ID and listeners are fixed for
// the life of a GoBGP server, so changing those restarts the engine: every
// session drops and bulk-injected routes are lost.
func (e *Engine) SetGlobal(ctx context.Context, g config.Global) error {
	return e.store.Update(
		func(c *config.Config) error { c.Global = g; return nil },
		func(old, next *config.Config) error {
			identity := next.Global
			identity.Import, identity.Export = old.Global.Import, old.Global.Export
			if reflect.DeepEqual(identity, old.Global) {
				return setPolicy(ctx, e.server(), next)
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			e.stop()
			if err := e.start(ctx, next); err != nil {
				return errors.Join(err, e.start(ctx, old))
			}
			return nil
		})
}

func (e *Engine) AddNeighbor(ctx context.Context, n config.Neighbor) error {
	return e.store.Update(
		func(c *config.Config) error { c.Neighbors = append(c.Neighbors, n); return nil },
		func(_, next *config.Config) error {
			// Validate normalized the address, so look the neighbor up again.
			return addPeer(ctx, e.server(), next.Neighbors[len(next.Neighbors)-1])
		})
}

func (e *Engine) UpdateNeighbor(ctx context.Context, addr string, n config.Neighbor) error {
	return e.store.Update(
		func(c *config.Config) error {
			old := c.Neighbor(addr)
			if old == nil {
				return fmt.Errorf("neighbor %s not found", addr)
			}
			n.Address = addr
			keepSecrets(&n, old)
			*old = n
			return nil
		},
		func(old, next *config.Config) error {
			srv, was, now := e.server(), old.Neighbor(addr), *next.Neighbor(addr)
			if reflect.DeepEqual(was.TcpAo, now.TcpAo) {
				_, err := srv.UpdatePeer(ctx, &api.UpdatePeerRequest{Peer: apiPeer(now), DoSoftResetIn: true})
				return err
			}
			// A TCP-AO key cannot be swapped under a live session, so the
			// neighbor is recreated with its new keychain.
			if err := deletePeer(ctx, srv, *was); err != nil {
				return err
			}
			return addPeer(ctx, srv, now)
		})
}

func (e *Engine) DeleteNeighbor(ctx context.Context, addr string) error {
	return e.store.Update(
		func(c *config.Config) error {
			if c.Neighbor(addr) == nil {
				return fmt.Errorf("neighbor %s not found", addr)
			}
			c.Neighbors = slices.DeleteFunc(c.Neighbors, func(n config.Neighbor) bool { return n.Address == addr })
			return nil
		},
		func(old, _ *config.Config) error { return deletePeer(ctx, e.server(), *old.Neighbor(addr)) })
}

// NeighborAction runs an operational command on a neighbor: "enable",
// "disable" (administrative shutdown, RFC 4486/8203, persisted), "soft-reset"
// (route refresh, direction "in", "out" or both) or "hard-reset".
func (e *Engine) NeighborAction(ctx context.Context, addr, action, direction, message string) error {
	srv := e.server()
	setAdmin := func(down bool, apply func() error) error {
		return e.store.Update(
			func(c *config.Config) error {
				n := c.Neighbor(addr)
				if n == nil {
					return fmt.Errorf("neighbor %s not found", addr)
				}
				n.AdminDown = down
				return nil
			},
			func(_, _ *config.Config) error { return apply() })
	}
	switch action {
	case "enable":
		return setAdmin(false, func() error { return srv.EnablePeer(ctx, &api.EnablePeerRequest{Address: addr}) })
	case "disable":
		return setAdmin(true, func() error {
			return srv.DisablePeer(ctx, &api.DisablePeerRequest{Address: addr, Communication: message})
		})
	case "soft-reset":
		dir := api.ResetPeerRequest_DIRECTION_BOTH
		switch direction {
		case "in":
			dir = api.ResetPeerRequest_DIRECTION_IN
		case "out":
			dir = api.ResetPeerRequest_DIRECTION_OUT
		}
		return srv.ResetPeer(ctx, &api.ResetPeerRequest{Address: addr, Soft: true, Direction: dir})
	case "hard-reset":
		return srv.ResetPeer(ctx, &api.ResetPeerRequest{Address: addr, Communication: message})
	}
	return fmt.Errorf("unknown action %q", action)
}

// SetPolicy replaces the policy definitions. Routes already in the RIB keep
// the result of the old policy until the neighbor is soft reset.
func (e *Engine) SetPolicy(ctx context.Context, p config.Policy) error {
	return e.store.Update(
		func(c *config.Config) error { c.Policy = p; return nil },
		func(_, next *config.Config) error { return setPolicy(ctx, e.server(), next) })
}

// AddRoute originates a route and persists it, replacing any static route for
// the same prefix.
func (e *Engine) AddRoute(r config.Route) error {
	pfx, _, _, err := routeAttrs(r)
	if err != nil {
		return err
	}
	return e.store.Update(
		func(c *config.Config) error {
			c.Routes = slices.DeleteFunc(c.Routes, func(o config.Route) bool { return o.Prefix == pfx.String() })
			c.Routes = append(c.Routes, r)
			return nil
		},
		func(_, next *config.Config) error { return addRoutes(e.server(), next.Routes[len(next.Routes)-1:]) })
}

// DeleteRoute withdraws a locally originated prefix, static or bulk injected.
func (e *Engine) DeleteRoute(prefix string) error {
	pfx, err := netip.ParsePrefix(prefix)
	if err != nil {
		return fmt.Errorf("invalid prefix %q", prefix)
	}
	pfx = pfx.Masked()
	return e.store.Update(
		func(c *config.Config) error {
			c.Routes = slices.DeleteFunc(c.Routes, func(o config.Route) bool { return o.Prefix == pfx.String() })
			return nil
		},
		func(_, _ *config.Config) error {
			nlri, _ := bgp.NewIPAddrPrefix(pfx)
			return e.server().DeletePath(apiutil.DeletePathRequest{
				Paths: []*apiutil.Path{{Family: familyOf(pfx), Nlri: nlri, Withdrawal: true}},
			})
		})
}

// ClearInjected withdraws every locally originated route and re-originates
// the static ones, which removes exactly the bulk-injected routes.
func (e *Engine) ClearInjected() error {
	srv := e.server()
	if err := srv.DeletePath(apiutil.DeletePathRequest{DeleteAll: true}); err != nil {
		return err
	}
	return addRoutes(srv, e.store.Get().Routes)
}

func (e *Engine) AddRpki(ctx context.Context, s config.RpkiServer) error {
	return e.store.Update(
		func(c *config.Config) error { c.Rpki = append(c.Rpki, s); return nil },
		func(_, _ *config.Config) error {
			return e.server().AddRpki(ctx, &api.AddRpkiRequest{Address: s.Address, Port: s.Port})
		})
}

func (e *Engine) DeleteRpki(ctx context.Context, address string, port uint32) error {
	return e.store.Update(
		func(c *config.Config) error {
			c.Rpki = slices.DeleteFunc(c.Rpki, func(s config.RpkiServer) bool { return s.Address == address && s.Port == port })
			return nil
		},
		func(_, _ *config.Config) error {
			return e.server().DeleteRpki(ctx, &api.DeleteRpkiRequest{Address: address, Port: port})
		})
}

func (e *Engine) AddBmp(ctx context.Context, s config.BmpServer) error {
	return e.store.Update(
		func(c *config.Config) error { c.Bmp = append(c.Bmp, s); return nil },
		func(_, _ *config.Config) error { return addBmp(ctx, e.server(), s) })
}

func (e *Engine) DeleteBmp(ctx context.Context, address string, port uint32) error {
	return e.store.Update(
		func(c *config.Config) error {
			c.Bmp = slices.DeleteFunc(c.Bmp, func(s config.BmpServer) bool { return s.Address == address && s.Port == port })
			return nil
		},
		func(_, _ *config.Config) error {
			return e.server().DeleteBmp(ctx, &api.DeleteBmpRequest{Address: address, Port: port})
		})
}

// Describe and Collect make the engine a Prometheus collector that follows
// the GoBGP server across restarts.
func (e *Engine) Describe(ch chan<- *prometheus.Desc) { e.currentCollector().Describe(ch) }
func (e *Engine) Collect(ch chan<- prometheus.Metric) { e.currentCollector().Collect(ch) }

func (e *Engine) currentCollector() prometheus.Collector {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.collector
}
