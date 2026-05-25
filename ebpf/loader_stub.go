//go:build !linux

package ebpf

import "fmt"

type EBPFLoader struct{}

func NewEBPFLoader() *EBPFLoader {
	return &EBPFLoader{}
}

// LoadSockmap e um stub fora de Linux que ativa o modo Fallback TCP graciosamente no Windows.
func (l *EBPFLoader) LoadSockmap() error {
	fmt.Println("[WARN] Sockmap/eBPF nao e suportado fora do Linux.")
	fmt.Println("[WARN] O ChannelWorkers funcionara exclusivamente no modo Fallback TCP Transparent Proxy em User-Space.")
	return nil
}

// Unload realiza o desligamento do stub.
func (l *EBPFLoader) Unload() {
	// No-op
}
