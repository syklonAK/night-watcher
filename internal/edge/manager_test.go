package edge

import (
	"encoding/json"
	"os"
	"testing"
)

func TestManagerReloadBackupAndFailedConfig(t *testing.T) {
	c := DefaultConfig()
	m := testManager(t, c, false)
	before := m.Snapshot().Generation
	if e := os.WriteFile(configPath(m.dir), []byte(`{"version":1,"broken":`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.Reload(); e == nil {
		t.Fatal("invalid reload accepted")
	}
	if m.Snapshot().Generation != before {
		t.Fatal("failed config replaced runtime")
	}
	next := m.Config()
	next.Enabled = true
	next.Default = "block"
	if e := m.Save(next); e != nil {
		t.Fatal(e)
	}
	got, e := LoadConfig(configPath(m.dir))
	if e != nil || got.Default != "block" {
		t.Fatal("save missing", e)
	}
	backup, e := m.Backup()
	if e != nil {
		t.Fatal(e)
	}
	var restored Config
	if e = json.Unmarshal(backup, &restored); e != nil || restored.Default != "block" {
		t.Fatal("backup invalid")
	}
	copy := m.Config()
	copy.Default = "direct"
	if m.Config().Default != "block" {
		t.Fatal("caller mutated runtime")
	}
	if e = m.Restore([]byte(`{"version":99}`)); e == nil {
		t.Fatal("invalid restore accepted")
	}
}
