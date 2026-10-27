package edge

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type inlet struct {
	mu    sync.Mutex
	ln    net.Listener
	cfg   atomic.Pointer[ListenerConfig]
	http  *http.Server
	slots chan struct{}
}

func newInlet(c ListenerConfig) (*inlet, error) {
	if c.Mode == "https" {
		if _, e := tls.LoadX509KeyPair(c.CertFile, c.KeyFile); e != nil {
			return nil, e
		}
	}
	ln, e := net.Listen("tcp", c.Address)
	if e != nil {
		return nil, e
	}
	i := &inlet{ln: ln, slots: make(chan struct{}, 1024)}
	i.cfg.Store(&c)
	return i, nil
}
func (i *inlet) close() {
	_ = i.ln.Close()
	i.mu.Lock()
	srv := i.http
	i.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
func inboundGroup(c ListenerConfig, host, path string) string {
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	for _, r := range c.Routes {
		if (r.Host == "*" || domainMatch(r.Host, host)) && (r.Path == "" || strings.HasPrefix(path, r.Path)) {
			return r.Group
		}
	}
	return c.DefaultGroup
}
func (i *inlet) run(ctx context.Context, m *Manager) {
	c := i.cfg.Load()
	if c.Mode == "http" || c.Mode == "https" {
		i.mu.Lock()
		i.http = &http.Server{ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 65536, BaseContext: func(net.Listener) context.Context { return ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case i.slots <- struct{}{}:
				defer func() { <-i.slots }()
			default:
				http.Error(w, "edge capacity reached", 503)
				return
			}
			cfg := i.cfg.Load()
			group := inboundGroup(*cfg, r.Host, r.URL.Path)
			target := &url.URL{Scheme: "http", Host: "edge-backend"}
			p := httputil.NewSingleHostReverseProxy(target)
			tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second, ForceAttemptHTTP2: false, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return m.DialGroup(ctx, network, addr, group, true)
			}}
			defer tr.CloseIdleConnections()
			p.Transport = tr
			p.BufferPool = relayPool{}
			p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { http.Error(w, "no healthy edge backend", 502) }
			p.ServeHTTP(w, r)
		})}
		i.mu.Unlock()
		ln := i.ln
		if c.Mode == "https" {
			ln = tls.NewListener(ln, &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				c := i.cfg.Load()
				cert, e := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
				return &cert, e
			}})
		}
		_ = i.http.Serve(ln)
		return
	}
	for {
		conn, e := i.ln.Accept()
		if e != nil {
			return
		}
		select {
		case i.slots <- struct{}{}:
			go func() {
				defer func() { <-i.slots }()
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { conn.Close() })
				defer stop()
				cfg := i.cfg.Load()
				group := cfg.DefaultGroup
				var pre []byte
				if cfg.Mode == "tls-passthrough" {
					host, data, e := sniffSNI(conn)
					if e != nil {
						return
					}
					pre = data
					group = inboundGroup(*cfg, host, "")
				}
				dst, e := m.DialGroup(ctx, "tcp", "", group, true)
				if e != nil {
					return
				}
				defer dst.Close()
				stopDst := context.AfterFunc(ctx, func() { dst.Close() })
				defer stopDst()
				if len(pre) > 0 {
					if _, e = dst.Write(pre); e != nil {
						return
					}
				}
				relay(conn, dst)
			}()
		default:
			conn.Close()
			m.traffic.RejectedFlows.Add(1)
		}
	}
}

var copyBuffers = sync.Pool{New: func() any { return make([]byte, 32*1024) }}

type relayPool struct{}

func (relayPool) Get() []byte { return copyBuffers.Get().([]byte) }
func (relayPool) Put(b []byte) {
	if cap(b) == 32*1024 {
		copyBuffers.Put(b[:32*1024])
	}
}
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	copyOne := func(dst, src net.Conn) {
		defer wg.Done()
		buf := copyBuffers.Get().([]byte)
		defer copyBuffers.Put(buf)
		_, _ = io.CopyBuffer(dst, src, buf)
		if h, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = h.CloseWrite()
		}
	}
	wg.Add(2)
	go copyOne(a, b)
	go copyOne(b, a)
	wg.Wait()
}

type helloConn struct {
	net.Conn
	buf bytes.Buffer
}

func (c *helloConn) Read(b []byte) (int, error) {
	remaining := 65536 - c.buf.Len()
	if remaining <= 0 {
		return 0, errors.New("TLS hello exceeds 64 KiB")
	}
	if len(b) > remaining {
		b = b[:remaining]
	}
	n, e := c.Conn.Read(b)
	c.buf.Write(b[:n])
	return n, e
}
func (c *helloConn) Write(b []byte) (int, error) { return len(b), nil } // never send the inspection handshake's alert
func sniffSNI(c net.Conn) (string, []byte, error) {
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	tap := &helloConn{Conn: c}
	found := errors.New("hello captured")
	host := ""
	s := tls.Server(tap, &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) { host = h.ServerName; return nil, found }})
	e := s.Handshake()
	if !errors.Is(e, found) {
		return "", nil, e
	}
	return host, append([]byte(nil), tap.buf.Bytes()...), nil
}
