package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// ErrExists 表示目标文件已存在且未指定 Force。
var ErrExists = errors.New("目标文件已存在")

// DownloadOptions 控制下载行为。
type DownloadOptions struct {
	Dest       string // 目标文件路径
	Force      bool   // 覆盖已存在的目标
	Resume     bool   // 用 Range 从 .part 的已有字节续传
	OnProgress func(done, total int64)
}

// DownloadFile 下载单个附件：先写 <dest>.part，校验 sha256 后 rename 到 dest。
func (c *Client) DownloadFile(ctx context.Context, itemID string, f File, o DownloadOptions) error {
	// 目标已存在时一律要求 --force；--resume 只决定是否从 .part 续传。
	if !o.Force {
		if _, err := os.Stat(o.Dest); err == nil {
			return ErrExists
		}
	}
	tmp := o.Dest + ".part"
	var offset int64
	if o.Resume {
		if st, err := os.Stat(tmp); err == nil {
			offset = st.Size()
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base.String()+"/api/items/"+url.PathEscape(itemID)+"/files/"+url.PathEscape(f.ID), nil)
	if err != nil {
		return err
	}
	if c.sid != "" {
		req.Header.Set("Cookie", "sid="+c.sid)
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0 // 服务端不支持 Range，从头写
	case http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		// .part 已经比服务端文件还长（脏数据），丢掉重来。
		offset = 0
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			return err
		}
	default:
		return parseError(resp)
	}

	flags := os.O_WRONLY | os.O_CREATE
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	if err := os.MkdirAll(filepath.Dir(o.Dest), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(tmp, flags, 0o640)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	// 续传时先喂入已有字节，才能算出全文件哈希。
	if offset > 0 {
		existing, err := os.Open(tmp)
		if err != nil {
			out.Close()
			return err
		}
		if _, err := io.Copy(hasher, existing); err != nil {
			existing.Close()
			out.Close()
			return err
		}
		existing.Close()
	}
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), &progressReader{
		r: resp.Body, done: offset, total: f.Size, onProgress: o.OnProgress,
	})
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("写入文件失败: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("保存文件失败: %w", closeErr)
	}
	if f.Size > 0 && offset+written != f.Size {
		return fmt.Errorf("下载长度不符：期望 %d 字节，得到 %d", f.Size, offset+written)
	}
	if f.SHA256 != "" && hex.EncodeToString(hasher.Sum(nil)) != f.SHA256 {
		os.Remove(tmp)
		return fmt.Errorf("%s 校验失败：sha256 与服务器不一致", f.RelPath())
	}
	return os.Rename(tmp, o.Dest)
}

// progressReader 在读取时回报进度。
type progressReader struct {
	r          io.Reader
	done       int64
	total      int64
	onProgress func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		if p.onProgress != nil {
			p.onProgress(p.done, p.total)
		}
	}
	return n, err
}

// DownloadZip 下载整条内容的 zip 包（不支持续传）。
// dest 已存在且 force 为 false 时返回 ErrExists；否则照常写 .part 再 rename。
func (c *Client) DownloadZip(ctx context.Context, itemID, dest string, force bool) error {
	if !force {
		if _, err := os.Stat(dest); err == nil {
			return ErrExists
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base.String()+"/api/items/"+url.PathEscape(itemID)+"/zip", nil)
	if err != nil {
		return err
	}
	if c.sid != "" {
		req.Header.Set("Cookie", "sid="+c.sid)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return parseError(resp)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tmp := dest + ".part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("写入 zip 失败: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
