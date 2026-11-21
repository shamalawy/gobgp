package bgp

import (
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shamalawy/gobgp/internal/config"
	"github.com/shamalawy/gobgp/internal/mrt/mrttest"
)

// newTestEngine starts an engine that listens on addr, or not at all when
// addr is empty.
func newTestEngine(t testing.TB, asn uint32, addr string, port int32) *Engine {
	t.Helper()
	cfg := config.Default(asn, "10.0.0.1")
	cfg.Global.ListenPort = -1
	if addr != "" {
		cfg.Global.RouterID, cfg.Global.ListenPort, cfg.Global.ListenAddresses = addr, port, []string{addr}
	}
	return startEngine(t, filepath.Join(t.TempDir(), "bgpd.yaml"), cfg)
}

func startEngine(t testing.TB, path string, def *config.Config) *Engine {
	t.Helper()
	store, err := config.Open(path, def)
	if err != nil {
		t.Fatal(err)
	}
	e := New(store, slog.New(slog.DiscardHandler), "")
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	return e
}

// writeDump writes a gzip-compressed synthetic MRT dump and returns its path.
func writeDump(t testing.TB, v4, v6, sets int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rib.mrt.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if err := mrttest.Write(zw, v4, v6, sets); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func inject(t testing.TB, e *Engine, req InjectRequest) InjectStatus {
	t.Helper()
	if err := e.Inject(req, nil); err != nil {
		t.Fatal(err)
	}
	for {
		if st := e.InjectStatus(); st.State != "running" {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func query(t testing.TB, e *Engine, q RibQuery) *RibPage {
	t.Helper()
	q.Refresh = true
	if q.Limit == 0 {
		q.Limit = 100
	}
	page, err := e.QueryRIB(q)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func ptr(v uint32) *uint32 { return &v }

func TestInjectMRT(t *testing.T) {
	e := newTestEngine(t, 65000, "", 0)
	st := inject(t, e, InjectRequest{
		Source: writeDump(t, 5000, 1000, 50), Prepend: 2, LocalPref: ptr(250),
		Communities: []string{"65000:99"}, PeerAS: mrttest.PeerAS[1],
	})
	if st.State != "done" || st.Routes != 6000 || st.Failed != 0 {
		t.Fatalf("status %+v", st)
	}
	if st.AttrSets != 100 { // 50 per family, shared by 6000 routes
		t.Fatalf("got %d attribute sets, want 100", st.AttrSets)
	}
	if st.BytesTotal == 0 || st.BytesRead != st.BytesTotal {
		t.Fatalf("read %d of %d bytes", st.BytesRead, st.BytesTotal)
	}

	v4 := query(t, e, RibQuery{Family: "ipv4"})
	if v4.Total != 5000 || v4.Size != 5000 || len(v4.Rows) != 100 {
		t.Fatalf("ipv4: total %d size %d rows %d", v4.Total, v4.Size, len(v4.Rows))
	}
	r := v4.Rows[3]
	if r.Prefix != "1.0.3.0/24" || !r.Best || r.Peer != "" {
		t.Fatalf("unexpected row %+v", r)
	}
	if want := fmt.Sprintf("65000 65000 %d 3356 65539", mrttest.PeerAS[1]); r.AsPath != want {
		t.Fatalf("AS path %q, want %q", r.AsPath, want)
	}
	if r.NextHop != mrttest.NextHop4[1].String() || *r.LocalPref != 250 || *r.Med != 3 || r.Origin != "igp" {
		t.Fatalf("unexpected attributes %+v", r)
	}
	if strings.Join(r.Communities, " ") != "3356:3 3356:2 65000:99" || r.LargeCommunities[0] != "4200000001:1:3" {
		t.Fatalf("unexpected communities %v %v", r.Communities, r.LargeCommunities)
	}
	v6 := query(t, e, RibQuery{Family: "ipv6", Offset: 999})
	if v6.Total != 1000 || len(v6.Rows) != 1 || v6.Rows[0].NextHop != mrttest.NextHop6[1].String() {
		t.Fatalf("ipv6: %+v", v6)
	}

	if err := e.ClearInjected(); err != nil {
		t.Fatal(err)
	}
	if n := query(t, e, RibQuery{Family: "ipv4"}).Total; n != 0 {
		t.Fatalf("%d routes left after clearing", n)
	}
}

func TestInjectOptions(t *testing.T) {
	e := newTestEngine(t, 65000, "", 0)
	path := writeDump(t, 3000, 3000, 10)
	st := inject(t, e, InjectRequest{Source: path, Family: "ipv6", Limit: 1500, NextHopSelf: true, Rate: 20000})
	if st.State != "done" || st.Routes != 1500 {
		t.Fatalf("status %+v", st)
	}
	if took := st.Finished - st.Started; took < 60 { // 1500 routes at 20000/s, in batches of 1024
		t.Fatalf("rate limit not applied: took %d ms", took)
	}
	if n := query(t, e, RibQuery{Family: "ipv4"}).Total; n != 0 {
		t.Fatalf("family filter let %d IPv4 routes through", n)
	}
	if row := query(t, e, RibQuery{Family: "ipv6"}).Rows[0]; row.NextHop != "::" {
		t.Fatalf("next hop %q, want :: (self)", row.NextHop)
	}

	if st := inject(t, e, InjectRequest{Source: filepath.Join(t.TempDir(), "missing.mrt")}); st.State != "failed" || st.Error == "" {
		t.Fatalf("missing file: %+v", st)
	}
	if err := e.Inject(InjectRequest{Source: path, NextHop: "bogus"}, nil); err == nil {
		t.Fatal("want an error for an invalid next hop")
	}
}

func TestInjectFeeds(t *testing.T) {
	e := newTestEngine(t, 65000, "", 0)
	dir := t.TempDir()
	csv := filepath.Join(dir, "routes.csv")
	os.WriteFile(csv, []byte("prefix,next_hop,as_path,origin,med,local_pref,communities\n"+
		"203.0.113.0/24,192.0.2.1,65001 65002,egp,10,200,65001:1 65001:2:3 rt:65001:4\n"+
		"2001:db8:1::/48,2001:db8::1,65001,,,,\n"+
		"198.51.100.0/24\n"), 0o600)
	if st := inject(t, e, InjectRequest{Source: csv}); st.State != "done" || st.Routes != 3 {
		t.Fatalf("csv: %+v", st)
	}
	row := query(t, e, RibQuery{Family: "ipv4", Prefix: "203.0.113.0/24", Match: "exact"}).Rows[0]
	if row.AsPath != "65001 65002" || row.Origin != "egp" || *row.Med != 10 || *row.LocalPref != 200 ||
		row.Communities[0] != "65001:1" || row.LargeCommunities[0] != "65001:2:3" || row.ExtCommunities[0] != "rt:65001:4" {
		t.Fatalf("csv row %+v", row)
	}

	for name, body := range map[string]string{
		"array.json": `[{"prefix":"192.0.2.0/25","nextHop":"192.0.2.1","asPath":"65009"},
		                {"prefix":"2001:db8:2::/48","largeCommunities":["65009:1:1"]}]`,
		"lines.ndjson": `{"prefix":"192.0.2.128/25","med":5}` + "\n" + `{"prefix":"2001:db8:3::/48"}` + "\n",
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte(body), 0o600)
		if st := inject(t, e, InjectRequest{Source: path}); st.State != "done" || st.Routes != 2 {
			t.Fatalf("%s: %+v", name, st)
		}
	}
	if n := query(t, e, RibQuery{Family: "ipv4"}).Total; n != 4 {
		t.Fatalf("got %d IPv4 routes, want 4", n)
	}
	bad := filepath.Join(dir, "bad.csv")
	os.WriteFile(bad, []byte("10.0.0.0/8,not-an-address\n"), 0o600)
	if st := inject(t, e, InjectRequest{Source: bad}); st.State != "failed" {
		t.Fatalf("bad csv: %+v", st)
	}
}

func TestRibQuery(t *testing.T) {
	e := newTestEngine(t, 65000, "", 0)
	if st := inject(t, e, InjectRequest{Source: writeDump(t, 2000, 300, 20)}); st.State != "done" {
		t.Fatalf("status %+v", st)
	}
	for _, r := range []config.Route{
		{Prefix: "1.0.0.0/16", NextHop: "192.0.2.9", AsPath: "65010 65011"},
		{Prefix: "1.0.5.0/25", NextHop: "192.0.2.9"},
	} {
		if err := e.AddRoute(r); err != nil {
			t.Fatal(err)
		}
	}
	prefixes := func(q RibQuery) string {
		q.Family = "ipv4"
		var out []string
		for _, r := range query(t, e, q).Rows {
			out = append(out, r.Prefix)
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		q    RibQuery
		want string
	}{
		{RibQuery{Prefix: "1.0.5.0/24", Match: "exact"}, "1.0.5.0/24"},
		{RibQuery{Prefix: "1.0.5.200"}, "1.0.5.0/24"},                                          // an address: longest match
		{RibQuery{Prefix: "1.0.5.1"}, "1.0.5.0/25"},                                            // the more specific wins
		{RibQuery{Prefix: "1.0.5.0/24"}, "1.0.5.0/24 1.0.5.0/25"},                              // a CIDR: it and its subnets
		{RibQuery{Prefix: "1.0.5.0/25", Match: "shorter"}, "1.0.0.0/16 1.0.5.0/24 1.0.5.0/25"}, // covering routes
		{RibQuery{Prefix: "9.9.9.9"}, ""},
		{RibQuery{NextHop: "192.0.2.9"}, "1.0.0.0/16 1.0.5.0/25"},
		{RibQuery{AsPath: `^65010 `}, "1.0.0.0/16"},
		{RibQuery{AsPath: ` 65540$`, Limit: 2}, "1.0.4.0/24 1.0.24.0/24"},
		{RibQuery{Community: "3356:7", Prefix: "1.0.0.0/19"}, "1.0.7.0/24 1.0.27.0/24"},
	} {
		if got := prefixes(c.q); got != c.want {
			t.Errorf("%+v:\n got %q\nwant %q", c.q, got, c.want)
		}
	}
	if page := query(t, e, RibQuery{Family: "ipv4", Prefix: "1.0.0.0/16"}); page.Total != 258 || page.Size != 2002 {
		t.Fatalf("1.0.0.0/16 and longer: total %d size %d", page.Total, page.Size)
	}
	if page := query(t, e, RibQuery{Family: "ipv4", Best: "false"}); page.Total != 0 {
		t.Fatalf("%d non-best paths, want 0", page.Total)
	}

	// With an RPKI cache configured the Loc-RIB is read as best paths, and
	// the rows of a page are validated on their own.
	if err := e.AddRpki(context.Background(), config.RpkiServer{Address: "192.0.2.53", Port: 8282}); err != nil {
		t.Fatal(err)
	}
	page := query(t, e, RibQuery{Family: "ipv4", Prefix: "1.0.5.0/24"})
	if !page.BestOnly || page.Size != 2002 || page.Total != 2 || page.Rows[0].Rpki != "not-found" || !page.Rows[0].Best {
		t.Fatalf("with RPKI: %+v", page)
	}
	if page := query(t, e, RibQuery{Family: "ipv6"}); !page.BestOnly || page.Total != 300 {
		t.Fatalf("with RPKI, ipv6: total %d", page.Total)
	}
	for _, q := range []RibQuery{{Prefix: "nope"}, {AsPath: "("}, {NextHop: "x"}, {Table: "adj-in", Neighbor: "192.0.2.77"}} {
		if _, err := e.QueryRIB(q); err == nil {
			t.Errorf("%+v: want an error", q)
		}
	}
}

func TestConfigPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bgpd.yaml")
	def := config.Default(65000, "10.0.0.1")
	def.Global.ListenPort = -1
	e := startEngine(t, path, def)
	ctx := context.Background()

	policy := config.Policy{
		PrefixSets:    []config.PrefixSet{{Name: "bogons", Prefixes: []config.PrefixMatch{{Prefix: "10.0.0.0/8", Le: 32}}}},
		AsPathSets:    []config.NamedSet{{Name: "transit", List: []string{"_3356_"}}},
		CommunitySets: []config.NamedSet{{Name: "blackhole", List: []string{"65535:666"}}},
		RouteMaps: []config.RouteMap{{Name: "import-std", Statements: []config.Statement{
			{Match: config.Match{PrefixSet: &config.SetRef{Name: "bogons"}}, Action: "reject"},
			{Match: config.Match{AsPathSet: &config.SetRef{Name: "transit"}, Rpki: "valid"}, Action: "accept", Set: config.Set{
				LocalPref: ptr(200), Med: &config.MedAction{Op: "add", Value: 5}, NextHop: "self",
				AsPrepend:   &config.AsPrepend{ASN: 65000, Repeat: 2},
				Communities: &config.CommunityAction{Op: "add", Values: []string{"65000:1"}},
			}},
		}}},
	}
	if err := e.SetPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	n := config.Neighbor{
		Address: "2001:DB8::2", RemoteASN: 65002, Description: "upstream", Password: "s3cret", Passive: true,
		Families: []string{config.IPv4Unicast, config.IPv6Unicast}, HoldTime: 30, Keepalive: 10,
		RouteReflectorClient: true, AddPathReceive: true, AddPathSendMax: 4, TtlSecurityMin: 254,
		GracefulRestart: &config.GracefulRestart{RestartTime: 90, LongLived: true, LlgrTime: 3600},
		Import:          config.PolicyRef{Policies: []string{"import-std"}, Default: "reject"},
	}
	if err := e.AddNeighbor(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := e.AddNeighbor(ctx, n); err == nil {
		t.Fatal("want an error for a duplicate neighbor")
	}
	if err := e.AddRpki(ctx, config.RpkiServer{Address: "rtr.example.net", Port: 8282}); err == nil {
		t.Fatal("want an error for an RPKI cache given by name")
	}
	if err := e.AddRpki(ctx, config.RpkiServer{Address: "192.0.2.53", Port: 8282}); err != nil {
		t.Fatal(err)
	}
	if list, err := e.RpkiServers(ctx); err != nil || len(list) != 1 || list[0].Up {
		t.Fatalf("rpki servers %+v, err %v", list, err)
	}
	if err := e.DeleteRpki(ctx, "192.0.2.53", 8282); err != nil {
		t.Fatal(err)
	}
	if err := e.AddRoute(config.Route{Prefix: "203.0.113.7/24", Communities: []string{"no-export"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.NeighborAction(ctx, "2001:db8::2", "disable", "", "maintenance"); err != nil {
		t.Fatal(err)
	}

	// A policy that is still referenced, or broken, must be refused and
	// must not reach the file.
	if err := e.SetPolicy(ctx, config.Policy{}); err == nil {
		t.Fatal("want an error when removing a route map a neighbor uses")
	}
	if cfg := e.Config(); cfg.Neighbors[0].Password != Secret || len(cfg.Policy.RouteMaps) != 1 {
		t.Fatalf("config after refused update: %+v", cfg)
	}

	e.Stop()
	e = startEngine(t, path, nil) // everything must come back from the file
	st, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := st.Neighbors[0]
	if got.Address != "2001:db8::2" || !got.AdminDown || got.State != "Idle" || got.GracefulRestart.LlgrTime != 3600 {
		t.Fatalf("neighbor after restart: %+v", got)
	}
	if st.Ribs["ipv4"].LocRib != 1 {
		t.Fatalf("static route not restored: %+v", st.Ribs)
	}
	if row := query(t, e, RibQuery{Family: "ipv4"}).Rows[0]; row.Prefix != "203.0.113.0/24" || row.Communities[0] != "no-export" {
		t.Fatalf("restored route %+v", row)
	}

	// Editing with the masked secret keeps the stored one.
	got.Neighbor.Description = "edited"
	if err := e.UpdateNeighbor(ctx, got.Address, got.Neighbor); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "s3cret") || !strings.Contains(string(raw), "edited") {
		t.Fatalf("stored config lost the password or the edit:\n%s", raw)
	}
	if err := e.DeleteRoute("203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteNeighbor(ctx, got.Address); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.Status(ctx); len(st.Neighbors) != 0 || st.Ribs["ipv4"].LocRib != 0 {
		t.Fatalf("after deletes: %+v", st)
	}
}

// TestPeering runs two engines against each other over loopback: an eBGP
// session with both families, a full-table style injection on one side and
// an import policy on the other.
func TestPeering(t *testing.T) {
	const port = 17917
	ctx := context.Background()
	a := newTestEngine(t, 65001, "127.0.0.1", port)
	b := newTestEngine(t, 65002, "127.0.0.2", port)

	err := b.SetPolicy(ctx, config.Policy{
		PrefixSets: []config.PrefixSet{{Name: "drop", Prefixes: []config.PrefixMatch{{Prefix: "1.0.0.0/20", Ge: 24, Le: 24}}}},
		RouteMaps: []config.RouteMap{{Name: "in", Statements: []config.Statement{
			{Match: config.Match{PrefixSet: &config.SetRef{Name: "drop"}}, Action: "reject"},
			{Action: "accept", Set: config.Set{LocalPref: ptr(300)}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	families := []string{config.IPv4Unicast, config.IPv6Unicast}
	if err := b.AddNeighbor(ctx, config.Neighbor{
		Address: "127.0.0.1", RemoteASN: 65001, Families: families, Passive: true, LocalAddress: "127.0.0.2",
		Import: config.PolicyRef{Policies: []string{"in"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddNeighbor(ctx, config.Neighbor{
		Address: "127.0.0.2", RemoteASN: 65002, Families: families, RemotePort: port, LocalAddress: "127.0.0.1", ConnectRetry: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if st := inject(t, a, InjectRequest{Source: writeDump(t, 20000, 4000, 500), NextHopSelf: true}); st.State != "done" {
		t.Fatalf("status %+v", st)
	}

	var st *Status
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, _ = b.Status(ctx)
		n := st.Neighbors[0]
		if n.State == "Established" && n.Counts["ipv4"].Received == 20000 && n.Counts["ipv6"].Received == 4000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no full table after 30s: %+v", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
	n := st.Neighbors[0]
	if got := st.Ribs["ipv4"].LocRib; got != 20000-16 { // 1.0.0.0/20 holds 16 of the /24s
		t.Fatalf("Loc-RIB holds %d IPv4 routes, want %d", got, 20000-16)
	}
	for _, c := range []string{"mp-ipv4", "mp-ipv6", "4-octet-as", "route-refresh", "extended-message", "extended-nexthop"} {
		if !strings.Contains(strings.Join(n.Capabilities, " "), c) {
			t.Errorf("capability %s not negotiated: %v", c, n.Capabilities)
		}
	}
	if as, _ := a.Status(ctx); as.Ribs["ipv4"].AdjOut != 20000 || as.Ribs["ipv6"].AdjOut != 4000 {
		t.Fatalf("sender Adj-RIB-Out counts: %+v", as.Ribs)
	}

	row := query(t, b, RibQuery{Family: "ipv4", Prefix: "1.0.16.0/24", Match: "exact"}).Rows[0]
	if row.Peer != "127.0.0.1" || row.PeerASN != 65001 || row.NextHop != "127.0.0.1" || *row.LocalPref != 300 ||
		!strings.HasPrefix(row.AsPath, "65001 64500 3356 ") {
		t.Fatalf("received route %+v", row)
	}
	if page := query(t, b, RibQuery{Family: "ipv4", Prefix: "1.0.0.0/20"}); page.Total != 0 {
		t.Fatalf("%d filtered routes reached the Loc-RIB", page.Total)
	}
	if page := query(t, b, RibQuery{Table: "adj-in", Neighbor: "127.0.0.1", Family: "ipv4", Prefix: "1.0.0.0/20"}); page.Total != 16 {
		t.Fatalf("Adj-RIB-In holds %d of the filtered routes, want 16", page.Total)
	}
	if page := query(t, a, RibQuery{Table: "adj-out", Neighbor: "127.0.0.2", Family: "ipv6"}); page.Total != 4000 {
		t.Fatalf("Adj-RIB-Out holds %d IPv6 routes, want 4000", page.Total)
	}

	// Administrative shutdown tears the session down and withdraws the routes.
	if err := b.NeighborAction(ctx, "127.0.0.1", "disable", "", "test"); err != nil {
		t.Fatal(err)
	}
	for st, _ = b.Status(ctx); st.Ribs["ipv4"].LocRib != 0; st, _ = b.Status(ctx) {
		if time.Now().After(deadline) {
			t.Fatalf("routes not withdrawn after shutdown: %+v", st.Ribs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// BenchmarkInjectFullTable injects an internet-sized table (1,000,000 IPv4
// and 200,000 IPv6 routes over 180,000 attribute sets) from a gzip MRT dump
// into a fresh engine. Run it once: go test -bench InjectFullTable -benchtime 1x
func BenchmarkInjectFullTable(b *testing.B) {
	const v4, v6, sets = 1_000_000, 200_000, 90_000
	path := writeDump(b, v4, v6, sets)
	for b.Loop() {
		e := newTestEngine(b, 65000, "", 0)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		st := inject(b, e, InjectRequest{Source: path})
		if st.State != "done" || st.Routes != v4+v6 || st.Failed != 0 {
			b.Fatalf("status %+v", st)
		}
		secs := float64(st.Finished-st.Started) / 1000
		runtime.GC()
		runtime.ReadMemStats(&after)
		b.ReportMetric(float64(st.Routes)/secs, "routes/s")
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/float64(st.Routes), "heap-B/route")
		b.ReportMetric(float64(st.AttrSets), "attr-sets")

		start := time.Now()
		page := query(b, e, RibQuery{Family: "ipv4", Offset: 900_000})
		b.ReportMetric(time.Since(start).Seconds(), "snapshot-s")
		if page.Total != v4 {
			b.Fatalf("RIB holds %d IPv4 routes", page.Total)
		}
		start = time.Now()
		for i := range 100 { // paging and lookups run against the snapshot
			q := RibQuery{Family: "ipv4", Offset: i * 9000, Limit: 200}
			if i%2 == 1 {
				q = RibQuery{Family: "ipv4", Prefix: mrttest.Prefix4(i * 9000).Addr().String(), Offset: 1}
			}
			if _, err := e.QueryRIB(q); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(time.Since(start).Microseconds())/100, "query-µs")
		e.Stop()
	}
}
