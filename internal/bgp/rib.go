package bgp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gaissmai/bart"
	"github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/server"

	"github.com/shamalawy/gobgp/internal/config"
)

const (
	// Reading a full table out of GoBGP takes seconds, so a snapshot is
	// reused for 20 times what it cost to build, within these bounds. A
	// client paging through it keeps it for the upper bound, so scrolling
	// sees a stable table.
	snapshotMinTTL = 5 * time.Second
	snapshotMaxTTL = 5 * time.Minute
	maxPage        = 1000
)

// RibQuery selects a page of a RIB.
type RibQuery struct {
	Table     string // "global" (Loc-RIB), "adj-in" or "adj-out"
	Neighbor  string // for adj-in and adj-out
	Family    string // "ipv4" or "ipv6"
	Prefix    string // CIDR or address
	Match     string // exact, longest, longer, shorter; default longer for a CIDR, longest for an address
	NextHop   string
	AsPath    string // regular expression over the AS path
	Community string // exact standard, extended or large community
	Best      string // "true", "false" or "" for both
	Offset    int
	Limit     int
	Refresh   bool // rebuild the snapshot even if a recent one exists
}

type RibRow struct {
	config.Route
	Best    bool   `json:"best"`
	Stale   bool   `json:"stale,omitempty"`
	Peer    string `json:"peer,omitempty"` // empty for locally originated routes
	PeerASN uint32 `json:"peerAsn,omitempty"`
	Age     int64  `json:"age"` // unix seconds
	PathID  uint32 `json:"pathId,omitempty"`
	Rpki    string `json:"rpki,omitempty"`
}

type RibPage struct {
	Total    int      `json:"total"`    // rows matching the filter
	Size     int      `json:"size"`     // rows in the table
	Snapshot int64    `json:"snapshot"` // unix milliseconds the table was read
	BestOnly bool     `json:"bestOnly"` // the snapshot holds best paths only
	Rows     []RibRow `json:"rows"`
}

// ribRow is one path. Rows are kept compact because a snapshot of the full
// internet table holds over a million of them; the attributes are shared.
type ribRow struct {
	prefix netip.Prefix
	attrs  *config.Route
	age    int64
	pathID uint32
	seq    uint32 // position in GoBGP's listing, which puts the best path first
	peer   uint16 // index into ribSnapshot.peers
	best   bool
	stale  bool
	rpki   uint8
}

type ribPeer struct {
	addr string
	asn  uint32
}

// ribSnapshot is a point-in-time copy of one table, sorted by prefix, with a
// radix trie over the prefixes for exact, longest, covering and covered
// lookups.
type ribSnapshot struct {
	taken time.Time
	ttl   time.Duration
	// bestOnly marks a Loc-RIB snapshot read without GoBGP's origin
	// validation; QueryRIB then validates just the rows it returns.
	bestOnly bool
	rows     []ribRow
	peers    []ribPeer
	trie     bart.Table[int32] // prefix -> index of its first row

	viewKey string  // the filter the cached view belongs to
	view    []int32 // matching row indexes; nil means every row
}

type ribCache struct {
	mu    sync.Mutex
	snaps map[string]*ribSnapshot
}

func (c *ribCache) reset() {
	c.mu.Lock()
	c.snaps = nil
	c.mu.Unlock()
}

var rpkiNames = map[api.ValidationState]string{
	api.ValidationState_VALIDATION_STATE_VALID:     "valid",
	api.ValidationState_VALIDATION_STATE_INVALID:   "invalid",
	api.ValidationState_VALIDATION_STATE_NOT_FOUND: "not-found",
}

// QueryRIB returns one page of a table. The table is read from GoBGP into a
// snapshot on the first request and paged from there.
func (e *Engine) QueryRIB(q RibQuery) (*RibPage, error) {
	req := apiutil.ListPathRequest{Name: q.Neighbor, Family: bgp.RF_IPv4_UC}
	switch q.Table {
	case "", "global":
		req.TableType, req.Name = api.TableType_TABLE_TYPE_GLOBAL, ""
	case "adj-in":
		req.TableType = api.TableType_TABLE_TYPE_ADJ_IN
	case "adj-out":
		req.TableType = api.TableType_TABLE_TYPE_ADJ_OUT
	default:
		return nil, fmt.Errorf("unknown table %q", q.Table)
	}
	if q.Family == "ipv6" {
		req.Family = bgp.RF_IPv6_UC
	}
	filter, err := newRibFilter(q)
	if err != nil {
		return nil, err
	}

	c := &e.rib
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%d|%s|%d", req.TableType, req.Name, req.Family)
	snap := c.snaps[key]
	if snap != nil && q.Offset > 0 {
		snap.ttl = snapshotMaxTTL
	}
	if snap == nil || q.Refresh || time.Since(snap.taken) > snap.ttl {
		if snap, err = newRibSnapshot(e, req); err != nil {
			return nil, err
		}
		// One snapshot at a time bounds memory to a single table copy.
		c.snaps = map[string]*ribSnapshot{key: snap}
	}
	if snap.viewKey != filter.key {
		snap.viewKey, snap.view = filter.key, filter.apply(snap)
	}

	total := len(snap.rows)
	if snap.view != nil {
		total = len(snap.view)
	}
	page := &RibPage{
		Total: total, Size: len(snap.rows), Snapshot: snap.taken.UnixMilli(), BestOnly: snap.bestOnly, Rows: []RibRow{},
	}
	limit := min(max(q.Limit, 1), maxPage)
	for i := max(q.Offset, 0); i < min(q.Offset+limit, total); i++ {
		idx := i
		if snap.view != nil {
			idx = int(snap.view[i])
		}
		r := &snap.rows[idx]
		row := RibRow{
			Route: *r.attrs, Best: r.best, Stale: r.stale, Age: r.age, PathID: r.pathID,
			Peer: snap.peers[r.peer].addr, PeerASN: snap.peers[r.peer].asn, Rpki: rpkiNames[api.ValidationState(r.rpki)],
		}
		row.Prefix = r.prefix.String()
		page.Rows = append(page.Rows, row)
	}
	if snap.bestOnly {
		e.validateRows(req.Family, page.Rows)
	}
	return page, nil
}

// validateRows fills in the RPKI state of a page of best paths with one exact
// lookup per prefix.
func (e *Engine) validateRows(family bgp.Family, rows []RibRow) {
	req := apiutil.ListPathRequest{TableType: api.TableType_TABLE_TYPE_GLOBAL, Family: family}
	for _, r := range rows {
		req.Prefixes = append(req.Prefixes, &apiutil.LookupPrefix{Prefix: r.Prefix})
	}
	if len(rows) == 0 {
		return
	}
	states := map[string]string{}
	// A failed lookup leaves the column empty; the rows themselves stand.
	_ = e.server().ListPath(req, func(nlri bgp.NLRI, paths []*apiutil.Path) {
		if len(paths) > 0 {
			states[nlri.String()] = rpkiNames[paths[0].Validation.GetState()]
		}
	})
	for i := range rows {
		rows[i].Rpki = states[rows[i].Prefix]
	}
}

// listBestPaths reads the best paths of one family from the Loc-RIB through
// a watcher, whose first event is the current table.
//
// ListPath is not used for this once an RPKI cache is configured: GoBGP then
// validates every path of the listed table while holding its server lock,
// which stalls the speaker for minutes on a full table.
func (e *Engine) listBestPaths(family bgp.Family, fn func(bgp.NLRI, []*apiutil.Path)) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	table := make(chan []*apiutil.Path, 1)
	err := e.server().WatchEvent(ctx, server.WatchEventMessageCallbacks{
		OnBestPath: func(paths []*apiutil.Path, _ time.Time) {
			select {
			case table <- paths:
			default: // a later update; only the initial table is wanted
			}
		},
	}, server.WatchBestPath(true))
	if err != nil {
		return err
	}
	select {
	case paths := <-table:
		for _, p := range paths {
			if p.Family == family && !p.Withdrawal {
				p.Best = true
				fn(p.Nlri, []*apiutil.Path{p})
			}
		}
		return nil
	case <-ctx.Done():
		return errors.New("timed out reading the Loc-RIB")
	}
}

func newRibSnapshot(e *Engine, req apiutil.ListPathRequest) (*ribSnapshot, error) {
	start := time.Now()
	snap := &ribSnapshot{peers: []ribPeer{{}}}
	peerIdx := map[netip.Addr]uint16{{}: 0}
	in := newAttrInterner()
	add := func(nlri bgp.NLRI, paths []*apiutil.Path) {
		p, ok := nlri.(*bgp.IPAddrPrefix)
		if !ok {
			return
		}
		for _, path := range paths {
			idx, ok := peerIdx[path.PeerAddress]
			if !ok {
				idx = uint16(len(snap.peers))
				peerIdx[path.PeerAddress] = idx
				snap.peers = append(snap.peers, ribPeer{addr: path.PeerAddress.String(), asn: path.PeerASN})
			}
			snap.rows = append(snap.rows, ribRow{
				prefix: p.Prefix, attrs: in.intern(path.Attrs), age: path.Age, pathID: path.RemoteID,
				seq: uint32(len(snap.rows)), peer: idx, best: path.Best, stale: path.Stale,
				rpki: uint8(path.Validation.GetState()),
			})
		}
	}
	var err error
	if snap.bestOnly = req.TableType == api.TableType_TABLE_TYPE_GLOBAL && len(e.store.Get().Rpki) > 0; snap.bestOnly {
		err = e.listBestPaths(req.Family, add)
	} else {
		err = e.server().ListPath(req, add)
	}
	if err != nil {
		return nil, err
	}
	slices.SortFunc(snap.rows, func(a, b ribRow) int {
		return cmp.Or(a.prefix.Compare(b.prefix), cmp.Compare(a.seq, b.seq))
	})
	for i := range snap.rows {
		if i == 0 || snap.rows[i].prefix != snap.rows[i-1].prefix {
			snap.trie.Insert(snap.rows[i].prefix, int32(i))
		}
	}
	snap.taken = time.Now()
	snap.ttl = min(max(20*snap.taken.Sub(start), snapshotMinTTL), snapshotMaxTTL)
	return snap, nil
}

// attrInterner renders each distinct attribute set once. Paths that came from
// one UPDATE, or from one interned bulk injection set, share their attribute
// objects, so comparing object identities finds most repeats without
// rendering; the rendered content catches the rest.
type attrInterner struct {
	byAsPath map[bgp.PathAttributeInterface][]internedAttrs // candidates sharing an AS_PATH object
	byValue  map[string]*config.Route
}

type internedAttrs struct {
	attrs []bgp.PathAttributeInterface
	route *config.Route
}

func newAttrInterner() *attrInterner {
	return &attrInterner{byAsPath: map[bgp.PathAttributeInterface][]internedAttrs{}, byValue: map[string]*config.Route{}}
}

func (in *attrInterner) intern(attrs []bgp.PathAttributeInterface) *config.Route {
	var asPath bgp.PathAttributeInterface
	for _, a := range attrs {
		if a.GetType() == bgp.BGP_ATTR_TYPE_AS_PATH {
			asPath = a
			break
		}
	}
	for _, c := range in.byAsPath[asPath] {
		if sameAttrs(c.attrs, attrs) {
			return c.route
		}
	}
	r := renderAttrs(attrs)
	value := fmt.Sprint(r.NextHop, "|", r.AsPath, "|", r.Origin, "|", deref(r.Med), "|", deref(r.LocalPref), "|",
		r.Communities, r.ExtCommunities, r.LargeCommunities)
	shared, ok := in.byValue[value]
	if !ok {
		shared = &r
		in.byValue[value] = shared
	}
	in.byAsPath[asPath] = append(in.byAsPath[asPath], internedAttrs{attrs, shared})
	return shared
}

// sameAttrs reports whether two attribute lists hold the same objects. The
// next hop attributes are compared by value: GoBGP builds them per path.
func sameAttrs(a, b []bgp.PathAttributeInterface) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		switch x := a[i].(type) {
		case *bgp.PathAttributeNextHop:
			if y, ok := b[i].(*bgp.PathAttributeNextHop); !ok || x.Value != y.Value {
				return false
			}
		case *bgp.PathAttributeMpReachNLRI:
			if y, ok := b[i].(*bgp.PathAttributeMpReachNLRI); !ok || x.Nexthop != y.Nexthop {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func deref(v *uint32) int64 {
	if v == nil {
		return -1
	}
	return int64(*v)
}

type ribFilter struct {
	key       string
	prefix    netip.Prefix
	match     string
	nextHop   string
	asPath    *regexp.Regexp
	community string
	best      string
}

func newRibFilter(q RibQuery) (*ribFilter, error) {
	f := &ribFilter{
		key:   strings.Join([]string{q.Prefix, q.Match, q.NextHop, q.AsPath, q.Community, q.Best}, "\x00"),
		match: q.Match, community: q.Community, best: q.Best,
	}
	if q.Prefix != "" {
		if addr, err := netip.ParseAddr(q.Prefix); err == nil {
			f.prefix = netip.PrefixFrom(addr, addr.BitLen())
			if f.match == "" {
				f.match = "longest"
			}
		} else if f.prefix, err = netip.ParsePrefix(q.Prefix); err != nil {
			return nil, fmt.Errorf("invalid prefix %q", q.Prefix)
		}
		f.prefix = f.prefix.Masked()
		if f.match == "" {
			f.match = "longer"
		}
		if !slices.Contains([]string{"exact", "longest", "longer", "shorter"}, f.match) {
			return nil, fmt.Errorf("invalid match %q", f.match)
		}
	}
	if q.NextHop != "" {
		nh, err := netip.ParseAddr(q.NextHop)
		if err != nil {
			return nil, fmt.Errorf("invalid next hop %q", q.NextHop)
		}
		f.nextHop = nh.String()
	}
	if q.AsPath != "" {
		re, err := regexp.Compile(q.AsPath)
		if err != nil {
			return nil, fmt.Errorf("invalid AS path expression: %v", err)
		}
		f.asPath = re
	}
	return f, nil
}

// apply returns the indexes of the rows that pass the filter, in table order,
// or nil when every row does.
func (f *ribFilter) apply(s *ribSnapshot) []int32 {
	byAttrs := f.nextHop != "" || f.asPath != nil || f.community != ""
	if !f.prefix.IsValid() && !byAttrs && f.best == "" {
		return nil
	}
	verdicts := map[*config.Route]bool{} // attribute sets are shared, so each is judged once
	keep := func(r *ribRow) bool {
		if f.best != "" && r.best != (f.best == "true") {
			return false
		}
		if !byAttrs {
			return true
		}
		ok, seen := verdicts[r.attrs]
		if !seen {
			a := r.attrs
			ok = (f.nextHop == "" || a.NextHop == f.nextHop) &&
				(f.asPath == nil || f.asPath.MatchString(a.AsPath)) &&
				(f.community == "" || slices.Contains(a.Communities, f.community) ||
					slices.Contains(a.ExtCommunities, f.community) || slices.Contains(a.LargeCommunities, f.community))
			verdicts[r.attrs] = ok
		}
		return ok
	}

	view := []int32{}
	if !f.prefix.IsValid() {
		for i := range s.rows {
			if keep(&s.rows[i]) {
				view = append(view, int32(i))
			}
		}
		return view
	}
	var starts []int32
	switch f.match {
	case "exact":
		if i, ok := s.trie.Get(f.prefix); ok {
			starts = append(starts, i)
		}
	case "longest":
		if _, i, ok := s.trie.LookupPrefixLPM(f.prefix); ok {
			starts = append(starts, i)
		}
	case "longer":
		for _, i := range s.trie.Subnets(f.prefix) {
			starts = append(starts, i)
		}
	case "shorter":
		for _, i := range s.trie.Supernets(f.prefix) {
			starts = append(starts, i)
		}
	}
	slices.Sort(starts)
	for _, start := range starts {
		for i := int(start); i < len(s.rows) && s.rows[i].prefix == s.rows[start].prefix; i++ {
			if keep(&s.rows[i]) {
				view = append(view, int32(i))
			}
		}
	}
	return view
}
