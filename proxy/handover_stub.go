//go:build !linux

package proxy

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

func handoverPortPath(proxyPort int) string {
	return HandoverSocketPath(proxyPort) + ".port"
}

func readHandoverPort(proxyPort int) (int, error) {
	data, err := os.ReadFile(handoverPortPath(proxyPort))
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return port, nil
}

func writeHandoverPort(proxyPort, handoverPort int) error {
	return os.WriteFile(handoverPortPath(proxyPort), []byte(strconv.Itoa(handoverPort)), 0644)
}

// StartListener tenta contactar o processo antigo para libertar a porta (fallback Windows/não-Linux).
func (h *HandoverCoordinator) StartListener() (net.Listener, error) {
	handoverPort, err := readHandoverPort(h.proxy.cfg.ListenPort)
	if err != nil {
		return net.Listen("tcp", fmt.Sprintf(":%d", h.proxy.cfg.ListenPort))
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", handoverPort), 2*time.Second)
	if err != nil {
		return net.Listen("tcp", fmt.Sprintf(":%d", h.proxy.cfg.ListenPort))
	}
	defer conn.Close()

	_, err = conn.Write([]byte("RELEASE_PORT"))
	if err != nil {
		return nil, fmt.Errorf("falha ao enviar comando RELEASE_PORT: %w", err)
	}

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "PORT_RELEASED" {
		return nil, fmt.Errorf("processo antigo nao libertou a porta")
	}

	var newListener net.Listener
	for i := 0; i < 10; i++ {
		newListener, err = net.Listen("tcp", fmt.Sprintf(":%d", h.proxy.cfg.ListenPort))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("falha ao dar bind na porta libertada apos 10 tentativas: %w", err)
	}

	_, _ = conn.Write([]byte("SHUTDOWN"))

	return newListener, nil
}

// RegisterHandoverServer inicia o servidor de handover via TCP (fallback Windows/não-Linux).
func (h *HandoverCoordinator) RegisterHandoverServer(listener net.Listener) {
	handoverListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	h.unixListener = handoverListener

	handoverPort := handoverListener.Addr().(*net.TCPAddr).Port
	_ = writeHandoverPort(h.proxy.cfg.ListenPort, handoverPort)

	go func() {
		defer handoverListener.Close()
		defer os.Remove(handoverPortPath(h.proxy.cfg.ListenPort))

		for {
			conn, err := handoverListener.Accept()
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

				buf := make([]byte, 64)
				n, err := c.Read(buf)
				if err != nil {
					return
				}

				cmd := string(buf[:n])
				switch cmd {
				case "RELEASE_PORT":
					if listener != nil {
						_ = listener.Close()
					}
					_, _ = c.Write([]byte("PORT_RELEASED"))

				case "SHUTDOWN":
					go h.proxy.Stop()
				}
			}(conn)
		}
	}()
}
