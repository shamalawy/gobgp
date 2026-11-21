package bgp

import (
	"context"
	"slices"

	"github.com/osrg/gobgp/v4/api"

	"github.com/shamalawy/gobgp/internal/config"
)

// RibCounts are the route counts of one address family.
type RibCounts struct {
	AdjIn    uint64 `json:"adjIn"`    // paths received from all neighbors
	LocRib   uint64 `json:"locRib"`   // paths in the Loc-RIB
	Prefixes uint64 `json:"prefixes"` // destinations in the Loc-RIB
	AdjOut   uint64 `json:"adjOut"`   // paths advertised to all neighbors
}

type Status struct {
	ASN         uint32               `json:"asn"`
	RouterID    string               `json:"routerId"`
	ListenPort  int32                `json:"listenPort"`
	Started     int64                `json:"started"` // unix seconds
	Ribs        map[string]RibCounts `json:"ribs"`    // keyed by "ipv4" and "ipv6"
	Neighbors   []Neighbor           `json:"neighbors"`
	Established int                  `json:"established"`
}

type FamilyState struct {
	Received   uint64 `json:"received"`
	Advertised uint64 `json:"advertised"`
}

// Neighbor is a neighbor's configuration together with its session state.
type Neighbor struct {
	config.Neighbor
	State        string                 `json:"state"`
	Since        int64                  `json:"since"` // unix seconds of the last transition to or from Established
	RouterID     string                 `json:"peerRouterId,omitempty"`
	Counts       map[string]FamilyState `json:"counts"`
	Capabilities []string               `json:"capabilities"` // advertised by both sides
	MessagesIn   uint64                 `json:"messagesIn"`
	MessagesOut  uint64                 `json:"messagesOut"`
	Flaps        uint32                 `json:"flaps"`
	LastError    string                 `json:"lastError,omitempty"`
	BfdState     string                 `json:"bfdState,omitempty"`
}

func familyKey(f *api.Family) string {
	if f.GetAfi() == api.Family_AFI_IP6 {
		return "ipv6"
	}
	return "ipv4"
}

// Status reports the speaker identity, the per-family route counts and every
// configured neighbor with its live state.
func (e *Engine) Status(ctx context.Context) (*Status, error) {
	srv, cfg := e.server(), e.Config()
	st := &Status{
		ASN: cfg.Global.ASN, RouterID: cfg.Global.RouterID, ListenPort: cfg.Global.ListenPort,
		Started: e.started.Unix(), Ribs: map[string]RibCounts{}, Neighbors: make([]Neighbor, 0, len(cfg.Neighbors)),
	}
	live := map[string]*api.Peer{}
	if err := srv.ListPeer(ctx, &api.ListPeerRequest{EnableAdvertised: true}, func(p *api.Peer) {
		live[p.GetConf().GetNeighborAddress()] = p
	}); err != nil {
		return nil, err
	}
	for _, f := range []*api.Family{familyV4, familyV6} {
		t, err := srv.GetTable(ctx, &api.GetTableRequest{TableType: api.TableType_TABLE_TYPE_GLOBAL, Family: f})
		if err != nil {
			return nil, err
		}
		st.Ribs[familyKey(f)] = RibCounts{LocRib: t.NumPath, Prefixes: t.NumDestination}
	}
	for _, n := range cfg.Neighbors {
		out := Neighbor{Neighbor: n, State: stateNames[0], Counts: map[string]FamilyState{}, Capabilities: []string{}}
		if p := live[n.Address]; p != nil {
			s := p.GetState()
			if i := int(s.GetSessionState()) - 1; i > 0 {
				out.State = stateNames[i]
			}
			out.Since = p.GetTimers().GetState().GetDowntime().GetSeconds()
			if out.State == "Established" {
				st.Established++
				out.Since = p.GetTimers().GetState().GetUptime().GetSeconds()
			}
			out.RouterID = s.GetRouterId()
			out.MessagesIn = s.GetMessages().GetReceived().GetTotal()
			out.MessagesOut = s.GetMessages().GetSent().GetTotal()
			out.Flaps = s.GetFlops()
			out.LastError = s.GetDisconnectMessage()
			if n.Bfd != nil {
				out.BfdState = s.GetBfdState().GetSessionState().String()
			}
			remote := capNames(s.GetRemoteCap())
			for _, c := range capNames(s.GetLocalCap()) {
				if slices.Contains(remote, c) {
					out.Capabilities = append(out.Capabilities, c)
				}
			}
			for _, af := range p.GetAfiSafis() {
				a, key := af.GetState(), familyKey(af.GetConfig().GetFamily())
				out.Counts[key] = FamilyState{Received: a.GetReceived(), Advertised: a.GetAdvertised()}
				c := st.Ribs[key]
				c.AdjIn += a.GetReceived()
				c.AdjOut += a.GetAdvertised()
				st.Ribs[key] = c
			}
		}
		st.Neighbors = append(st.Neighbors, out)
	}
	return st, nil
}

func capNames(caps []*api.Capability) []string {
	var names []string
	for _, c := range caps {
		switch v := c.GetCap().(type) {
		case *api.Capability_MultiProtocol:
			names = append(names, "mp-"+familyKey(v.MultiProtocol.GetFamily()))
		case *api.Capability_RouteRefresh:
			names = append(names, "route-refresh")
		case *api.Capability_EnhancedRouteRefresh:
			names = append(names, "enhanced-route-refresh")
		case *api.Capability_ExtendedNexthop:
			names = append(names, "extended-nexthop")
		case *api.Capability_ExtendedMessage:
			names = append(names, "extended-message")
		case *api.Capability_FourOctetAsn:
			names = append(names, "4-octet-as")
		case *api.Capability_AddPath:
			names = append(names, "add-path")
		case *api.Capability_GracefulRestart:
			names = append(names, "graceful-restart")
		case *api.Capability_LongLivedGracefulRestart:
			names = append(names, "long-lived-graceful-restart")
		}
	}
	return names
}

// RpkiServer is an RTR cache session (RFC 8210).
type RpkiServer struct {
	config.RpkiServer
	Up     bool   `json:"up"`
	Since  int64  `json:"since"`
	Serial uint32 `json:"serial"`
	ROAsV4 uint32 `json:"roasV4"`
	ROAsV6 uint32 `json:"roasV6"`
}

func (e *Engine) RpkiServers(ctx context.Context) ([]RpkiServer, error) {
	out := []RpkiServer{}
	err := e.server().ListRpki(ctx, &api.ListRpkiRequest{}, func(r *api.Rpki) {
		s := r.GetState()
		srv := RpkiServer{
			RpkiServer: config.RpkiServer{Address: r.GetConf().GetAddress(), Port: r.GetConf().GetRemotePort()},
			Up:         s.GetUp(), Since: s.GetDowntime().GetSeconds(), Serial: s.GetSerial(),
			ROAsV4: s.GetRecordIpv4(), ROAsV6: s.GetRecordIpv6(),
		}
		if srv.Up {
			srv.Since = s.GetUptime().GetSeconds()
		}
		out = append(out, srv)
	})
	return out, err
}
