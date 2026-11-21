package mrt_test

import (
	"bytes"
	"compress/gzip"
	"net/netip"
	"testing"

	"github.com/osrg/gobgp/v4/pkg/packet/bgp"

	"github.com/shamalawy/gobgp/internal/mrt"
	"github.com/shamalawy/gobgp/internal/mrt/mrttest"
)

func dump(t testing.TB, v4, v6, sets int) []byte {
	var buf bytes.Buffer
	if err := mrttest.Write(&buf, v4, v6, sets); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func read(t *testing.T, raw []byte, f mrt.Filter) []mrt.Route {
	var routes []mrt.Route
	err := mrt.Read(bytes.NewReader(raw), f, func(b []mrt.Route) error {
		routes = append(routes, b...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return routes
}

func TestReadSelectsPeer(t *testing.T) {
	raw := dump(t, 3000, 500, 40) // spans several batches
	for peer, f := range []mrt.Filter{{}, {PeerAS: mrttest.PeerAS[1]}, {PeerIP: mrttest.PeerIP[0]}} {
		peer %= 2 // the third filter selects peer 0 again, by address
		routes := read(t, raw, f)
		if len(routes) != 3500 {
			t.Fatalf("filter %+v: got %d routes, want 3500", f, len(routes))
		}
		for i, r := range routes {
			want, nexthop := mrttest.Prefix4(i), mrttest.NextHop4[peer]
			if i >= 3000 {
				want, nexthop = mrttest.Prefix6(i-3000), mrttest.NextHop6[peer]
			}
			if r.Prefix != want {
				t.Fatalf("route %d: prefix %v, want %v", i, r.Prefix, want)
			}
			attrs, nh, err := mrt.ParseAttrs(r.Attrs)
			if err != nil {
				t.Fatalf("route %d: %v", i, err)
			}
			if nh != nexthop {
				t.Fatalf("route %d: next hop %v, want %v", i, nh, nexthop)
			}
			var asPath string
			for _, a := range attrs {
				switch a := a.(type) {
				case *bgp.PathAttributeAsPath:
					asPath = bgp.AsPathString(a)
				case *bgp.PathAttributeNextHop, *bgp.PathAttributeMpReachNLRI:
					t.Fatalf("route %d: next hop attribute left in the list", i)
				}
			}
			if want := bgp.AsPathString(bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{
				bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, []uint32{mrttest.PeerAS[peer], 3356, uint32(65536 + i%40)}),
			})); asPath != want {
				t.Fatalf("route %d: AS path %q, want %q", i, asPath, want)
			}
		}
	}
}

func TestReadUnknownPeer(t *testing.T) {
	err := mrt.Read(bytes.NewReader(dump(t, 1, 0, 1)), mrt.Filter{PeerAS: 1}, func([]mrt.Route) error { return nil })
	if err == nil {
		t.Fatal("want an error for a peer that is not in the dump")
	}
}

func TestReadTruncated(t *testing.T) {
	raw := dump(t, 10, 10, 2)
	for _, n := range []int{len(raw) - 1, len(raw) - 20, 13} {
		if err := mrt.Read(bytes.NewReader(raw[:n]), mrt.Filter{}, func([]mrt.Route) error { return nil }); err == nil {
			t.Fatalf("truncated at %d: want an error", n)
		}
	}
}

func TestDecompress(t *testing.T) {
	raw := dump(t, 100, 0, 5)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw)
	zw.Close()
	for name, in := range map[string][]byte{"plain": raw, "gzip": gz.Bytes()} {
		r, err := mrt.Decompress(bytes.NewReader(in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n := 0
		if err := mrt.Read(r, mrt.Filter{}, func(b []mrt.Route) error { n += len(b); return nil }); err != nil || n != 100 {
			t.Fatalf("%s: got %d routes, err %v", name, n, err)
		}
	}
}

// TestParseAttrsFullMpReach covers writers that put the whole MP_REACH_NLRI
// attribute in a RIB entry instead of the abbreviated form.
func TestParseAttrsFullMpReach(t *testing.T) {
	nh := netip.MustParseAddr("2001:db8::9")
	value := append([]byte{0, 2, 1, 16}, nh.AsSlice()...)
	value = append(value, 0, 48, 0x24, 0, 0, 0, 0, 1) // reserved, 2400:0:1::/48
	raw := append([]byte{0x80, 14, byte(len(value))}, value...)
	if _, got, err := mrt.ParseAttrs(raw); err != nil || got != nh {
		t.Fatalf("next hop %v, err %v; want %v", got, err, nh)
	}
}

func BenchmarkRead(b *testing.B) {
	raw := dump(b, 200000, 0, 30000)
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		if err := mrt.Read(bytes.NewReader(raw), mrt.Filter{}, func([]mrt.Route) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}
