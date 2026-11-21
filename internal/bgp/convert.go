package bgp

import (
	"fmt"
	"net/netip"

	"github.com/osrg/gobgp/v4/api"

	"github.com/shamalawy/gobgp/internal/config"
)

var (
	familyV4 = &api.Family{Afi: api.Family_AFI_IP, Safi: api.Family_SAFI_UNICAST}
	familyV6 = &api.Family{Afi: api.Family_AFI_IP6, Safi: api.Family_SAFI_UNICAST}

	tcpAoAlgorithms = map[string]api.TcpAoAlgorithm{
		"hmac-sha1-96":    api.TcpAoAlgorithm_TCP_AO_ALGORITHM_HMAC_SHA1_96,
		"aes-128-cmac-96": api.TcpAoAlgorithm_TCP_AO_ALGORITHM_AES_128_CMAC_96,
		"hmac-sha256-96":  api.TcpAoAlgorithm_TCP_AO_ALGORITHM_HMAC_SHA256_96,
		"hmac-sha256-128": api.TcpAoAlgorithm_TCP_AO_ALGORITHM_HMAC_SHA256_128,
	}
	matchTypes = map[string]api.MatchSet_Type{
		"":       api.MatchSet_TYPE_ANY,
		"any":    api.MatchSet_TYPE_ANY,
		"all":    api.MatchSet_TYPE_ALL,
		"invert": api.MatchSet_TYPE_INVERT,
	}
	routeActions = map[string]api.RouteAction{
		"accept": api.RouteAction_ROUTE_ACTION_ACCEPT,
		"reject": api.RouteAction_ROUTE_ACTION_REJECT,
	}
	communityOps = map[string]api.CommunityAction_Type{
		"add":     api.CommunityAction_TYPE_ADD,
		"remove":  api.CommunityAction_TYPE_REMOVE,
		"replace": api.CommunityAction_TYPE_REPLACE,
	}
	rpkiStates = map[string]api.ValidationState{
		"valid":     api.ValidationState_VALIDATION_STATE_VALID,
		"invalid":   api.ValidationState_VALIDATION_STATE_INVALID,
		"not-found": api.ValidationState_VALIDATION_STATE_NOT_FOUND,
	}
	bmpPolicies = map[string]api.AddBmpRequest_MonitoringPolicy{
		"":      api.AddBmpRequest_MONITORING_POLICY_PRE,
		"pre":   api.AddBmpRequest_MONITORING_POLICY_PRE,
		"post":  api.AddBmpRequest_MONITORING_POLICY_POST,
		"both":  api.AddBmpRequest_MONITORING_POLICY_BOTH,
		"local": api.AddBmpRequest_MONITORING_POLICY_LOCAL, // RFC 9069 Loc-RIB
		"all":   api.AddBmpRequest_MONITORING_POLICY_ALL,
	}
)

func apiFamily(name string) *api.Family {
	if name == config.IPv6Unicast {
		return familyV6
	}
	return familyV4
}

func apiGlobal(g config.Global) *api.Global {
	out := &api.Global{
		Asn:             g.ASN,
		RouterId:        g.RouterID,
		ListenPort:      g.ListenPort,
		ListenAddresses: g.ListenAddresses,
	}
	if c := g.Confederation; c != nil {
		out.Confederation = &api.Confederation{Enabled: true, Identifier: c.ID, MemberAsList: c.Members}
	}
	return out
}

// keychainName is the TCP-AO keychain that holds a neighbor's single key.
func keychainName(addr string) string { return "neighbor-" + addr }

func apiKeychain(n config.Neighbor) *api.TcpAoKeychain {
	return &api.TcpAoKeychain{Name: keychainName(n.Address), Keys: []*api.TcpAoKey{{
		SendId:    n.TcpAo.SendID,
		ReceiveId: n.TcpAo.ReceiveID,
		Algorithm: tcpAoAlgorithms[n.TcpAo.Algorithm],
		MasterKey: []byte(n.TcpAo.Key),
	}}}
}

func apiPeer(n config.Neighbor) *api.Peer {
	p := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: n.Address,
			PeerAsn:         n.RemoteASN,
			LocalAsn:        n.LocalASN,
			Description:     n.Description,
			AuthPassword:    n.Password,
			AdminDown:       n.AdminDown,
			AllowOwnAsn:     n.AllowOwnAS,
		},
		Timers: &api.Timers{Config: &api.TimersConfig{
			HoldTime:          n.HoldTime,
			KeepaliveInterval: n.Keepalive,
			ConnectRetry:      n.ConnectRetry,
		}},
		Transport: &api.Transport{
			LocalAddress: n.LocalAddress,
			PassiveMode:  n.Passive,
			RemotePort:   n.RemotePort,
		},
		RouteReflector: &api.RouteReflector{
			RouteReflectorClient:    n.RouteReflectorClient,
			RouteReflectorClusterId: n.ClusterID,
		},
		RouteServer: &api.RouteServer{RouteServerClient: n.RouteServerClient},
		ApplyPolicy: &api.ApplyPolicy{
			ImportPolicy: apiAssignment(n.Address, api.PolicyDirection_POLICY_DIRECTION_IMPORT, n.Import),
			ExportPolicy: apiAssignment(n.Address, api.PolicyDirection_POLICY_DIRECTION_EXPORT, n.Export),
		},
	}
	switch n.RemovePrivateAS {
	case "all":
		p.Conf.RemovePrivate = api.RemovePrivate_REMOVE_PRIVATE_ALL
	case "replace":
		p.Conf.RemovePrivate = api.RemovePrivate_REMOVE_PRIVATE_REPLACE
	}
	if n.EbgpMultihop > 0 {
		p.EbgpMultihop = &api.EbgpMultihop{Enabled: true, MultihopTtl: n.EbgpMultihop}
	}
	if n.TtlSecurityMin > 0 {
		p.TtlSecurity = &api.TtlSecurity{Enabled: true, TtlMin: n.TtlSecurityMin}
	}
	if n.TcpAo != nil {
		p.TcpAo = &api.TcpAoPeerConfig{Keychain: keychainName(n.Address), SendId: n.TcpAo.SendID}
	}
	if b := n.Bfd; b != nil {
		p.Bfd = &api.BfdPeerConfig{
			Enabled:                  true,
			DesiredMinimumTxInterval: b.MinTx * 1000, // GoBGP takes microseconds
			RequiredMinimumReceive:   b.MinRx * 1000,
			DetectionMultiplier:      b.Multiplier,
			Multihop:                 b.Multihop,
		}
	}
	gr := n.GracefulRestart
	if gr != nil {
		p.GracefulRestart = &api.GracefulRestart{
			Enabled:             true,
			RestartTime:         gr.RestartTime,
			HelperOnly:          gr.HelperOnly,
			NotificationEnabled: gr.Notification,
			LonglivedEnabled:    gr.LongLived,
		}
		if gr.RestartTime == 0 {
			p.GracefulRestart.RestartTime = 120
		}
	}
	for _, name := range n.Families {
		af := &api.AfiSafi{
			Config:   &api.AfiSafiConfig{Family: apiFamily(name), Enabled: true},
			AddPaths: &api.AddPaths{Config: &api.AddPathsConfig{Receive: n.AddPathReceive, SendMax: n.AddPathSendMax}},
		}
		if gr != nil {
			af.MpGracefulRestart = &api.MpGracefulRestart{Config: &api.MpGracefulRestartConfig{Enabled: true}}
			if gr.LongLived {
				af.LongLivedGracefulRestart = &api.LongLivedGracefulRestart{
					Config: &api.LongLivedGracefulRestartConfig{Enabled: true, RestartTime: gr.LlgrTime},
				}
			}
		}
		if n.MaxPrefixes > 0 {
			af.PrefixLimits = &api.PrefixLimit{Family: apiFamily(name), MaxPrefixes: n.MaxPrefixes}
		}
		p.AfiSafis = append(p.AfiSafis, af)
	}
	return p
}

// apiAssignment binds route maps to one direction of a table: a neighbor
// address, or "" for the global RIB.
func apiAssignment(name string, dir api.PolicyDirection, ref config.PolicyRef) *api.PolicyAssignment {
	a := &api.PolicyAssignment{Name: name, Direction: dir, DefaultAction: routeActions[ref.Default]}
	for _, p := range ref.Policies {
		a.Policies = append(a.Policies, &api.Policy{Name: p})
	}
	return a
}

func apiPolicies(p config.Policy) *api.SetPoliciesRequest {
	req := &api.SetPoliciesRequest{}
	for _, s := range p.PrefixSets {
		ds := &api.DefinedSet{DefinedType: api.DefinedType_DEFINED_TYPE_PREFIX, Name: s.Name}
		for _, m := range s.Prefixes {
			lo, hi := m.Ge, m.Le
			if lo == 0 && hi == 0 { // exact match
				bits := uint32(netip.MustParsePrefix(m.Prefix).Bits())
				lo, hi = bits, bits
			}
			ds.Prefixes = append(ds.Prefixes, &api.Prefix{IpPrefix: m.Prefix, MaskLengthMin: lo, MaskLengthMax: hi})
		}
		req.DefinedSets = append(req.DefinedSets, ds)
	}
	for typ, sets := range map[api.DefinedType][]config.NamedSet{
		api.DefinedType_DEFINED_TYPE_AS_PATH:         p.AsPathSets,
		api.DefinedType_DEFINED_TYPE_COMMUNITY:       p.CommunitySets,
		api.DefinedType_DEFINED_TYPE_EXT_COMMUNITY:   p.ExtCommunitySets,
		api.DefinedType_DEFINED_TYPE_LARGE_COMMUNITY: p.LargeCommunitySets,
	} {
		for _, s := range sets {
			req.DefinedSets = append(req.DefinedSets, &api.DefinedSet{DefinedType: typ, Name: s.Name, List: s.List})
		}
	}
	matchSet := func(r *config.SetRef) *api.MatchSet {
		if r == nil {
			return nil
		}
		return &api.MatchSet{Name: r.Name, Type: matchTypes[r.Match]}
	}
	community := func(a *config.CommunityAction) *api.CommunityAction {
		if a == nil {
			return nil
		}
		return &api.CommunityAction{Type: communityOps[a.Op], Communities: a.Values}
	}
	for _, m := range p.RouteMaps {
		pol := &api.Policy{Name: m.Name}
		for i, st := range m.Statements {
			act := &api.Actions{
				RouteAction:    routeActions[st.Action],
				Community:      community(st.Set.Communities),
				ExtCommunity:   community(st.Set.ExtCommunities),
				LargeCommunity: community(st.Set.LargeCommunities),
			}
			if a := st.Set.Med; a != nil {
				act.Med = &api.MedAction{Type: api.MedAction_TYPE_REPLACE, Value: a.Value}
				switch a.Op {
				case "add":
					act.Med.Type = api.MedAction_TYPE_MOD
				case "sub":
					act.Med.Type, act.Med.Value = api.MedAction_TYPE_MOD, -a.Value
				}
			}
			if st.Set.LocalPref != nil {
				act.LocalPref = &api.LocalPrefAction{Value: *st.Set.LocalPref}
			}
			if a := st.Set.AsPrepend; a != nil && a.Repeat > 0 {
				act.AsPrepend = &api.AsPrependAction{Asn: a.ASN, Repeat: a.Repeat, UseLeftMost: a.ASN == 0}
			}
			switch nh := st.Set.NextHop; nh {
			case "":
			case "self":
				act.Nexthop = &api.NexthopAction{Self: true}
			case "unchanged":
				act.Nexthop = &api.NexthopAction{Unchanged: true}
			default:
				act.Nexthop = &api.NexthopAction{Address: nh}
			}
			pol.Statements = append(pol.Statements, &api.Statement{
				// Statement names are global in GoBGP.
				Name: fmt.Sprintf("%s#%d", m.Name, i+1),
				Conditions: &api.Conditions{
					PrefixSet:         matchSet(st.Match.PrefixSet),
					AsPathSet:         matchSet(st.Match.AsPathSet),
					CommunitySet:      matchSet(st.Match.CommunitySet),
					ExtCommunitySet:   matchSet(st.Match.ExtCommunitySet),
					LargeCommunitySet: matchSet(st.Match.LargeCommunitySet),
					RpkiResult:        rpkiStates[st.Match.Rpki],
				},
				Actions: act,
			})
		}
		req.Policies = append(req.Policies, pol)
	}
	return req
}
