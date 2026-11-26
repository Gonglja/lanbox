//go:build windows

package updater

// chmodExec 在 Windows 上不需要处理可执行位。
func chmodExec(string) error { return nil }
