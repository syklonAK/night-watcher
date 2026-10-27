package server

import (
	"github.com/syklonAK/night-watcher/internal/packet/engine/protocol"
	"github.com/syklonAK/night-watcher/internal/packet/engine/tnet"
)

func (s *Server) handleTCPF(strm tnet.Strm, p *protocol.Proto) {
	if len(p.TCPF) == 0 {
		s.listener.DeleteClientTCPF(strm.RemoteAddr())
		return
	}
	s.listener.SetClientTCPF(strm.RemoteAddr(), p.TCPF)
}
