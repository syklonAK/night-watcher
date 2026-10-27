package manage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildArchive returns a gzip tar containing name -> content.
func buildArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// restoreInTemp extracts an archive with app.ConfigDir pointed at a temporary
// directory and returns that directory for inspection.
func restoreInTemp(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	old := appConfigDir
	appConfigDir = dir
	t.Cleanup(func() { appConfigDir = old })
	if _, err := Restore(bytes.NewReader(buildArchive(t, files))); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	return dir
}

const localEdge = `{"enabled":true,"shell":{"enabled":false,"uid":1000,"gid":1000,"directory":"/","commands":{"diag":["/usr/bin/free"]},"timeout_seconds":60,"idle_seconds":30,"max_sessions":1}}`

func TestRestoreKeepsLocalEdgeShell(t *testing.T) {
	// A malicious or foreign archive carries a privileged shell section.
	evil := `{"enabled":true,"shell":{"enabled":true,"uid":0,"gid":0,"directory":"/","commands":{"root":["/bin/sh"]},"timeout_seconds":3600,"idle_seconds":3600,"max_sessions":8}}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "edge.json"), []byte(localEdge), 0600); err != nil {
		t.Fatal(err)
	}
	old := appConfigDir
	appConfigDir = dir
	t.Cleanup(func() { appConfigDir = old })

	if _, err := Restore(bytes.NewReader(buildArchive(t, map[string]string{"edge.json": evil}))); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "edge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"uid":0`) {
		t.Fatalf("archived shell section replaced the local one: %s", got)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("merged edge.json is not valid JSON: %v", err)
	}
	var shell map[string]any
	json.Unmarshal(m["shell"], &shell)
	cmds, _ := shell["commands"].(map[string]any)
	if _, ok := cmds["diag"]; !ok {
		t.Fatalf("local shell section not preserved: %s", m["shell"])
	}
	// Everything outside shell comes from the archive.
	if !strings.Contains(string(got), `"enabled":true`) {
		t.Fatalf("non-shell keys lost: %s", got)
	}
}

func TestRestoreWritesArchivedShellWhenNoLocalFile(t *testing.T) {
	evil := `{"enabled":true,"shell":{"enabled":true,"uid":0,"gid":0,"directory":"/","commands":{"root":["/bin/sh"]},"timeout_seconds":3600,"idle_seconds":3600,"max_sessions":8}}`
	dir := restoreInTemp(t, map[string]string{"edge.json": evil})
	got, err := os.ReadFile(filepath.Join(dir, "edge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != evil {
		t.Fatalf("fresh host should take the archived edge.json verbatim, got %s", got)
	}
}

func TestRestoreRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	old := appConfigDir
	appConfigDir = dir
	t.Cleanup(func() { appConfigDir = old })
	if _, err := Restore(bytes.NewReader(buildArchive(t, map[string]string{"../evil.txt": "x"}))); err == nil {
		t.Fatal("Restore accepted a path traversal entry")
	}
}
