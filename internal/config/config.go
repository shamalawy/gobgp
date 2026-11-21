// Package config defines the daemon's persistent configuration: the schema,
// its validation and an atomic file-backed store.
package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
)

// Family names accepted in Neighbor.Families.
const (
	IPv4Unicast = "ipv4-unicast"
	IPv6Unicast = "ipv6-unicast"
)

type Config struct {
	Global    Global       `json:"global"`
	Neighbors []Neighbor   `json:"neighbors"`
	Policy    Policy       `json:"policy"`
	Routes    []Route      `json:"routes"`
	Rpki      []RpkiServer `json:"rpki"`
	Bmp       []BmpServer  `json:"bmp"`
}

type Global struct {
	ASN             uint32         `json:"asn"`
	RouterID        string         `json:"routerId"`
	ListenPort      int32          `json:"listenPort"` // -1 disables the listener
	ListenAddresses []string       `json:"listenAddresses,omitempty"`
	Confederation   *Confederation `json:"confederation,omitempty"` // RFC 5065
	Import          PolicyRef      `json:"import,omitzero"`
	Export          PolicyRef      `json:"export,omitzero"`
}

type Confederation struct {
	ID      uint32   `json:"id"`
	Members []uint32 `json:"members"`
}

// PolicyRef assigns route maps to a direction. Default is "accept", "reject"
// or empty, which for a neighbor means "inherit the global assignment".
type PolicyRef struct {
	Policies []string `json:"policies,omitempty"`
	Default  string   `json:"default,omitempty"`
}

type Neighbor struct {
	Address     string   `json:"address"`
	RemoteASN   uint32   `json:"remoteAsn"`
	LocalASN    uint32   `json:"localAsn,omitempty"`
	Description string   `json:"description,omitempty"`
	AdminDown   bool     `json:"adminDown,omitempty"` // RFC 4486 administrative shutdown
	Families    []string `json:"families,omitempty"`  // defaults to the family of Address

	Passive      bool   `json:"passive,omitempty"`
	LocalAddress string `json:"localAddress,omitempty"`
	RemotePort   uint32 `json:"remotePort,omitempty"`

	HoldTime     uint64 `json:"holdTime,omitempty"`
	Keepalive    uint64 `json:"keepalive,omitempty"`
	ConnectRetry uint64 `json:"connectRetry,omitempty"`

	Password       string `json:"password,omitempty"`       // RFC 2385 TCP MD5
	TcpAo          *TcpAo `json:"tcpAo,omitempty"`          // RFC 5925
	EbgpMultihop   uint32 `json:"ebgpMultihop,omitempty"`   // TTL, 0 = directly connected
	TtlSecurityMin uint32 `json:"ttlSecurityMin,omitempty"` // RFC 5082 GTSM, 0 = off

	RouteReflectorClient bool   `json:"routeReflectorClient,omitempty"` // RFC 4456
	ClusterID            string `json:"clusterId,omitempty"`
	RouteServerClient    bool   `json:"routeServerClient,omitempty"`

	AddPathReceive bool   `json:"addPathReceive,omitempty"` // RFC 7911
	AddPathSendMax uint32 `json:"addPathSendMax,omitempty"`

	GracefulRestart *GracefulRestart `json:"gracefulRestart,omitempty"`
	Bfd             *Bfd             `json:"bfd,omitempty"`

	RemovePrivateAS string `json:"removePrivateAs,omitempty"` // "", "all", "replace"
	AllowOwnAS      uint32 `json:"allowOwnAs,omitempty"`
	MaxPrefixes     uint32 `json:"maxPrefixes,omitempty"`

	Import PolicyRef `json:"import,omitzero"`
	Export PolicyRef `json:"export,omitzero"`
}

type TcpAo struct {
	SendID    uint32 `json:"sendId"`
	ReceiveID uint32 `json:"receiveId"`
	Algorithm string `json:"algorithm"` // hmac-sha1-96, aes-128-cmac-96, hmac-sha256-96, hmac-sha256-128
	Key       string `json:"key"`
}

// GracefulRestart covers RFC 4724 and, with LongLived, RFC 9494.
type GracefulRestart struct {
	RestartTime  uint32 `json:"restartTime,omitempty"`
	HelperOnly   bool   `json:"helperOnly,omitempty"`
	Notification bool   `json:"notification,omitempty"` // RFC 8538
	LongLived    bool   `json:"longLived,omitempty"`
	LlgrTime     uint32 `json:"llgrTime,omitempty"`
}

// Bfd intervals are in milliseconds.
type Bfd struct {
	MinTx      uint32 `json:"minTx,omitempty"`
	MinRx      uint32 `json:"minRx,omitempty"`
	Multiplier uint32 `json:"multiplier,omitempty"`
	Multihop   bool   `json:"multihop,omitempty"`
}

type Policy struct {
	PrefixSets         []PrefixSet `json:"prefixSets"`
	AsPathSets         []NamedSet  `json:"asPathSets"`
	CommunitySets      []NamedSet  `json:"communitySets"`
	ExtCommunitySets   []NamedSet  `json:"extCommunitySets"`
	LargeCommunitySets []NamedSet  `json:"largeCommunitySets"`
	RouteMaps          []RouteMap  `json:"routeMaps"`
}

type PrefixSet struct {
	Name     string        `json:"name"`
	Prefixes []PrefixMatch `json:"prefixes"`
}

// PrefixMatch matches Prefix and its more-specifics with a length in
// [Ge, Le]. Both zero means an exact match.
type PrefixMatch struct {
	Prefix string `json:"prefix"`
	Ge     uint32 `json:"ge,omitempty"`
	Le     uint32 `json:"le,omitempty"`
}

// NamedSet is a list of regular expressions (AS path, communities).
type NamedSet struct {
	Name string   `json:"name"`
	List []string `json:"list"`
}

type RouteMap struct {
	Name       string      `json:"name"`
	Statements []Statement `json:"statements"`
}

// Statement applies Set and Action to routes that satisfy every condition in
// Match. An empty Action falls through to the next statement.
type Statement struct {
	Match  Match  `json:"match"`
	Set    Set    `json:"set"`
	Action string `json:"action,omitempty"` // accept, reject or ""
}

type Match struct {
	PrefixSet         *SetRef `json:"prefixSet,omitempty"`
	AsPathSet         *SetRef `json:"asPathSet,omitempty"`
	CommunitySet      *SetRef `json:"communitySet,omitempty"`
	ExtCommunitySet   *SetRef `json:"extCommunitySet,omitempty"`
	LargeCommunitySet *SetRef `json:"largeCommunitySet,omitempty"`
	Rpki              string  `json:"rpki,omitempty"` // valid, invalid, not-found
}

type SetRef struct {
	Name  string `json:"name"`
	Match string `json:"match,omitempty"` // any (default), all, invert
}

type Set struct {
	Med              *MedAction       `json:"med,omitempty"`
	LocalPref        *uint32          `json:"localPref,omitempty"`
	AsPrepend        *AsPrepend       `json:"asPrepend,omitempty"`
	NextHop          string           `json:"nextHop,omitempty"` // "self", "unchanged" or an address
	Communities      *CommunityAction `json:"communities,omitempty"`
	ExtCommunities   *CommunityAction `json:"extCommunities,omitempty"`
	LargeCommunities *CommunityAction `json:"largeCommunities,omitempty"`
}

type MedAction struct {
	Op    string `json:"op"` // set, add, sub
	Value int64  `json:"value"`
}

// AsPrepend prepends ASN Repeat times; ASN 0 repeats the leftmost AS.
type AsPrepend struct {
	ASN    uint32 `json:"asn"`
	Repeat uint32 `json:"repeat"`
}

type CommunityAction struct {
	Op     string   `json:"op"` // add, remove, replace
	Values []string `json:"values"`
}

// Route is a single prefix with its path attributes. It is the model for
// statically originated routes, bulk feeds and RIB rows.
type Route struct {
	Prefix           string   `json:"prefix"`
	NextHop          string   `json:"nextHop,omitempty"` // empty = self
	AsPath           string   `json:"asPath,omitempty"`  // space separated ASNs
	Origin           string   `json:"origin,omitempty"`  // igp (default), egp, incomplete
	Med              *uint32  `json:"med,omitempty"`
	LocalPref        *uint32  `json:"localPref,omitempty"`
	Communities      []string `json:"communities,omitempty"`
	ExtCommunities   []string `json:"extCommunities,omitempty"` // rt:65000:1, soo:65000:1
	LargeCommunities []string `json:"largeCommunities,omitempty"`
}

type RpkiServer struct {
	Address string `json:"address"`
	Port    uint32 `json:"port"`
}

type BmpServer struct {
	Address string `json:"address"`
	Port    uint32 `json:"port"`
	Policy  string `json:"policy,omitempty"` // pre (default), post, both, local, all
}

// Default returns the configuration used when no file exists yet.
func Default(asn uint32, routerID string) *Config {
	return &Config{Global: Global{ASN: asn, RouterID: routerID, ListenPort: 179}}
}

// Neighbor returns the neighbor with the given address.
func (c *Config) Neighbor(addr string) *Neighbor {
	for i := range c.Neighbors {
		if c.Neighbors[i].Address == addr {
			return &c.Neighbors[i]
		}
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// Validate normalizes addresses and prefixes and rejects a configuration the
// engine could not apply.
func (c *Config) Validate() error {
	g := &c.Global
	if g.ASN == 0 {
		return fmt.Errorf("global: asn is required")
	}
	if id, err := netip.ParseAddr(g.RouterID); err != nil || !id.Is4() {
		return fmt.Errorf("global: routerId must be an IPv4 address")
	}
	if g.ListenPort == 0 {
		g.ListenPort = 179
	}
	if g.Confederation != nil && g.Confederation.ID == 0 {
		g.Confederation = nil
	}

	sets := map[string]map[string]bool{}
	checkNames := func(kind string, names []string) error {
		seen := map[string]bool{}
		for _, n := range names {
			if !nameRE.MatchString(n) {
				return fmt.Errorf("%s: invalid name %q", kind, n)
			}
			if seen[n] {
				return fmt.Errorf("%s: duplicate name %q", kind, n)
			}
			seen[n] = true
		}
		sets[kind] = seen
		return nil
	}
	p := &c.Policy
	named := func(l []NamedSet) (names []string) {
		for _, s := range l {
			names = append(names, s.Name)
		}
		return names
	}
	var prefixSetNames, mapNames []string
	for i := range p.PrefixSets {
		prefixSetNames = append(prefixSetNames, p.PrefixSets[i].Name)
		for j := range p.PrefixSets[i].Prefixes {
			m := &p.PrefixSets[i].Prefixes[j]
			pfx, err := netip.ParsePrefix(m.Prefix)
			if err != nil {
				return fmt.Errorf("prefix set %s: %v", p.PrefixSets[i].Name, err)
			}
			pfx = pfx.Masked()
			m.Prefix = pfx.String()
			maxLen := uint32(pfx.Addr().BitLen())
			if m.Ge != 0 || m.Le != 0 {
				if m.Le == 0 {
					m.Le = maxLen
				}
				if m.Ge == 0 {
					m.Ge = uint32(pfx.Bits())
				}
				if m.Ge < uint32(pfx.Bits()) || m.Ge > m.Le || m.Le > maxLen {
					return fmt.Errorf("prefix set %s: %s needs %d <= ge <= le <= %d", p.PrefixSets[i].Name, m.Prefix, pfx.Bits(), maxLen)
				}
			}
		}
	}
	for _, m := range p.RouteMaps {
		mapNames = append(mapNames, m.Name)
	}
	for kind, names := range map[string][]string{
		"prefix set": prefixSetNames, "as-path set": named(p.AsPathSets),
		"community set": named(p.CommunitySets), "ext-community set": named(p.ExtCommunitySets),
		"large-community set": named(p.LargeCommunitySets), "route map": mapNames,
	} {
		if err := checkNames(kind, names); err != nil {
			return err
		}
	}
	maps := sets["route map"]
	for _, s := range p.AsPathSets {
		for _, re := range s.List {
			if _, err := regexp.Compile(re); err != nil {
				return fmt.Errorf("as-path set %s: %v", s.Name, err)
			}
		}
	}
	for _, m := range p.RouteMaps {
		for i, st := range m.Statements {
			where := fmt.Sprintf("route map %s statement %d", m.Name, i+1)
			for kind, ref := range map[string]*SetRef{
				"prefix set": st.Match.PrefixSet, "as-path set": st.Match.AsPathSet,
				"community set": st.Match.CommunitySet, "ext-community set": st.Match.ExtCommunitySet,
				"large-community set": st.Match.LargeCommunitySet,
			} {
				if ref == nil {
					continue
				}
				if !sets[kind][ref.Name] {
					return fmt.Errorf("%s: unknown %s %q", where, kind, ref.Name)
				}
				if !slices.Contains([]string{"", "any", "all", "invert"}, ref.Match) {
					return fmt.Errorf("%s: invalid match type %q", where, ref.Match)
				}
			}
			if !slices.Contains([]string{"", "valid", "invalid", "not-found"}, st.Match.Rpki) {
				return fmt.Errorf("%s: invalid rpki state %q", where, st.Match.Rpki)
			}
			if !slices.Contains([]string{"", "accept", "reject"}, st.Action) {
				return fmt.Errorf("%s: invalid action %q", where, st.Action)
			}
			if a := st.Set.Med; a != nil && !slices.Contains([]string{"set", "add", "sub"}, a.Op) {
				return fmt.Errorf("%s: invalid med op %q", where, a.Op)
			}
			if nh := st.Set.NextHop; nh != "" && nh != "self" && nh != "unchanged" {
				if _, err := netip.ParseAddr(nh); err != nil {
					return fmt.Errorf("%s: invalid next hop %q", where, nh)
				}
			}
			for _, a := range []*CommunityAction{st.Set.Communities, st.Set.ExtCommunities, st.Set.LargeCommunities} {
				if a != nil && !slices.Contains([]string{"add", "remove", "replace"}, a.Op) {
					return fmt.Errorf("%s: invalid community op %q", where, a.Op)
				}
			}
		}
	}
	checkRef := func(where string, r PolicyRef) error {
		if !slices.Contains([]string{"", "accept", "reject"}, r.Default) {
			return fmt.Errorf("%s: invalid default action %q", where, r.Default)
		}
		for _, n := range r.Policies {
			if !maps[n] {
				return fmt.Errorf("%s: unknown route map %q", where, n)
			}
		}
		return nil
	}
	if err := checkRef("global import", g.Import); err != nil {
		return err
	}
	if err := checkRef("global export", g.Export); err != nil {
		return err
	}

	seen := map[string]bool{}
	for i := range c.Neighbors {
		n := &c.Neighbors[i]
		addr, err := netip.ParseAddr(n.Address)
		if err != nil {
			return fmt.Errorf("neighbor %q: invalid address", n.Address)
		}
		n.Address = addr.String()
		if seen[n.Address] {
			return fmt.Errorf("neighbor %s: duplicate", n.Address)
		}
		seen[n.Address] = true
		if n.RemoteASN == 0 {
			return fmt.Errorf("neighbor %s: remoteAsn is required", n.Address)
		}
		if len(n.Families) == 0 {
			n.Families = []string{IPv4Unicast}
			if addr.Is6() {
				n.Families = []string{IPv6Unicast}
			}
		}
		for _, f := range n.Families {
			if f != IPv4Unicast && f != IPv6Unicast {
				return fmt.Errorf("neighbor %s: unsupported family %q", n.Address, f)
			}
		}
		if n.LocalAddress != "" {
			if _, err := netip.ParseAddr(n.LocalAddress); err != nil {
				return fmt.Errorf("neighbor %s: invalid localAddress", n.Address)
			}
		}
		if n.ClusterID != "" {
			if id, err := netip.ParseAddr(n.ClusterID); err != nil || !id.Is4() {
				return fmt.Errorf("neighbor %s: clusterId must be an IPv4 address", n.Address)
			}
		}
		if n.HoldTime != 0 && n.HoldTime < 3 {
			return fmt.Errorf("neighbor %s: holdTime must be 0 or >= 3", n.Address)
		}
		if n.Keepalive != 0 && n.HoldTime != 0 && n.Keepalive > n.HoldTime/3 {
			return fmt.Errorf("neighbor %s: keepalive must be at most a third of holdTime", n.Address)
		}
		if !slices.Contains([]string{"", "all", "replace"}, n.RemovePrivateAS) {
			return fmt.Errorf("neighbor %s: invalid removePrivateAs %q", n.Address, n.RemovePrivateAS)
		}
		if n.TtlSecurityMin > 255 || n.EbgpMultihop > 255 {
			return fmt.Errorf("neighbor %s: TTL values must be <= 255", n.Address)
		}
		if n.TtlSecurityMin != 0 && n.EbgpMultihop != 0 {
			return fmt.Errorf("neighbor %s: ttlSecurityMin and ebgpMultihop are mutually exclusive", n.Address)
		}
		if n.Password != "" && n.TcpAo != nil {
			return fmt.Errorf("neighbor %s: password (MD5) and tcpAo are mutually exclusive", n.Address)
		}
		if a := n.TcpAo; a != nil {
			if a.Key == "" || a.SendID > 255 || a.ReceiveID > 255 {
				return fmt.Errorf("neighbor %s: tcpAo needs a key and ids <= 255", n.Address)
			}
			if !slices.Contains([]string{"hmac-sha1-96", "aes-128-cmac-96", "hmac-sha256-96", "hmac-sha256-128"}, a.Algorithm) {
				return fmt.Errorf("neighbor %s: invalid tcpAo algorithm %q", n.Address, a.Algorithm)
			}
		}
		if err := checkRef("neighbor "+n.Address+" import", n.Import); err != nil {
			return err
		}
		if err := checkRef("neighbor "+n.Address+" export", n.Export); err != nil {
			return err
		}
	}

	seen = map[string]bool{}
	for i := range c.Routes {
		r := &c.Routes[i]
		pfx, err := netip.ParsePrefix(r.Prefix)
		if err != nil {
			return fmt.Errorf("route %q: invalid prefix", r.Prefix)
		}
		r.Prefix = pfx.Masked().String()
		if seen[r.Prefix] {
			return fmt.Errorf("route %s: duplicate", r.Prefix)
		}
		seen[r.Prefix] = true
	}
	// Lists are never null in the API or the file.
	for _, l := range []*[]NamedSet{&p.AsPathSets, &p.CommunitySets, &p.ExtCommunitySets, &p.LargeCommunitySets} {
		*l = nonNil(*l)
	}
	p.PrefixSets, p.RouteMaps = nonNil(p.PrefixSets), nonNil(p.RouteMaps)
	c.Neighbors, c.Routes, c.Rpki, c.Bmp = nonNil(c.Neighbors), nonNil(c.Routes), nonNil(c.Rpki), nonNil(c.Bmp)

	for i := range c.Rpki {
		s := &c.Rpki[i]
		// GoBGP dials a host name but then fails to list the session.
		addr, err := netip.ParseAddr(s.Address)
		if err != nil || s.Port == 0 || s.Port > 65535 {
			return fmt.Errorf("rpki server %q: an IP address and a port are required", s.Address)
		}
		s.Address = addr.String()
	}
	for _, s := range c.Bmp {
		if s.Address == "" || s.Port == 0 || s.Port > 65535 {
			return fmt.Errorf("bmp server %q: address and port are required", s.Address)
		}
		if !slices.Contains([]string{"", "pre", "post", "both", "local", "all"}, s.Policy) {
			return fmt.Errorf("bmp server %s: invalid policy %q", s.Address, s.Policy)
		}
	}
	return nil
}

func nonNil[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}
