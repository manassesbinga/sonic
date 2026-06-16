//go:build !linux && !windows
package runtime

import (
	"os/exec"
)

func configureSysProcAttr(cmd *exec.Cmd) {
}
