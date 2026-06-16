//go:build linux

package proxy

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// StartListener tenta obter o listener herdado via UNIX socket do processo antigo.
// Se não encontrar ou falhar, inicia um listener TCP novo normal.
func (h *HandoverCoordinator) StartListener() (net.Listener, error) {
	socketPath := HandoverSocketPath(h.proxy.cfg.ListenPort)

	// Tenta conectar ao processo antigo primeiro (sem TOCTOU)
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		// Se o socket UNIX não existe ou está órfão, apaga-o e inicia listener normal
		if _, statErr := os.Stat(socketPath); statErr == nil {
			_ = os.Remove(socketPath)
		}
		return net.Listen("tcp", fmt.Sprintf(":%d", h.proxy.cfg.ListenPort))
	}
	defer conn.Close()

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return net.Listen("tcp", fmt.Sprintf(":%d", h.proxy.cfg.ListenPort))
	}

	// Solicita o listener
	_, err = unixConn.Write([]byte("GET_LISTENER"))
	if err != nil {
		return nil, fmt.Errorf("falha ao solicitar listener: %w", err)
	}

	// Recebe o FD via OOB (Out of Band)
	buf := make([]byte, 32)
	oob := make([]byte, syscall.CmsgSpace(4)) // Espaço para 1 FD
	_, oobn, _, _, err := unixConn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, fmt.Errorf("falha ao ler FD do socket UNIX: %w", err)
	}

	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) == 0 {
		return nil, fmt.Errorf("nenhum file descriptor recebido do processo antigo")
	}

	fds, err := syscall.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) == 0 {
		return nil, fmt.Errorf("falha ao fazer parse do UnixRights: %w", err)
	}

	// Reconstrói o listener a partir do FD
	file := os.NewFile(uintptr(fds[0]), "inherited-listener")
	defer file.Close() // net.FileListener duplica o FD, por isso podemos fechar o original

	inheritedListener, err := net.FileListener(file)
	if err != nil {
		return nil, fmt.Errorf("falha ao reconstruir listener a partir do FD: %w", err)
	}

	// Envia sinal ao processo antigo para encerrar
	_, _ = unixConn.Write([]byte("SHUTDOWN"))

	return inheritedListener, nil
}

// RegisterHandoverServer inicia o servidor UNIX socket em background para transferir o listener.
func (h *HandoverCoordinator) RegisterHandoverServer(listener net.Listener) {
	socketPath := HandoverSocketPath(h.proxy.cfg.ListenPort)
	_ = os.Remove(socketPath) // Limpa socket anterior se houver

	unixListener, err := net.Listen("unix", socketPath)
	if err != nil {
		return
	}
	h.unixListener = unixListener

	go func() {
		defer unixListener.Close()
		defer os.Remove(socketPath)

		for {
			conn, err := unixListener.Accept()
			if err != nil {
				select {
				case <-h.shutdown:
					return
				default:
					return
				}
			}

			go func(c net.Conn) {
				defer c.Close()
				unixConn, ok := c.(*net.UnixConn)
				if !ok {
					return
				}

				buf := make([]byte, 64)
				n, err := unixConn.Read(buf)
				if err != nil {
					return
				}

				cmd := string(buf[:n])
				switch cmd {
				case "GET_LISTENER":
					tcpListener, ok := listener.(*net.TCPListener)
					if !ok {
						return
					}
					file, err := tcpListener.File()
					if err != nil {
						return
					}
					defer file.Close()

					rights := syscall.UnixRights(int(file.Fd()))
					_, _, err = unixConn.WriteMsgUnix([]byte("OK"), rights, nil)
					if err != nil {
						return
					}

				case "SHUTDOWN":
					go h.proxy.Stop()
				}
			}(conn)
		}
	}()
}
