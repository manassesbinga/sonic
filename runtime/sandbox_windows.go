//go:build windows
package runtime

import (
	"os/exec"
)

func configureSysProcAttr(cmd *exec.Cmd) {
	// No Windows, por agora, handles do processo pai não são herdados desnecessariamente.
	// O Go já define isso por padrão. Lógica de Job Objects adicionais pode ser estendida no futuro.
}
