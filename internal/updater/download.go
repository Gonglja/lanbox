package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// tempPrefix 是下载临时文件前缀；启动时可用 CleanupTemps 清理遗留。
const tempPrefix = ".lanbox-update-"

// tempName 生成同目录下的临时文件名。版本号里的路径分隔符会被替换掉。
func tempName(version string) string {
	safe := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, version)
	if safe == "" {
		safe = "new"
	}
	return tempPrefix + safe + ".tmp"
}

// Download 把 Release 的资产下载到 dir 下的临时文件，边下边算 SHA256，
// 校验大小、摘要与二进制魔数。任何一步失败都会删掉临时文件并返回带阶段的错误。
func (u *Updater) Download(ctx context.Context, dir string, r Release, progress func(done, total int64)) (string, error) {
	if r.AssetURL == "" {
		return "", failf("download", "缺少下载地址")
	}
	if r.Version == "" {
		return "", failf("download", "缺少版本号")
	}
	// 没有校验值宁可不升级：绝不静默跳过校验。
	if !validSHA256(r.SHA256) {
		return "", failf("verify", "缺少合法的 sha256 校验值，拒绝下载")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", failf("permission", "创建下载目录 %s 失败: %v", dir, err)
	}
	if r.Size > 0 {
		if free, err := freeSpace(dir); err == nil && free < 2*r.Size {
			return "", failf("download", "磁盘剩余 %s，不足以容纳 %s 的更新包（需要两倍空间）",
				humanBytes(free), humanBytes(r.Size))
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.AssetURL, nil)
	if err != nil {
		return "", failf("download", "%v", err)
	}
	req.Header.Set("User-Agent", "lanbox-updater/"+u.cur.String())
	if tok := u.source().Token; tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return "", failf("download", "请求 %s 失败: %v", r.AssetURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", failf("download", "下载 %s 失败: HTTP %d", r.AssetName, resp.StatusCode)
	}

	tmp := filepath.Join(dir, tempName(r.Version))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", failf("permission", "无法写入 %s（程序目录不可写？）: %v", dir, err)
	}
	h := sha256.New()
	rd := &progressReader{r: resp.Body, total: r.Size, fn: progress}
	n, err := io.Copy(io.MultiWriter(f, h), rd)
	if err == nil && progress != nil {
		progress(n, r.Size) // 收尾一定报一次，小文件也能看到 100%
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", failf("download", "写入 %s 失败: %v", tmp, err)
	}
	if r.Size > 0 && n != r.Size {
		os.Remove(tmp)
		return "", failf("verify", "下载大小不符：得到 %d 字节，清单写的是 %d", n, r.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != r.SHA256 {
		os.Remove(tmp)
		return "", failf("verify", "校验失败：得到 %s，期望 %s", got, r.SHA256)
	}
	if err := checkMagic(tmp); err != nil {
		os.Remove(tmp)
		return "", failf("verify", "%v", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return "", failf("permission", "设置可执行权限失败: %v", err)
	}
	return tmp, nil
}

// checkMagic 检查文件头，防止把登录页/错误页当成二进制。
func checkMagic(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch runtime.GOOS {
	case "windows":
		if n < 2 || head[0] != 'M' || head[1] != 'Z' {
			return fmt.Errorf("下载到的不是 Windows 可执行文件（可能是错误页或重定向落地页）")
		}
	default:
		if n < 4 || head[0] != 0x7f || head[1] != 'E' || head[2] != 'L' || head[3] != 'F' {
			return fmt.Errorf("下载到的不是 ELF 可执行文件（可能是错误页或重定向落地页）")
		}
	}
	return nil
}

// progressReader 在读取过程中回调进度（调用方自行节流）。
type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	fn    func(done, total int64)
	next  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.fn != nil && time.Now().After(p.next) {
		p.fn(p.done, p.total)
		p.next = time.Now().Add(200 * time.Millisecond)
	}
	return n, err
}

// humanBytes 把字节数压成 12.3 MB 这类形式。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
