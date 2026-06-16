package ebpf

import "sync/atomic"

var (
	ebpfActive uint32
)

// SetActive define o estado de ativação do eBPF.
func SetActive(active bool) {
	if active {
		atomic.StoreUint32(&ebpfActive, 1)
	} else {
		atomic.StoreUint32(&ebpfActive, 0)
	}
}

// IsActive retorna se a aceleração eBPF/Sockmap está de fato carregada e ativa no Kernel.
func IsActive() bool {
	return atomic.LoadUint32(&ebpfActive) == 1
}
