package server

import (
	"github.com/syklonAK/night-watcher/internal/packet/engine/flog"
	"github.com/syklonAK/night-watcher/internal/packet/engine/protocol"
	"github.com/syklonAK/night-watcher/internal/packet/engine/tnet"
)

func (s *Server) handlePing(strm tnet.Strm) {
	p := protocol.Proto{Type: protocol.PPONG}
	if err := p.Write(strm); err != nil {
		flog.Errorf("failed to send pong on stream %d: %v", strm.SID(), err)
	}
}
