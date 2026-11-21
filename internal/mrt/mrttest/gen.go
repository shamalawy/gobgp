// Package mrttest generates synthetic TABLE_DUMP_V2 dumps for tests and
// benchmarks. It writes them with GoBGP's MRT serializer, so the reader in
// package mrt is checked against an independent implementation.
package mrttest

import (
	"encoding/binary"
	"io"
	"net/netip"
	"time"

	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/packet/mrt"
)

// Collector peers of every generated dump. Peer i announces each prefix with
// next hop NextHop4[i] / NextHop6[i] and an AS path starting with PeerAS[i].
var (
	PeerAS   = []uint32{64500, 4200000001}
	PeerIP   = []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8::1")}
	NextHop4 = []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")}
	NextHop6 = []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2")}
)

// Prefix4 and Prefix6 return the i-th generated prefix of each family.
func Prefix4(i int) netip.Prefix {
	var a [4]byte
	binary.BigEndian.PutUint32(a[:], 1<<24+uint32(i)<<8)
	return netip.PrefixFrom(netip.AddrFrom4(a), 24)
}

func Prefix6(i int) netip.Prefix {
	a := netip.MustParseAddr("2400::").As16()
	binary.BigEndian.PutUint32(a[2:], uint32(i))
	return netip.PrefixFrom(netip.AddrFrom16(a), 48)
}

// Write emits a dump with v4 IPv4 /24s and v6 IPv6 /48s. Every prefix has one
// entry per collector peer, and the entries cycle through attrSets distinct
// attribute sets per peer, like the few hundred thousand a real table has.
func Write(w io.Writer, v4, v6, attrSets int) error {
	now := time.Unix(1700000000, 0)
	record := func(sub mrt.MRTSubTypeTableDumpv2, body mrt.Body) error {
		m, err := mrt.NewMRTMessage(now, mrt.TABLE_DUMPv2, sub, body)
		if err != nil {
			return err
		}
		b, err := m.Serialize()
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
	peers := make([]*mrt.Peer, len(PeerAS))
	for i := range peers {
		peers[i] = mrt.NewPeer(netip.MustParseAddr("192.0.2.1"), PeerIP[i], PeerAS[i], true)
	}
	if err := record(mrt.PEER_INDEX_TABLE, mrt.NewPeerIndexTable(netip.MustParseAddr("192.0.2.254"), "test", peers)); err != nil {
		return err
	}

	// attrs[family][peer][set]
	var attrs [2][][][]bgp.PathAttributeInterface
	for fam := range attrs {
		attrs[fam] = make([][][]bgp.PathAttributeInterface, len(PeerAS))
		for peer := range PeerAS {
			for set := range attrSets {
				path := bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, []uint32{PeerAS[peer], 3356, uint32(65536 + set)})
				a := []bgp.PathAttributeInterface{
					bgp.NewPathAttributeOrigin(bgp.BGP_ORIGIN_ATTR_TYPE_IGP),
					bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{path}),
				}
				if fam == 0 {
					nh, _ := bgp.NewPathAttributeNextHop(NextHop4[peer])
					a = append(a, nh)
				}
				a = append(a,
					bgp.NewPathAttributeMultiExitDisc(uint32(set%7)),
					bgp.NewPathAttributeCommunities([]uint32{3356<<16 | uint32(set%1000), 3356<<16 | 2}),
					bgp.NewPathAttributeLargeCommunities([]*bgp.LargeCommunity{bgp.NewLargeCommunity(PeerAS[peer], 1, uint32(set))}),
				)
				attrs[fam][peer] = append(attrs[fam][peer], a)
			}
		}
	}
	for fam, n := range []int{v4, v6} {
		sub, family := mrt.RIB_IPV4_UNICAST, bgp.RF_IPv4_UC
		if fam == 1 {
			sub, family = mrt.RIB_IPV6_UNICAST, bgp.RF_IPv6_UC
		}
		for i := range n {
			pfx := Prefix4(i)
			if fam == 1 {
				pfx = Prefix6(i)
			}
			nlri, _ := bgp.NewIPAddrPrefix(pfx)
			entries := make([]*mrt.RibEntry, len(PeerAS))
			for peer := range entries {
				a := attrs[fam][peer][i%attrSets]
				if fam == 1 { // MP_REACH_NLRI is per prefix; the writer abbreviates it
					mp, _ := bgp.NewPathAttributeMpReachNLRI(family, []bgp.PathNLRI{{NLRI: nlri}}, NextHop6[peer])
					a = append(a[:len(a):len(a)], mp)
				}
				entries[peer] = mrt.NewRibEntry(uint16(peer), uint32(now.Unix()), 0, a, false)
			}
			if err := record(sub, mrt.NewRib(uint32(i), family, nlri, entries)); err != nil {
				return err
			}
		}
	}
	return nil
}
