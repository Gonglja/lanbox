//go:build unix

package updater

import (
	"fmt"
	"os"
)

func chmodExec(path string) error {
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("设置可执行权限失败: %w", err)
	}
	return nil
}
