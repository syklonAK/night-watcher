//go:build !linux

package edge

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
)

func shellSupported() bool { return false }
func servePTY(context.Context, *websocket.Conn, ShellConfig, []string) error {
	return errors.New("PTY shell requires Linux")
}
