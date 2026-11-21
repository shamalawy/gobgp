package bgp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"

	"github.com/shamalawy/gobgp/internal/mrt"
)

// InjectRequest describes a bulk injection.
type InjectRequest struct {
	Source string `json:"source"` // http(s) URL or local file; .gz and .bz2 are decompressed
	Format string `json:"format"` // mrt, csv or json; empty = from the file extension
	Rate   int    `json:"rate"`   // routes per second, 0 = unlimited
	Limit  int    `json:"limit"`  // stop after this many routes, 0 = all
	Family string `json:"family"` // "ipv4" or "ipv6" to inject only one family

	// MRT only: which collector peer's view to take. Zero values take the
	// first entry of every prefix.
	PeerAS uint32 `json:"peerAs"`
	PeerIP string `json:"peerIp"`

	// Attribute overrides.
	NextHopSelf bool     `json:"nextHopSelf"` // advertise with the session's local address
	NextHop     string   `json:"nextHop"`     // rewrite the next hop of routes of this address's family
	Prepend     int      `json:"prepend"`     // times to prepend the local ASN
	LocalPref   *uint32  `json:"localPref"`
	Med         *uint32  `json:"med"`
	Communities []string `json:"communities"` // standard communities to add
}

// InjectStatus is the progress of the current or last injection.
type InjectStatus struct {
	State      string  `json:"state"` // idle, running, done, failed, cancelled
	Source     string  `json:"source"`
	Started    int64   `json:"started"`  // unix milliseconds
	Finished   int64   `json:"finished"` // unix milliseconds, 0 while running
	BytesRead  int64   `json:"bytesRead"`
	BytesTotal int64   `json:"bytesTotal"` // 0 when unknown
	Routes     int64   `json:"routes"`     // accepted by the RIB
	Failed     int64   `json:"failed"`
	AttrSets   int64   `json:"attrSets"` // distinct attribute sets, shared by all routes carrying them
	Rate       float64 `json:"rate"`     // routes per second over the last interval
	Error      string  `json:"error,omitempty"`
}

type injector struct {
	mu       sync.Mutex
	status   InjectStatus
	cancel   context.CancelFunc
	onUpdate func(InjectStatus)

	bytes, routes, failed, attrSets atomic.Int64
}

// OnInject registers the receiver of progress updates, sent twice a second
// while an injection runs and once when it ends.
// Call it before the first Inject.
func (e *Engine) OnInject(fn func(InjectStatus)) { e.inj.onUpdate = fn }

func (e *Engine) InjectStatus() InjectStatus {
	j := &e.inj
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.status
	if st.State == "" {
		st.State = "idle"
	}
	st.BytesRead, st.Routes, st.Failed, st.AttrSets = j.bytes.Load(), j.routes.Load(), j.failed.Load(), j.attrSets.Load()
	return st
}

// CancelInject stops the running injection; routes already injected stay.
func (e *Engine) CancelInject() {
	j := &e.inj
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.cancel()
	}
}

// Inject starts a bulk injection in the background. Only one runs at a time.
// cleanup, if not nil, is called when it ends.
func (e *Engine) Inject(req InjectRequest, cleanup func()) error {
	if cleanup == nil {
		cleanup = func() {}
	}
	plan, err := newInjectPlan(req, e.store.Get().Global.ASN)
	if err != nil {
		cleanup()
		return err
	}
	j := &e.inj
	j.mu.Lock()
	if j.status.State == "running" {
		j.mu.Unlock()
		cleanup()
		return errors.New("an injection is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.status = InjectStatus{State: "running", Source: req.Source, Started: time.Now().UnixMilli()}
	for _, c := range []*atomic.Int64{&j.bytes, &j.routes, &j.failed, &j.attrSets} {
		c.Store(0)
	}
	j.mu.Unlock()

	go func() {
		defer cleanup()
		defer cancel()
		done := make(chan struct{})
		go e.reportProgress(done)
		err := e.runInject(ctx, plan)
		close(done)

		j.mu.Lock()
		j.status.State, j.status.Finished, j.status.Rate = "done", time.Now().UnixMilli(), 0
		switch {
		case errors.Is(err, context.Canceled):
			j.status.State = "cancelled"
		case err != nil:
			j.status.State, j.status.Error = "failed", err.Error()
		}
		j.mu.Unlock()
		if j.onUpdate != nil {
			j.onUpdate(e.InjectStatus())
		}
	}()
	return nil
}

func (e *Engine) reportProgress(done <-chan struct{}) {
	j := &e.inj
	const interval = 500 * time.Millisecond
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var last int64
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			n := j.routes.Load()
			j.mu.Lock()
			j.status.Rate = float64(n-last) / interval.Seconds()
			j.mu.Unlock()
			last = n
			if j.onUpdate != nil {
				j.onUpdate(e.InjectStatus())
			}
		}
	}
}

// injectPlan is a validated InjectRequest.
type injectPlan struct {
	InjectRequest
	filter      mrt.Filter
	nextHop     netip.Addr
	localASN    uint32
	communities []uint32
}

func newInjectPlan(req InjectRequest, localASN uint32) (*injectPlan, error) {
	p := &injectPlan{InjectRequest: req, localASN: localASN, filter: mrt.Filter{PeerAS: req.PeerAS}}
	var err error
	if req.Source == "" {
		return nil, errors.New("source is required")
	}
	if p.Format == "" {
		name := strings.ToLower(req.Source)
		if i := strings.IndexAny(name, "?#"); i >= 0 && strings.Contains(name, "://") {
			name = name[:i]
		}
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".bz2")
		switch {
		case strings.HasSuffix(name, ".csv"):
			p.Format = "csv"
		case strings.HasSuffix(name, ".json"), strings.HasSuffix(name, ".ndjson"), strings.HasSuffix(name, ".jsonl"):
			p.Format = "json"
		default:
			p.Format = "mrt"
		}
	}
	if p.Format != "mrt" && p.Format != "csv" && p.Format != "json" {
		return nil, fmt.Errorf("unknown format %q", p.Format)
	}
	if req.Family != "" && req.Family != "ipv4" && req.Family != "ipv6" {
		return nil, fmt.Errorf("unknown family %q", req.Family)
	}
	if req.PeerIP != "" {
		if p.filter.PeerIP, err = netip.ParseAddr(req.PeerIP); err != nil {
			return nil, fmt.Errorf("invalid peer address %q", req.PeerIP)
		}
	}
	if req.NextHop != "" {
		if p.nextHop, err = netip.ParseAddr(req.NextHop); err != nil {
			return nil, fmt.Errorf("invalid next hop %q", req.NextHop)
		}
	}
	if req.Prepend < 0 || req.Prepend > 32 || req.Rate < 0 || req.Limit < 0 {
		return nil, errors.New("prepend must be 0-32; rate and limit must not be negative")
	}
	for _, c := range req.Communities {
		v, err := parseCommunity(c)
		if err != nil {
			return nil, err
		}
		p.communities = append(p.communities, v)
	}
	return p, nil
}

// countingReader tracks compressed bytes consumed, which drives the progress bar.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func openSource(ctx context.Context, source string) (io.ReadCloser, int64, error) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, 0, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, 0, fmt.Errorf("GET %s: %s", source, resp.Status)
		}
		return resp.Body, max(resp.ContentLength, 0), nil
	}
	f, err := os.Open(source)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		f.Close()
		return nil, 0, fmt.Errorf("%s is not a readable file", source)
	}
	return f, fi.Size(), nil
}

var errLimit = errors.New("route limit reached")

// runInject is the pipeline: one reader decodes the source into batches, a
// bounded channel carries them to a worker pool, and each worker resolves the
// attributes through the shared intern table and hands the batch to GoBGP.
// The channel bound is the backpressure: when the RIB is the bottleneck the
// reader blocks instead of buffering the table in memory.
func (e *Engine) runInject(ctx context.Context, p *injectPlan) error {
	j := &e.inj
	src, size, err := openSource(ctx, p.Source)
	if err != nil {
		return err
	}
	defer src.Close()
	j.mu.Lock()
	j.status.BytesTotal = size
	j.mu.Unlock()
	stream, err := mrt.Decompress(countingReader{src, &j.bytes})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	workers := min(runtime.GOMAXPROCS(0), 4)
	batches := make(chan []mrt.Route, workers)
	interner := &injectInterner{plan: p, sets: [2]map[string]*injectAttrs{{}, {}}, count: &j.attrSets}
	srv := e.server()
	start := time.Now()

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			paths := make([]*apiutil.Path, 0, feedBatch)
			for batch := range batches {
				paths = paths[:0]
				for _, r := range batch {
					set := interner.get(r)
					if set.err != nil {
						j.failed.Add(1)
						continue
					}
					paths = append(paths, newPath(r.Prefix, set.nexthop, set.attrs))
				}
				if len(paths) == 0 {
					continue
				}
				resp, err := srv.AddPath(apiutil.AddPathRequest{Paths: paths})
				if len(resp) == 0 && err != nil { // the request as a whole was refused
					cancel(err)
					return
				}
				bad := int64(0)
				for _, r := range resp {
					if r.Error != nil {
						bad++
					}
				}
				j.failed.Add(bad)
				total := j.routes.Add(int64(len(paths)) - bad)
				if p.Rate > 0 {
					// Pace against the whole run, so short stalls are caught up.
					due := start.Add(time.Duration(float64(total) / float64(p.Rate) * float64(time.Second)))
					select {
					case <-time.After(time.Until(due)):
					case <-ctx.Done():
					}
				}
			}
		})
	}

	emitted := 0
	emit := func(batch []mrt.Route) error {
		if p.Family != "" {
			keep := batch[:0]
			for _, r := range batch {
				if r.Prefix.Addr().Is4() == (p.Family == "ipv4") {
					keep = append(keep, r)
				}
			}
			batch = keep
		}
		var stop error
		if p.Limit > 0 && emitted+len(batch) >= p.Limit {
			batch, stop = batch[:p.Limit-emitted], errLimit
		}
		emitted += len(batch)
		select {
		case batches <- batch:
			return stop
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	switch p.Format {
	case "csv":
		err = readCSV(stream, emit)
	case "json":
		err = readJSON(stream, emit)
	default:
		err = mrt.Read(stream, p.filter, emit)
	}
	close(batches)
	wg.Wait()
	if errors.Is(err, errLimit) {
		err = nil
	}
	if err == nil {
		err = context.Cause(ctx)
	}
	if err == nil && j.routes.Load() == 0 {
		err = errors.New("the source contained no routes that could be injected")
	}
	return err
}

// injectAttrs is one distinct attribute set after overrides. Every path
// injected with it references the same attribute objects, so a full table
// costs memory per distinct set (about 15% of the routes), not per route.
type injectAttrs struct {
	attrs   []bgp.PathAttributeInterface
	nexthop bgp.PathAttributeInterface
	err     error
}

type injectInterner struct {
	plan  *injectPlan
	mu    sync.RWMutex
	sets  [2]map[string]*injectAttrs // by raw attribute bytes; [0] IPv4, [1] IPv6
	count *atomic.Int64
}

func (in *injectInterner) get(r mrt.Route) *injectAttrs {
	fam := 0
	if r.Prefix.Addr().Is6() {
		fam = 1
	}
	in.mu.RLock()
	set := in.sets[fam][string(r.Attrs)]
	in.mu.RUnlock()
	if set != nil {
		return set
	}
	set = in.build(r)
	in.mu.Lock()
	defer in.mu.Unlock()
	if other := in.sets[fam][string(r.Attrs)]; other != nil {
		return other
	}
	in.sets[fam][string(r.Attrs)] = set
	in.count.Add(1)
	return set
}

func (in *injectInterner) build(r mrt.Route) *injectAttrs {
	p := in.plan
	attrs, nh, err := mrt.ParseAttrs(r.Attrs)
	if err != nil {
		return &injectAttrs{err: err}
	}
	switch {
	case p.NextHopSelf, !nh.IsValid():
		nh = unspecified(r.Prefix)
	case p.nextHop.IsValid() && p.nextHop.Is4() == r.Prefix.Addr().Is4():
		nh = p.nextHop
	}
	// set replaces the attribute of the same type, or adds it.
	set := func(a bgp.PathAttributeInterface) {
		for i, old := range attrs {
			if old.GetType() == a.GetType() {
				attrs[i] = a
				return
			}
		}
		attrs = append(attrs, a)
	}
	find := func(t bgp.BGPAttrType) bgp.PathAttributeInterface {
		for _, a := range attrs {
			if a.GetType() == t {
				return a
			}
		}
		return nil
	}
	if find(bgp.BGP_ATTR_TYPE_ORIGIN) == nil {
		set(bgp.NewPathAttributeOrigin(bgp.BGP_ORIGIN_ATTR_TYPE_INCOMPLETE))
	}
	if p.Prepend > 0 {
		asns := make([]uint32, p.Prepend)
		for i := range asns {
			asns[i] = p.localASN
		}
		segs := []bgp.AsPathParamInterface{bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, asns)}
		if old, ok := find(bgp.BGP_ATTR_TYPE_AS_PATH).(*bgp.PathAttributeAsPath); ok {
			segs = append(segs, old.Value...)
		}
		set(bgp.NewPathAttributeAsPath(segs))
	}
	if p.LocalPref != nil {
		set(bgp.NewPathAttributeLocalPref(*p.LocalPref))
	}
	if p.Med != nil {
		set(bgp.NewPathAttributeMultiExitDisc(*p.Med))
	}
	if len(p.communities) > 0 {
		vals := p.communities
		if old, ok := find(bgp.BGP_ATTR_TYPE_COMMUNITIES).(*bgp.PathAttributeCommunities); ok {
			vals = append(append([]uint32{}, old.Value...), vals...)
		}
		set(bgp.NewPathAttributeCommunities(vals))
	}
	return &injectAttrs{attrs: attrs, nexthop: nexthopAttr(nh)}
}
