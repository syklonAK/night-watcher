package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
)

type State struct {
	Started    time.Time    `json:"started"`
	Enabled    bool         `json:"enabled"`
	Generation uint64       `json:"generation"`
	Network    NetworkState `json:"network"`
	Metrics    Metrics      `json:"metrics"`
	Nodes      []NodeState  `json:"nodes"`
	LastError  string       `json:"last_error,omitempty"`
	Controller bool         `json:"controller"`
}
type Manager struct {
	mu           sync.RWMutex
	applyMu      sync.Mutex
	config       Config
	router       *Router
	balancer     *Balancer
	traffic      Counters
	sampler      metricSampler
	network      NetworkState
	metrics      Metrics
	started      time.Time
	generation   uint64
	lastError    string
	dir          string
	owner        bool
	ctx          context.Context
	cancel       context.CancelFunc
	healthCancel context.CancelFunc
	healthDone   chan struct{}
	done         chan struct{}
	inlets       map[string]*inlet
	firewall     Firewall
	Shell        *Shell
}

// Start starts one edge instance per process. The web panel owns ingress and
// firewall; tunnel/proxy processes consume the same policy file independently.
func Start(ctx context.Context, dir string, controller bool) (*Manager, error) {
	c, e := LoadConfig(configPath(dir))
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{dir: dir, owner: controller, ctx: ctx, cancel: cancel, started: time.Now(), inlets: map[string]*inlet{}, Shell: NewShell(), done: make(chan struct{})}
	if e = m.apply(c, false); e != nil {
		cancel()
		return nil, e
	}
	active.Store(m)
	go m.run()
	return m, nil
}
func (m *Manager) Context() context.Context { return m.ctx }
func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, _ := json.Marshal(m.config)
	var c Config
	_ = json.Unmarshal(b, &c)
	return c
}
func (m *Manager) Snapshot() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := State{Started: m.started, Enabled: m.config.Enabled, Generation: m.generation, Network: m.network, Metrics: m.metrics, Nodes: m.balancer.Snapshot(), LastError: m.lastError, Controller: m.owner}
	b, _ := json.Marshal(s)
	var copy State
	_ = json.Unmarshal(b, &copy)
	return copy
}
func (m *Manager) Save(c Config) error { return m.apply(c, true) }
func (m *Manager) Reload() error {
	c, e := LoadConfig(configPath(m.dir))
	if e != nil {
		return e
	}
	return m.apply(c, false)
}
func (m *Manager) apply(c Config, persist bool) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if e := c.Validate(); e != nil {
		return e
	}
	if m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	// Detach caller-owned slices/maps before publishing immutable config.
	b, _ := json.Marshal(c)
	_ = json.Unmarshal(b, &c)
	m.mu.RLock()
	old := m.config
	m.mu.RUnlock()
	if reflect.DeepEqual(c, old) && m.generation > 0 {
		m.mu.Lock()
		m.lastError = ""
		m.mu.Unlock()
		if persist {
			return SaveConfig(configPath(m.dir), c)
		}
		return nil
	}
	prepared := map[string]*inlet{}
	created := []*inlet{}
	cleanup := func() {
		for _, i := range created {
			i.close()
		}
	}
	if m.owner && c.Enabled {
		for _, l := range c.Listeners {
			key := l.Address + "/" + l.Mode
			if i, ok := m.inlets[key]; ok {
				prepared[key] = i
			} else {
				i, e := newInlet(l)
				if e != nil {
					cleanup()
					return e
				}
				prepared[key] = i
				created = append(created, i)
			}
		}
	}
	if m.owner {
		if e := m.firewall.Apply(m.ctx, c.Firewall); e != nil {
			cleanup()
			return e
		}
	}
	if persist {
		if e := SaveConfig(configPath(m.dir), c); e != nil {
			cleanup()
			if m.owner {
				// Roll the firewall back to the previous policy. A rollback
				// failure leaves the new rules live while the config file
				// still says otherwise — that mismatch must be visible, not
				// silently dropped.
				if rb := m.firewall.Apply(m.ctx, old.Firewall); rb != nil {
					return fmt.Errorf("saving config failed: %w (rollback of firewall rules also failed: %v — manual nft review needed)", e, rb)
				}
			}
			return e
		}
	}
	if m.healthCancel != nil {
		m.healthCancel()
		<-m.healthDone
	}
	bal := m.balancer
	if bal == nil || !reflect.DeepEqual(c.Upstreams, old.Upstreams) {
		bal = NewBalancer(c.Upstreams)
	}
	m.mu.Lock()
	m.config = c
	m.router = NewRouter(c)
	m.balancer = bal
	m.generation++
	m.lastError = ""
	m.mu.Unlock()
	for _, l := range c.Listeners {
		if i := prepared[l.Address+"/"+l.Mode]; i != nil {
			lcopy := l
			i.cfg.Store(&lcopy)
		}
	}
	for k, i := range m.inlets {
		if prepared[k] != i {
			i.close()
		}
	}
	m.inlets = prepared
	healthCtx, hcancel := context.WithCancel(m.ctx)
	m.healthCancel = hcancel
	m.healthDone = make(chan struct{})
	done := m.healthDone
	go func() {
		defer close(done)
		if c.Enabled {
			bal.Run(healthCtx)
		}
	}()
	for _, i := range created {
		go i.run(m.ctx, m)
	}
	return nil
}
func (m *Manager) run() {
	defer close(m.done)
	detect := func() { c := m.Config(); n := Detect(m.ctx, c.Detect); m.mu.Lock(); m.network = n; m.mu.Unlock() }
	detectDone := make(chan struct{})
	go func() {
		defer close(detectDone)
		detect()
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-t.C:
				detect()
			}
		}
	}()
	hup, stopHUP := reloadSignal()
	defer stopHUP()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			m.applyMu.Lock()
			if m.healthCancel != nil {
				m.healthCancel()
				<-m.healthDone
			}
			for _, i := range m.inlets {
				i.close()
			}
			m.applyMu.Unlock()
			<-detectDone
			return
		case <-hup:
			if e := m.Reload(); e != nil {
				m.mu.Lock()
				m.lastError = e.Error()
				m.mu.Unlock()
			}
		case <-ticker.C:
			c, e := LoadConfig(configPath(m.dir))
			if e == nil && !reflect.DeepEqual(c, m.Config()) {
				e = m.apply(c, false)
			}
			if e != nil {
				m.mu.Lock()
				m.lastError = e.Error()
				m.mu.Unlock()
			} else {
				m.mu.Lock()
				m.lastError = ""
				m.mu.Unlock()
			}
			ctx, cancel := context.WithTimeout(m.ctx, time.Second)
			metrics := m.sampler.Sample(ctx, &m.traffic, m.dir)
			cancel()
			m.mu.Lock()
			m.metrics = metrics
			m.mu.Unlock()
		}
	}
}
func (m *Manager) Close() { m.cancel(); <-m.done; active.CompareAndSwap(m, nil) }
func (m *Manager) Restore(r []byte) error {
	if len(r) > MaxConfigSize {
		return errors.New("backup too large")
	}
	c, e := DecodeConfig(bytes.NewReader(r))
	if e != nil {
		return e
	}
	return m.Save(c)
}
func (m *Manager) Backup() ([]byte, error) { return json.MarshalIndent(m.Config(), "", "  ") }

// Missing config means a disabled policy, never silently replace malformed data.
func ConfigExists(dir string) bool { _, e := os.Stat(configPath(dir)); return e == nil }
