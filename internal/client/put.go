package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
)

// Upload 是一个待上传的文件。
type Upload struct {
	Path string // 内容内的相对路径，如 "photos/a.jpg"
	Size int64
	Open func() (io.ReadCloser, error)
}

// Put 上传文字与文件，返回服务端创建的内容。
// onProgress 可为 nil；sent 与 total 都包含文本字节。
func (c *Client) Put(ctx context.Context, text string, files []Upload, onProgress func(sent, total int64)) (*Item, error) {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	total += int64(len(text))

	// 用管道边写边发，避免把整个上传体攒进内存。
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := writeMultipart(mw, text, files, onProgress, total)
		if cerr := mw.Close(); err == nil {
			err = cerr
		}
		pw.CloseWithError(err)
	}()

	resp, err := c.do(ctx, http.MethodPost, "/api/items", pr, mw.FormDataContentType())
	if err != nil {
		// 请求提前失败时唤醒写协程，否则它会阻塞在管道上。
		pr.CloseWithError(err)
		return nil, err
	}
	defer resp.Body.Close()
	var it Item
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("解析上传响应失败: %w", err)
	}
	return &it, nil
}

// writeMultipart 把文本与文件写进 multipart 流，按已写字节回报进度（含文本）。
func writeMultipart(mw *multipart.Writer, text string, files []Upload, onProgress func(int64, int64), total int64) error {
	var sent int64
	if text != "" {
		w, err := mw.CreateFormField("text")
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, text); err != nil {
			return err
		}
		sent += int64(len(text))
		if onProgress != nil {
			onProgress(sent, total)
		}
	}
	buf := make([]byte, 64<<10)
	for _, f := range files {
		// Go 的 Part.FileName() 会丢弃目录信息，路径只能放在字段名里。
		w, err := mw.CreateFormFile("files:"+f.Path, path.Base(f.Path))
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		for {
			n, rerr := rc.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					rc.Close()
					return werr
				}
				sent += int64(n)
				if onProgress != nil {
					onProgress(sent, total)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				rc.Close()
				return rerr
			}
		}
		rc.Close()
	}
	return nil
}
