package proxy

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
)

// HandoverSocketPath retorna o caminho do ficheiro de coordenação de handover baseado na porta.
func HandoverSocketPath(port int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("sonic-handover-%d", port))
}


// HandoverCoordinator coordena o zero-downtime upgrade/reload do Sonic.
type HandoverCoordinator struct {
	proxy        *TransparentProxy
	unixListener net.Listener
	shutdown     chan struct{}
	stopped      atomic.Bool
}

// NewHandoverCoordinator cria um novo coordenador de handover para o proxy fornecido.
func NewHandoverCoordinator(proxy *TransparentProxy) *HandoverCoordinator {
	return &HandoverCoordinator{
		proxy:    proxy,
		shutdown: make(chan struct{}),
	}
}

// Stop encerra o servidor de handover e liberta o socket UNIX.
func (h *HandoverCoordinator) Stop() {
	if h.stopped.Load() {
		return
	}
	h.stopped.Store(true)
	close(h.shutdown)
	if h.unixListener != nil {
		_ = h.unixListener.Close()
	}
}
