package bgp

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"

	"github.com/shamalawy/gobgp/internal/config"
)

var origins = []string{"igp", "egp", "incomplete"}

// extPrefixes maps the textual prefix of an extended community to its subtype.
var extPrefixes = map[string]bgp.ExtendedCommunityAttrSubType{
	"rt":  bgp.EC_SUBTYPE_ROUTE_TARGET,
	"soo": bgp.EC_SUBTYPE_ROUTE_ORIGIN,
}

func familyOf(p netip.Prefix) bgp.Family {
	if p.Addr().Is4() {
		return bgp.RF_IPv4_UC
	}
	return bgp.RF_IPv6_UC
}

// newPath builds the path GoBGP's AddPath and DeletePath expect. The next hop
// travels in a NEXT_HOP attribute whatever its family; GoBGP turns it into
// MP_REACH_NLRI where the wire format needs one. An unspecified next hop
// makes GoBGP substitute the session's local address ("next-hop-self").
func newPath(pfx netip.Prefix, nexthop bgp.PathAttributeInterface, attrs []bgp.PathAttributeInterface) *apiutil.Path {
	nlri, _ := bgp.NewIPAddrPrefix(pfx)
	all := make([]bgp.PathAttributeInterface, 0, len(attrs)+1)
	return &apiutil.Path{Family: familyOf(pfx), Nlri: nlri, Attrs: append(append(all, attrs...), nexthop)}
}

func nexthopAttr(addr netip.Addr) bgp.PathAttributeInterface {
	a, _ := bgp.NewPathAttributeNextHop(addr)
	return a
}

// unspecified returns the "self" next hop for the family of pfx.
func unspecified(pfx netip.Prefix) netip.Addr {
	if pfx.Addr().Is4() {
		return netip.IPv4Unspecified()
	}
	return netip.IPv6Unspecified()
}

// routeAttrs converts a Route into its prefix, next hop and the remaining
// path attributes.
func routeAttrs(r config.Route) (netip.Prefix, netip.Addr, []bgp.PathAttributeInterface, error) {
	fail := func(format string, a ...any) (netip.Prefix, netip.Addr, []bgp.PathAttributeInterface, error) {
		return netip.Prefix{}, netip.Addr{}, nil, fmt.Errorf("route %s: "+format, append([]any{r.Prefix}, a...)...)
	}
	pfx, err := netip.ParsePrefix(r.Prefix)
	if err != nil {
		return fail("invalid prefix")
	}
	pfx = pfx.Masked()
	nh := unspecified(pfx)
	if r.NextHop != "" {
		if nh, err = netip.ParseAddr(r.NextHop); err != nil {
			return fail("invalid next hop %q", r.NextHop)
		}
		if pfx.Addr().Is6() && nh.Is4() {
			return fail("an IPv6 prefix needs an IPv6 next hop")
		}
	}

	origin := 0
	if r.Origin != "" {
		if origin = slices.Index(origins, strings.ToLower(r.Origin)); origin < 0 {
			return fail("invalid origin %q", r.Origin)
		}
	}
	attrs := []bgp.PathAttributeInterface{bgp.NewPathAttributeOrigin(uint8(origin))}

	var asns []uint32
	for _, f := range strings.Fields(r.AsPath) {
		as, err := strconv.ParseUint(f, 10, 32)
		if err != nil || as == 0 {
			return fail("invalid AS %q in AS path", f)
		}
		asns = append(asns, uint32(as))
	}
	if len(asns) > 255 {
		return fail("AS path longer than 255")
	}
	if len(asns) > 0 {
		attrs = append(attrs, bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, asns)}))
	}
	if r.Med != nil {
		attrs = append(attrs, bgp.NewPathAttributeMultiExitDisc(*r.Med))
	}
	if r.LocalPref != nil {
		attrs = append(attrs, bgp.NewPathAttributeLocalPref(*r.LocalPref))
	}
	if len(r.Communities) > 0 {
		vals := make([]uint32, 0, len(r.Communities))
		for _, s := range r.Communities {
			v, err := parseCommunity(s)
			if err != nil {
				return fail("%v", err)
			}
			vals = append(vals, v)
		}
		attrs = append(attrs, bgp.NewPathAttributeCommunities(vals))
	}
	if len(r.ExtCommunities) > 0 {
		vals := make([]bgp.ExtendedCommunityInterface, 0, len(r.ExtCommunities))
		for _, s := range r.ExtCommunities {
			kind, val, _ := strings.Cut(s, ":")
			sub, ok := extPrefixes[strings.ToLower(kind)]
			if !ok {
				return fail("extended community %q must start with rt: or soo:", s)
			}
			ec, err := bgp.ParseExtendedCommunity(sub, val)
			if err != nil {
				return fail("invalid extended community %q", s)
			}
			vals = append(vals, ec)
		}
		attrs = append(attrs, bgp.NewPathAttributeExtendedCommunities(vals))
	}
	if len(r.LargeCommunities) > 0 {
		vals := make([]*bgp.LargeCommunity, 0, len(r.LargeCommunities))
		for _, s := range r.LargeCommunities {
			lc, err := bgp.ParseLargeCommunity(s)
			if err != nil {
				return fail("invalid large community %q", s)
			}
			vals = append(vals, lc)
		}
		attrs = append(attrs, bgp.NewPathAttributeLargeCommunities(vals))
	}
	return pfx, nh, attrs, nil
}

func parseCommunity(s string) (uint32, error) {
	if v, ok := bgp.WellKnownCommunityValueMap[strings.ToLower(s)]; ok {
		return uint32(v), nil
	}
	hi, lo, ok := strings.Cut(s, ":")
	if ok {
		h, err1 := strconv.ParseUint(hi, 10, 16)
		l, err2 := strconv.ParseUint(lo, 10, 16)
		if err1 == nil && err2 == nil {
			return uint32(h<<16 | l), nil
		}
	} else if v, err := strconv.ParseUint(s, 10, 32); err == nil {
		return uint32(v), nil
	}
	return 0, fmt.Errorf("invalid community %q", s)
}

// renderAttrs is the inverse of routeAttrs for the attributes GoBGP reports.
func renderAttrs(attrs []bgp.PathAttributeInterface) config.Route {
	var r config.Route
	for _, a := range attrs {
		switch a := a.(type) {
		case *bgp.PathAttributeOrigin:
			if int(a.Value) < len(origins) {
				r.Origin = origins[a.Value]
			}
		case *bgp.PathAttributeAsPath:
			r.AsPath = bgp.AsPathString(a)
		case *bgp.PathAttributeNextHop:
			r.NextHop = a.Value.String()
		case *bgp.PathAttributeMpReachNLRI:
			r.NextHop = a.Nexthop.String()
		case *bgp.PathAttributeMultiExitDisc:
			r.Med = &a.Value
		case *bgp.PathAttributeLocalPref:
			r.LocalPref = &a.Value
		case *bgp.PathAttributeCommunities:
			for _, v := range a.Value {
				if name, ok := bgp.WellKnownCommunityNameMap[bgp.WellKnownCommunity(v)]; ok {
					r.Communities = append(r.Communities, name)
				} else {
					r.Communities = append(r.Communities, fmt.Sprintf("%d:%d", v>>16, v&0xffff))
				}
			}
		case *bgp.PathAttributeExtendedCommunities:
			for _, ec := range a.Value {
				s := ec.String()
				_, sub := ec.GetTypes()
				for prefix, t := range extPrefixes {
					if t == sub {
						s = prefix + ":" + s
					}
				}
				r.ExtCommunities = append(r.ExtCommunities, s)
			}
		case *bgp.PathAttributeLargeCommunities:
			for _, lc := range a.Values {
				r.LargeCommunities = append(r.LargeCommunities, lc.String())
			}
		}
	}
	return r
}
