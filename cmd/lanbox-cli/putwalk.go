package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lanbox/internal/client"
)

// collectUploads 把命令行路径展开成待上传列表。
// 目录必须显式 -r；目录内跳过符号链接与空目录，路径保留 "目录名/内部路径"。
func collectUploads(paths []string, recursive bool) ([]client.Upload, error) {
	var out []client.Upload
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("解析路径 %s 失败: %w", p, err)
		}
		st, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败: %w", p, err)
		}
		if !st.IsDir() {
			if st.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("%s 是符号链接，请直接指定目标文件", p)
			}
			out = append(out, newUpload(filepath.Base(abs), abs, st.Size()))
			continue
		}
		if !recursive {
			return nil, fmt.Errorf("%w: %s 是目录，需要 -r 才能上传目录", errUsage, p)
		}
		base := filepath.Dir(abs)
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			out = append(out, newUpload(filepath.ToSlash(rel), path, info.Size()))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("遍历目录 %s 失败: %w", p, err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func newUpload(relPath, diskPath string, size int64) client.Upload {
	return client.Upload{
		Path: strings.TrimPrefix(relPath, "/"),
		Size: size,
		Open: func() (io.ReadCloser, error) { return os.Open(diskPath) },
	}
}
