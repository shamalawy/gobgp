// Package api serves the REST/JSON API, the server-sent event stream, the
// Prometheus metrics and the embedded web UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/shamalawy/gobgp/internal/bgp"
	"github.com/shamalawy/gobgp/internal/config"
)

const (
	statusInterval = 2 * time.Second
	maxBody        = 4 << 20 // JSON request bodies
	maxUpload      = 8 << 30
)

type Server struct {
	engine  *bgp.Engine
	log     *slog.Logger
	dataDir string // holds uploaded dumps while they are injected
	ui      fs.FS
	version string
	hub     *hub
	kick    chan struct{} // asks Run for an immediate status broadcast
	cpu     cpuMeter
}

// New wires the server to the engine's event sources. ui is the built web
// application; nil serves the API only.
func New(engine *bgp.Engine, log *slog.Logger, dataDir string, ui fs.FS, version string) *Server {
	s := &Server{
		engine: engine, log: log, dataDir: dataDir, ui: ui, version: version,
		hub: newHub(), kick: make(chan struct{}, 1),
	}
	engine.OnEvent(func(ev bgp.Event) {
		s.hub.publish("log", ev)
		select {
		case s.kick <- struct{}{}:
		default:
		}
	})
	engine.OnInject(func(st bgp.InjectStatus) { s.hub.publish("inject", st) })
	return s
}

// Run broadcasts the status to connected clients until ctx is done: every
// statusInterval, and right after a neighbor changes state.
func (s *Server) Run(ctx context.Context) {
	tick := time.NewTicker(statusInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.kick:
		}
		if s.hub.clients() == 0 {
			continue
		}
		if st, err := s.status(ctx); err == nil {
			s.hub.publish("status", st)
		}
	}
}

func (s *Server) Handler() http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(s.engine, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /api/v1/events", s.events)
	mux.HandleFunc("POST /api/v1/inject/upload", s.upload)

	e := s.engine
	handle(mux, "GET /api/v1/status", func(r *http.Request, _ *struct{}) (any, error) { return s.status(r.Context()) })
	handle(mux, "GET /api/v1/config", func(*http.Request, *struct{}) (any, error) { return e.Config(), nil })
	handle(mux, "PUT /api/v1/global", func(r *http.Request, g *config.Global) (any, error) {
		return nil, e.SetGlobal(r.Context(), *g)
	})
	handle(mux, "PUT /api/v1/policy", func(r *http.Request, p *config.Policy) (any, error) {
		return nil, e.SetPolicy(r.Context(), *p)
	})

	handle(mux, "POST /api/v1/neighbors", func(r *http.Request, n *config.Neighbor) (any, error) {
		return nil, e.AddNeighbor(r.Context(), *n)
	})
	handle(mux, "PUT /api/v1/neighbors/{addr}", func(r *http.Request, n *config.Neighbor) (any, error) {
		addr, err := neighborAddr(r)
		if err != nil {
			return nil, err
		}
		return nil, e.UpdateNeighbor(r.Context(), addr, *n)
	})
	handle(mux, "DELETE /api/v1/neighbors/{addr}", func(r *http.Request, _ *struct{}) (any, error) {
		addr, err := neighborAddr(r)
		if err != nil {
			return nil, err
		}
		return nil, e.DeleteNeighbor(r.Context(), addr)
	})
	type action struct {
		Direction string `json:"direction"`
		Message   string `json:"message"`
	}
	handle(mux, "POST /api/v1/neighbors/{addr}/{action}", func(r *http.Request, a *action) (any, error) {
		addr, err := neighborAddr(r)
		if err != nil {
			return nil, err
		}
		return nil, e.NeighborAction(r.Context(), addr, r.PathValue("action"), a.Direction, a.Message)
	})

	handle(mux, "GET /api/v1/rib", func(r *http.Request, _ *struct{}) (any, error) {
		q := r.URL.Query()
		offset, _ := strconv.Atoi(q.Get("offset"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		return e.QueryRIB(bgp.RibQuery{
			Table: q.Get("table"), Neighbor: q.Get("neighbor"), Family: q.Get("family"),
			Prefix: q.Get("prefix"), Match: q.Get("match"), NextHop: q.Get("nextHop"), AsPath: q.Get("asPath"),
			Community: q.Get("community"), Best: q.Get("best"), Offset: offset, Limit: limit, Refresh: q.Get("refresh") == "true",
		})
	})
	handle(mux, "POST /api/v1/routes", func(_ *http.Request, rt *config.Route) (any, error) { return nil, e.AddRoute(*rt) })
	handle(mux, "DELETE /api/v1/routes", func(r *http.Request, _ *struct{}) (any, error) {
		return nil, e.DeleteRoute(r.URL.Query().Get("prefix"))
	})
	handle(mux, "DELETE /api/v1/routes/injected", func(*http.Request, *struct{}) (any, error) { return nil, e.ClearInjected() })

	handle(mux, "GET /api/v1/inject", func(*http.Request, *struct{}) (any, error) { return e.InjectStatus(), nil })
	handle(mux, "POST /api/v1/inject", func(_ *http.Request, req *bgp.InjectRequest) (any, error) {
		return nil, e.Inject(*req, nil)
	})
	handle(mux, "DELETE /api/v1/inject", func(*http.Request, *struct{}) (any, error) { e.CancelInject(); return nil, nil })

	handle(mux, "GET /api/v1/rpki", func(r *http.Request, _ *struct{}) (any, error) { return e.RpkiServers(r.Context()) })
	handle(mux, "POST /api/v1/rpki", func(r *http.Request, srv *config.RpkiServer) (any, error) {
		return nil, e.AddRpki(r.Context(), *srv)
	})
	handle(mux, "DELETE /api/v1/rpki", func(r *http.Request, _ *struct{}) (any, error) {
		port, _ := strconv.ParseUint(r.URL.Query().Get("port"), 10, 16)
		return nil, e.DeleteRpki(r.Context(), r.URL.Query().Get("address"), uint32(port))
	})
	handle(mux, "POST /api/v1/bmp", func(r *http.Request, srv *config.BmpServer) (any, error) {
		return nil, e.AddBmp(r.Context(), *srv)
	})
	handle(mux, "DELETE /api/v1/bmp", func(r *http.Request, _ *struct{}) (any, error) {
		port, _ := strconv.ParseUint(r.URL.Query().Get("port"), 10, 16)
		return nil, e.DeleteBmp(r.Context(), r.URL.Query().Get("address"), uint32(port))
	})

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "no such endpoint") })
	if s.ui != nil {
		mux.Handle("/", spa(s.ui))
	}
	return mux
}

// handle registers a JSON endpoint. A request with a body is decoded into a
// fresh In; the returned value, if any, is the JSON response.
func handle[In any](mux *http.ServeMux, pattern string, fn func(*http.Request, *In) (any, error)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		in := new(In)
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
			dec.DisallowUnknownFields()
			if err := dec.Decode(in); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
		}
		out, err := fn(r, in)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if out == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// neighborAddr returns the {addr} path value in the canonical form the
// configuration uses.
func neighborAddr(r *http.Request) (string, error) {
	addr, err := netip.ParseAddr(r.PathValue("addr"))
	if err != nil {
		return "", fmt.Errorf("invalid neighbor address %q", r.PathValue("addr"))
	}
	return addr.String(), nil
}

// Runtime describes the daemon process.
type Runtime struct {
	Goroutines int     `json:"goroutines"`
	HeapAlloc  uint64  `json:"heapAlloc"` // bytes of live heap objects
	Sys        uint64  `json:"sys"`       // bytes obtained from the OS
	GCCycles   uint32  `json:"gcCycles"`
	CPUPercent float64 `json:"cpuPercent"` // of one core, since the previous sample
	CPUs       int     `json:"cpus"`
}

type statusMessage struct {
	*bgp.Status
	Version string  `json:"version"`
	Runtime Runtime `json:"runtime"`
}

func (s *Server) status(ctx context.Context) (*statusMessage, error) {
	st, err := s.engine.Status(ctx)
	if err != nil {
		return nil, err
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return &statusMessage{Status: st, Version: s.version, Runtime: Runtime{
		Goroutines: runtime.NumGoroutine(), HeapAlloc: m.HeapAlloc, Sys: m.Sys, GCCycles: m.NumGC,
		CPUPercent: s.cpu.sample(), CPUs: runtime.NumCPU(),
	}}, nil
}

// cpuMeter derives CPU usage from the process's consumed CPU time.
type cpuMeter struct {
	at   time.Time
	used time.Duration
	last float64
}

func (c *cpuMeter) sample() float64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	now, used := time.Now(), time.Duration(ru.Utime.Nano()+ru.Stime.Nano())
	if wall := now.Sub(c.at); !c.at.IsZero() && wall < time.Second/2 {
		return c.last // sampled again within the same interval
	} else if !c.at.IsZero() {
		c.last = 100 * float64(used-c.used) / float64(wall)
	}
	c.at, c.used = now, used
	return c.last
}

// upload stores a dump sent as multipart form data and injects it. The
// "options" part, a JSON InjectRequest, must precede the "file" part.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req bgp.InjectRequest
	for {
		part, err := mr.NextPart()
		if err != nil {
			writeError(w, http.StatusBadRequest, "the upload has no file part")
			return
		}
		if part.FormName() == "options" {
			if err := json.NewDecoder(part).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid options: "+err.Error())
				return
			}
			continue
		}
		if part.FormName() != "file" {
			continue
		}
		// The original name ends the temporary one, so the format is
		// still detected from its extension.
		tmp, err := os.CreateTemp(s.dataDir, "upload-*-"+filepath.Base(part.FileName()))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		remove := func() { os.Remove(tmp.Name()) }
		_, err = io.Copy(tmp, part)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			remove()
			writeError(w, http.StatusBadRequest, "upload failed: "+err.Error())
			return
		}
		req.Source = tmp.Name()
		if err := s.engine.Inject(req, remove); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
}

// spa serves the built web application: real files as they are, and
// index.html for every other path so client-side routes survive a reload.
func spa(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if fi, err := fs.Stat(ui, name); name == "" || err != nil || fi.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, ui, "index.html")
			return
		}
		// Build output other than index.html carries a content hash.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
}
