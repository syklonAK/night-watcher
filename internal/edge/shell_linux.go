//go:build linux

package edge

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

func shellSupported() bool { return true }
func servePTY(ctx context.Context, ws *websocket.Conn, c ShellConfig, argv []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = c.Directory
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "HOME=" + c.Directory, "LANG=C.UTF-8"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: c.UID, Gid: c.GID, NoSetGroups: false}, Pdeathsig: syscall.SIGKILL}
	f, e := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if e != nil {
		return e
	}
	defer f.Close()
	var once sync.Once
	stop := func() {
		once.Do(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = f.Close(); _ = ws.Close() })
	}
	defer stop()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); cancel() }()
	go func() { <-ctx.Done(); stop() }()
	output := make(chan struct{})
	go func() {
		defer close(output)
		buf := make([]byte, 8192)
		for {
			n, e := f.Read(buf)
			if n > 0 {
				ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if ws.WriteMessage(websocket.BinaryMessage, buf[:n]) != nil {
					cancel()
					return
				}
			}
			if e != nil {
				cancel()
				return
			}
		}
	}()
	ws.SetReadLimit(8192)
	ws.SetReadDeadline(time.Now().Add(seconds(c.IdleSeconds)))
	for {
		kind, b, e := ws.ReadMessage()
		if e != nil {
			break
		}
		ws.SetReadDeadline(time.Now().Add(seconds(c.IdleSeconds)))
		if kind == websocket.BinaryMessage {
			if _, e = f.Write(b); e != nil {
				break
			}
		} else if kind == websocket.TextMessage {
			var size struct {
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(b, &size) != nil || size.Cols < 1 || size.Cols > 500 || size.Rows < 1 || size.Rows > 300 {
				break
			}
			if pty.Setsize(f, &pty.Winsize{Rows: size.Rows, Cols: size.Cols}) != nil {
				break
			}
		}
	}
	stop()
	<-output
	e = <-done
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}
	return e
}
