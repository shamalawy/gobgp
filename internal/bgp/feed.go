package bgp

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/osrg/gobgp/v4/pkg/packet/bgp"

	"github.com/shamalawy/gobgp/internal/config"
	"github.com/shamalawy/gobgp/internal/mrt"
)

const feedBatch = 1024

// feedWriter turns Routes into the raw-attribute form the injector consumes,
// so text feeds share the MRT path: interning, overrides and batching.
type feedWriter struct {
	emit  func([]mrt.Route) error
	batch []mrt.Route
	n     int
}

func (w *feedWriter) add(r config.Route) error {
	w.n++
	pfx, nh, attrs, err := routeAttrs(r)
	if err != nil {
		return fmt.Errorf("entry %d: %w", w.n, err)
	}
	raw := append([]byte{byte(bgp.BGP_ATTR_FLAG_TRANSITIVE), byte(bgp.BGP_ATTR_TYPE_NEXT_HOP), byte(nh.BitLen() / 8)}, nh.AsSlice()...)
	for _, a := range attrs {
		b, err := a.Serialize()
		if err != nil {
			return fmt.Errorf("entry %d: %w", w.n, err)
		}
		raw = append(raw, b...)
	}
	w.batch = append(w.batch, mrt.Route{Prefix: pfx, Attrs: raw})
	if len(w.batch) == feedBatch {
		return w.flush()
	}
	return nil
}

func (w *feedWriter) flush() error {
	if len(w.batch) == 0 {
		return nil
	}
	batch := w.batch
	w.batch = make([]mrt.Route, 0, feedBatch)
	return w.emit(batch)
}

// readCSV reads rows of
//
//	prefix,next_hop,as_path,origin,med,local_pref,communities
//
// Only the prefix is required and a header row is skipped. Communities are
// space separated and may mix standard (65000:1), large (65000:1:2) and
// extended (rt:65000:1) forms.
func readCSV(r io.Reader, emit func([]mrt.Route) error) error {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	cr.TrimLeadingSpace = true
	w := &feedWriter{emit: emit}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return w.flush()
		}
		if err != nil {
			return err
		}
		col := func(i int) string {
			if i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		if w.n == 0 && strings.EqualFold(col(0), "prefix") {
			continue
		}
		route := config.Route{Prefix: col(0), NextHop: col(1), AsPath: col(2), Origin: col(3)}
		for i, dst := range []**uint32{&route.Med, &route.LocalPref} {
			if s := col(4 + i); s != "" {
				v, err := strconv.ParseUint(s, 10, 32)
				if err != nil {
					return fmt.Errorf("entry %d: invalid number %q", w.n+1, s)
				}
				u := uint32(v)
				*dst = &u
			}
		}
		for _, c := range strings.Fields(col(6)) {
			switch {
			case strings.Count(c, ":") == 2 && c[0] >= '0' && c[0] <= '9':
				route.LargeCommunities = append(route.LargeCommunities, c)
			case strings.Count(c, ":") >= 2:
				route.ExtCommunities = append(route.ExtCommunities, c)
			default:
				route.Communities = append(route.Communities, c)
			}
		}
		if err := w.add(route); err != nil {
			return err
		}
	}
}

// readJSON reads Route objects, either as one JSON array or as a stream of
// objects (NDJSON).
func readJSON(r io.Reader, emit func([]mrt.Route) error) error {
	br := bufio.NewReader(r)
	dec := json.NewDecoder(br)
	for {
		c, err := br.Peek(1)
		if err != nil || !strings.ContainsRune(" \t\r\n", rune(c[0])) {
			if err == nil && c[0] == '[' {
				dec.Token() // enter the array; More then stops at its end
			}
			break
		}
		br.Discard(1)
	}
	w := &feedWriter{emit: emit}
	for dec.More() {
		var route config.Route
		if err := dec.Decode(&route); err != nil {
			return fmt.Errorf("entry %d: %w", w.n+1, err)
		}
		if err := w.add(route); err != nil {
			return err
		}
	}
	return w.flush()
}
