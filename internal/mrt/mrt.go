// Package mrt is a streaming reader for MRT TABLE_DUMP_V2 RIB snapshots
// (RFC 6396, RFC 8050), the format of RIPE RIS "bview" and RouteViews "rib"
// files.
//
// It is built for bulk injection: a RIB record lists one entry per collector
// peer, and only the selected entry is copied out, as raw path attribute
// bytes. Parsing those bytes is left to the consumer, which can do it once per
// distinct attribute set instead of once per route.
package mrt

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"

	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
)

const (
	typeTableDumpV2 = 13

	subPeerIndexTable     = 1
	subRibIPv4Unicast     = 2
	subRibIPv6Unicast     = 4
	subRibIPv4UnicastPath = 8 // RFC 8050 ADD-PATH variants
	subRibIPv6UnicastPath = 10

	headerLen    = 12
	maxRecordLen = 16 << 20 // sanity bound; real records are a few KiB
	batchSize    = 1024
)

// Route is one prefix with the raw path attributes of its selected entry.
// The attributes use 4-octet AS numbers. NEXT_HOP may carry an IPv6 address
// and MP_REACH_NLRI may be in the abbreviated form of RFC 6396 4.3.4;
// ParseAttrs understands both.
type Route struct {
	Prefix netip.Prefix
	Attrs  []byte
}

// Filter selects which collector peer's view to extract. The zero value takes
// the first entry of every prefix, which yields the most complete table.
type Filter struct {
	PeerAS uint32
	PeerIP netip.Addr
}

// Decompress wraps r with a gzip or bzip2 reader when the stream starts with
// the matching magic number.
func Decompress(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	magic, err := br.Peek(3)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	switch {
	case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		return gzip.NewReader(br)
	case len(magic) == 3 && string(magic) == "BZh":
		return bzip2.NewReader(br), nil
	}
	return br, nil
}

// Read decodes an uncompressed MRT stream and hands the routes to emit in
// batches. emit owns each batch; returning an error stops the read.
func Read(r io.Reader, f Filter, emit func([]Route) error) error {
	var (
		hdr     [headerLen]byte
		body    []byte
		wanted  map[uint16]bool // peer indexes matching f; nil = any
		batch   = make([]Route, 0, batchSize)
		arena   []byte
		records int
	)
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				if len(batch) > 0 {
					return emit(batch)
				}
				return nil
			}
			return fmt.Errorf("mrt record %d: %w", records+1, err)
		}
		records++
		typ, sub := binary.BigEndian.Uint16(hdr[4:]), binary.BigEndian.Uint16(hdr[6:])
		n := binary.BigEndian.Uint32(hdr[8:])
		if n > maxRecordLen {
			return fmt.Errorf("mrt record %d: length %d exceeds %d", records, n, maxRecordLen)
		}
		if cap(body) < int(n) {
			body = make([]byte, n)
		}
		body = body[:n]
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("mrt record %d: %w", records, err)
		}

		var v6, addPath bool
		switch {
		case typ != typeTableDumpV2:
			continue // not a RIB snapshot record
		case sub == subPeerIndexTable:
			if f != (Filter{}) {
				var err error
				if wanted, err = matchPeers(body, f); err != nil {
					return err
				}
			}
			continue
		case sub == subRibIPv4Unicast:
		case sub == subRibIPv6Unicast:
			v6 = true
		case sub == subRibIPv4UnicastPath:
			addPath = true
		case sub == subRibIPv6UnicastPath:
			v6, addPath = true, true
		default:
			continue // multicast and generic RIBs
		}
		pfx, attrs, err := parseRib(body, v6, addPath, wanted)
		if err != nil {
			return fmt.Errorf("mrt record %d: %w", records, err)
		}
		if attrs == nil {
			continue // no entry from the selected peer
		}
		// The body buffer is reused, so the attributes move to an arena
		// that lives as long as the batch.
		if cap(arena)-len(arena) < len(attrs) {
			arena = make([]byte, 0, max(64<<10, len(attrs)))
		}
		start := len(arena)
		arena = append(arena, attrs...)
		batch = append(batch, Route{Prefix: pfx, Attrs: arena[start:len(arena):len(arena)]})
		if len(batch) == batchSize {
			if err := emit(batch); err != nil {
				return err
			}
			batch, arena = make([]Route, 0, batchSize), nil
		}
	}
}

var errShort = errors.New("truncated record")

// matchPeers returns the indexes of the PEER_INDEX_TABLE entries selected by f.
func matchPeers(b []byte, f Filter) (map[uint16]bool, error) {
	if len(b) < 6 {
		return nil, errShort
	}
	b = b[4:] // collector BGP ID
	nameLen := int(binary.BigEndian.Uint16(b))
	if len(b) < 2+nameLen+2 {
		return nil, errShort
	}
	b = b[2+nameLen:]
	count := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	wanted := map[uint16]bool{}
	for i := range count {
		if len(b) < 5 {
			return nil, errShort
		}
		ipLen, asLen := 4, 2
		if b[0]&1 != 0 {
			ipLen = 16
		}
		if b[0]&2 != 0 {
			asLen = 4
		}
		b = b[5:] // peer type, peer BGP ID
		if len(b) < ipLen+asLen {
			return nil, errShort
		}
		ip, _ := netip.AddrFromSlice(b[:ipLen])
		as := uint32(binary.BigEndian.Uint16(b[ipLen:]))
		if asLen == 4 {
			as = binary.BigEndian.Uint32(b[ipLen:])
		}
		b = b[ipLen+asLen:]
		if (f.PeerAS == 0 || f.PeerAS == as) && (!f.PeerIP.IsValid() || f.PeerIP == ip) {
			wanted[uint16(i)] = true
		}
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("no collector peer matches AS %d address %v", f.PeerAS, f.PeerIP)
	}
	return wanted, nil
}

// parseRib returns the prefix of a RIB record and the attributes of its first
// entry whose peer index is wanted (any entry when wanted is nil). The
// attributes alias b.
func parseRib(b []byte, v6, addPath bool, wanted map[uint16]bool) (netip.Prefix, []byte, error) {
	if len(b) < 5 {
		return netip.Prefix{}, nil, errShort
	}
	bits := int(b[4])
	n := (bits + 7) / 8
	b = b[5:]
	if len(b) < n+2 {
		return netip.Prefix{}, nil, errShort
	}
	var addr netip.Addr
	if v6 {
		var a [16]byte
		if n > copy(a[:], b[:n]) {
			return netip.Prefix{}, nil, fmt.Errorf("invalid IPv6 prefix length %d", bits)
		}
		addr = netip.AddrFrom16(a)
	} else {
		var a [4]byte
		if n > copy(a[:], b[:n]) {
			return netip.Prefix{}, nil, fmt.Errorf("invalid IPv4 prefix length %d", bits)
		}
		addr = netip.AddrFrom4(a)
	}
	pfx := netip.PrefixFrom(addr, bits).Masked()
	count := int(binary.BigEndian.Uint16(b[n:]))
	b = b[n+2:]
	fixed := 8 // peer index, originated time, attribute length
	if addPath {
		fixed = 12
	}
	for range count {
		if len(b) < fixed {
			return netip.Prefix{}, nil, errShort
		}
		idx := binary.BigEndian.Uint16(b)
		attrLen := int(binary.BigEndian.Uint16(b[fixed-2:]))
		b = b[fixed:]
		if len(b) < attrLen {
			return netip.Prefix{}, nil, errShort
		}
		if wanted == nil || wanted[idx] {
			return pfx, b[:attrLen:attrLen], nil
		}
		b = b[attrLen:]
	}
	return pfx, nil, nil
}

// ParseAttrs decodes raw path attributes as found in Route.Attrs. The next
// hop is returned separately; MP_REACH_NLRI, MP_UNREACH_NLRI and the AS4
// compatibility attributes are dropped, the rest keep their wire order.
func ParseAttrs(raw []byte) (attrs []bgp.PathAttributeInterface, nexthop netip.Addr, err error) {
	for len(raw) > 0 {
		if len(raw) < 3 {
			return nil, nexthop, errShort
		}
		hdr, n := 3, int(raw[2])
		if bgp.BGPAttrFlag(raw[0])&bgp.BGP_ATTR_FLAG_EXTENDED_LENGTH != 0 {
			if len(raw) < 4 {
				return nil, nexthop, errShort
			}
			hdr, n = 4, int(binary.BigEndian.Uint16(raw[2:]))
		}
		if len(raw) < hdr+n {
			return nil, nexthop, errShort
		}
		val := raw[hdr : hdr+n]
		switch bgp.BGPAttrType(raw[1]) {
		case bgp.BGP_ATTR_TYPE_NEXT_HOP:
			nexthop, _ = netip.AddrFromSlice(val)
		case bgp.BGP_ATTR_TYPE_MP_REACH_NLRI:
			// Abbreviated form: next hop length, next hop. Some writers
			// emit the full attribute, which starts with AFI and SAFI.
			if n > 0 && int(val[0]) != n-1 && n > 4 {
				val = val[3:]
			}
			if n == 0 || len(val) < 1+int(val[0]) || val[0] < 4 {
				return nil, nexthop, fmt.Errorf("malformed MP_REACH_NLRI")
			}
			// A 32-byte next hop is global + link-local; keep the global.
			nexthop, _ = netip.AddrFromSlice(val[1 : 1+min(int(val[0]), 16)])
		case bgp.BGP_ATTR_TYPE_MP_UNREACH_NLRI, bgp.BGP_ATTR_TYPE_AS4_PATH, bgp.BGP_ATTR_TYPE_AS4_AGGREGATOR:
		default:
			a, err := bgp.GetPathAttribute(raw[:hdr+n])
			if err != nil {
				return nil, nexthop, err
			}
			if err := a.DecodeFromBytes(raw[:hdr+n]); err != nil {
				return nil, nexthop, err
			}
			attrs = append(attrs, a)
		}
		raw = raw[hdr+n:]
	}
	return attrs, nexthop, nil
}
