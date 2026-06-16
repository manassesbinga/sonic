//go:build windows

package cli

import (
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode    = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode    = kernel32.NewProc("SetConsoleMode")
	procGetStdHandle      = kernel32.NewProc("GetStdHandle")
)

func init() {
	enableVT(-11) // STD_OUTPUT_HANDLE
	enableVT(-12) // STD_ERROR_HANDLE
}

func enableVT(nStdHandle int) {
	handle, _, _ := procGetStdHandle.Call(uintptr(nStdHandle))
	if handle == 0 || handle == ^uintptr(0) {
		return
	}
	var mode uint32
	ret, _, _ := procGetConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
	if ret == 0 {
		return
	}
	mode |= 0x0004 // ENABLE_VIRTUAL_TERMINAL_PROCESSING
	procSetConsoleMode.Call(handle, uintptr(mode))
}
