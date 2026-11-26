//go:build unix

package updater

import "syscall"

// freeSpace 返回 path 所在文件系统的可用字节数。
func freeSpace(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
