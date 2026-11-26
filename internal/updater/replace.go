package updater

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNoBackup 表示没有可回滚的备份（从未升级过，或备份已被清理）。
var ErrNoBackup = errors.New("没有可回滚的备份")

// BackupPath 返回旧二进制的备份路径（与当前 exe 同目录）。
func BackupPath(exePath string) string { return exePath + ".old" }

// HasBackup 判断是否存在可回滚的备份。
func HasBackup(exePath string) bool {
	_, err := os.Stat(BackupPath(exePath))
	return err == nil
}

// Stage 用已下载并校验过的 newPath 替换 exePath：
//
//	exePath            → exePath.old   （旧版本留作备份）
//	newPath            → exePath
//
// 第二步失败时会把备份改回原名，保证 exePath 始终是一个可用的程序。
func Stage(exePath, newPath string) (string, error) {
	if exePath == "" || newPath == "" {
		return "", failf("replace", "替换路径为空")
	}
	dir := filepath.Dir(exePath)
	if err := probeWritable(dir); err != nil {
		return "", err
	}
	if same, err := sameFile(exePath, newPath); err == nil && same {
		return "", failf("replace", "新文件就是当前程序本身，拒绝替换")
	}

	backup := BackupPath(exePath)
	if err := os.Remove(backup); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", failf("permission", "删除旧备份 %s 失败: %v", backup, err)
	}
	if err := os.Rename(exePath, backup); err != nil {
		return "", wrapReplace("把当前程序改名为 %s 失败", err, backup)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		// 尽力还原，避免留下「既没新程序也没原程序」的状态。
		if rerr := os.Rename(backup, exePath); rerr != nil {
			return "", failf("replace", "替换失败且还原失败：%v / %v（原程序在 %s）", err, rerr, backup)
		}
		return "", wrapReplace("把新版本写入 %s 失败", err, exePath)
	}
	if err := postReplace(exePath); err != nil {
		return "", failf("replace", "%v", err)
	}
	return backup, nil
}

// Rollback 把备份换回来（当前程序留作 .failed 便于排查）。
func Rollback(exePath string) (string, error) {
	backup := BackupPath(exePath)
	if _, err := os.Stat(backup); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNoBackup
		}
		return "", failf("replace", "读取备份 %s 失败: %v", backup, err)
	}
	if err := probeWritable(filepath.Dir(exePath)); err != nil {
		return "", err
	}
	failed := exePath + ".failed"
	if err := os.Remove(failed); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", failf("permission", "清理 %s 失败: %v", failed, err)
	}
	if err := os.Rename(exePath, failed); err != nil {
		return "", wrapReplace("把当前程序改名为 %s 失败", err, failed)
	}
	if err := os.Rename(backup, exePath); err != nil {
		if rerr := os.Rename(failed, exePath); rerr != nil {
			return "", failf("replace", "回滚失败且还原失败：%v / %v（当前程序在 %s）", err, rerr, failed)
		}
		return "", wrapReplace("把备份写回 %s 失败", err, exePath)
	}
	if err := postReplace(exePath); err != nil {
		return "", failf("replace", "%v", err)
	}
	return failed, nil
}

// CleanupTemps 删除 dir 里遗留的下载临时文件（启动时调用）。
func CleanupTemps(dir string) int {
	matches, err := filepath.Glob(filepath.Join(dir, tempPrefix+"*.tmp"))
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range matches {
		if os.Remove(m) == nil {
			n++
		}
	}
	return n
}

// probeWritable 在 dir 里建一个探测文件，提前暴露「目录不可写」。
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, tempPrefix+"probe-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return failf("permission", "程序所在目录 %s 不可写：请把它放到可写目录，或用 sudo / 管理员身份运行", dir)
		}
		return failf("permission", "在 %s 里创建临时文件失败: %v", dir, err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// wrapReplace 把常见 errno 翻译成更具体的阶段。
func wrapReplace(format string, err error, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if errors.Is(err, fs.ErrPermission) {
		return failf("permission", "%s：权限不足（%v）", msg, err)
	}
	return failf("replace", "%s：%v", msg, err)
}

// sameFile 判断两个路径是否指向同一个文件（不存在时返回 error）。
func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// postReplace 处理平台特有的收尾（unix 上确保可执行位）。
//
//nolint:unused // windows 版本为空实现
func postReplace(exePath string) error {
	return chmodExec(exePath)
}
