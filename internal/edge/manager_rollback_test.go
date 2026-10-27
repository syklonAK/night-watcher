package edge

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
)

// failRunner is a CommandRunner that counts invocations (race-safe: the
// firewall calls it while the manager loop may be reading metrics) and fails
// from the Nth call onward, so a test can let "apply new rules" succeed while
// "roll back to the old rules" fails.
type failRunner struct {
	mu    sync.Mutex
	calls int
	fail  int // 1-based; calls >= fail fail; 0 = never
}

func (f *failRunner) Run(ctx context.Context, name string, args []string, in []byte) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	c := f.calls
	f.mu.Unlock()
	if f.fail > 0 && c >= f.fail {
		return nil, context.DeadlineExceeded
	}
	return nil, nil
}

func (f *failRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ CommandRunner = (&failRunner{}).Run

// The rollback after a failed persist must not be swallowed: if rolling the
// firewall back also fails, the caller has to learn that the live rules and
// the config file now disagree.
func TestManagerSaveReportsFailedRollback(t *testing.T) {
	dir := t.TempDir()
	c := DefaultConfig()
	c.Detect = DetectConfig{}
	if e := SaveConfig(configPath(dir), c); e != nil {
		t.Fatal(e)
	}
	m, e := Start(context.Background(), dir, true)
	if e != nil {
		t.Skipf("owner manager needs platform support: %v", e)
	}
	defer m.Close()

	// Step 1: enable the firewall with a healthy runner so a later rollback
	// has real rules to go back to.
	m.firewall.mu.Lock()
	m.firewall.Run = (&failRunner{}).Run
	m.firewall.mu.Unlock()
	enabled := m.Config()
	enabled.Firewall.Enabled = true
	if e := m.Save(enabled); e != nil {
		t.Fatalf("enabling firewall: %v", e)
	}

	// Step 2: make the config file unwritable so the next persist fails…
	if e := os.Chmod(configPath(dir), 0400); e != nil {
		t.Skip("cannot make config read-only:", e)
	}
	t.Cleanup(func() { os.Chmod(configPath(dir), 0600) })

	// …and fail the runner from the rollback call onward: calls 1–3 apply the
	// new rules, call 4+ is the rollback.
	m.firewall.mu.Lock()
	runner := &failRunner{fail: 4}
	m.firewall.Run = runner.Run
	m.firewall.mu.Unlock()

	next := m.Config()
	next.Firewall.PreventDNSLeaks = true
	e = m.Save(next)
	if e == nil {
		t.Fatal("save with unwritable config succeeded")
	}
	if !strings.Contains(e.Error(), "rollback") {
		t.Fatalf("rollback failure not reported: %v", e)
	}
	if runner.count() < 4 {
		t.Fatalf("rollback was not attempted: %d runner calls", runner.count())
	}
}
