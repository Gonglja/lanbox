package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lanbox/internal/client"
)

// downloadedFile 是一条成功下载的记录，同时用于 JSON 输出。
type downloadedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// downloadSummary 汇总下载结果。
type downloadSummary struct {
	Downloaded []downloadedFile
	Skipped    []string
	Failed     []string
}

// withNoneFailed 判断是否全部成功（没有失败项）。
func (s *downloadSummary) withNoneFailed() bool { return len(s.Failed) == 0 }

// err 把失败聚合成一个错误；没有失败时返回 nil。
func (s *downloadSummary) err() error {
	if s.withNoneFailed() {
		return nil
	}
	return fmt.Errorf("%d 个文件下载失败: %s", len(s.Failed), strings.Join(s.Failed, ", "))
}

// getJSON 是 get --json 的输出结构，字段按实际情况出现。
type getJSON struct {
	Downloaded []downloadedFile `json:"downloaded,omitempty"`
	Skipped    []string         `json:"skipped,omitempty"`
	Text       string           `json:"text,omitempty"`
}

// destinationPath 计算落盘路径：单附件且路径不含目录时压平，否则保留相对路径。
// 越界由 safeJoin 兜底，规则本身只看是否单附件与是否带目录。
func destinationPath(outDir, rel string, single bool) (string, error) {
	if single && !strings.Contains(rel, "/") {
		rel = filepath.Base(rel)
	}
	return safeJoin(outDir, rel)
}

// safeJoin 把内容内的相对路径安全地拼到 base 下，越界返回错误。
func safeJoin(base, rel string) (string, error) {
	// 服务端返回的必须是相对路径。Windows 的 filepath.IsAbs 不认 "/etc/passwd"
	// （它没有盘符），所以先按两种分隔符显式拒绝以根开头的写法。
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("服务端返回的路径不安全: %q", rel)
	}
	if filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("服务端返回的路径不安全: %q", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("服务端返回的路径不安全: %q", rel)
	}
	dest := filepath.Join(base, clean)
	// 用绝对路径复核前缀：base 为 "." 之类的相对路径时直接比较会误判。
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if absDest != absBase && !strings.HasPrefix(absDest, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("服务端返回的路径不安全: %q", rel)
	}
	return dest, nil
}

// cmdGet 下载某条内容的附件或整条 zip。
func cmdGet(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("get")
	out := fs.String("o", ".", "下载到哪个目录")
	zipPath := fs.String("zip", "", "下载整条内容为 zip 文件")
	force := fs.Bool("force", false, "覆盖已存在的文件")
	resume := fs.Bool("resume", false, "单文件断点续传")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli get <ID> [-o 目录] [--zip 文件]", errUsage)
	}
	if *zipPath != "" && *resume {
		return fmt.Errorf("%w: --zip 与 --resume 不能同时使用", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	it, err := c.GetItem(ctx, pos[0])
	if err != nil {
		return err
	}

	if *zipPath != "" {
		if len(it.Files) == 0 {
			return fmt.Errorf("这条内容没有附件，无法打包")
		}
		// 是否存在交给 client 判断，这样 --force 的语义只有一处。
		err := c.DownloadZip(ctx, it.ID, *zipPath, *force)
		if errors.Is(err, client.ErrExists) {
			fmt.Fprintf(g.stderr, "跳过已存在：%s\n", *zipPath)
			return nil
		}
		if err != nil {
			return err
		}
		if g.json {
			return printJSON(g.stdout, map[string]any{"zip": *zipPath, "files": len(it.Files)})
		}
		fmt.Fprintf(g.stdout, "已保存 %s（%d 个附件）\n", *zipPath, len(it.Files))
		return nil
	}

	// 纯文字内容直接打印。
	if len(it.Files) == 0 {
		if *resume {
			fmt.Fprintln(g.stderr, "提示: --resume 只对附件下载生效")
		}
		if g.json {
			return printJSON(g.stdout, getJSON{Text: it.Text})
		}
		fmt.Fprint(g.stdout, it.Text)
		if it.Text != "" {
			fmt.Fprintln(g.stdout)
		}
		return nil
	}

	// 需要落盘时先确认 -o 是个目录，避免写出 -o 指定的文件名。
	if st, err := os.Stat(*out); err == nil && !st.IsDir() {
		return fmt.Errorf("%w: -o %s 不是目录", errUsage, *out)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查 -o 目录失败: %w", err)
	}

	tty := isTerminal(os.Stderr.Fd())
	single := len(it.Files) == 1
	if *resume && !single {
		// 提示放在循环之前，免得被逐文件进度覆盖。
		fmt.Fprintln(g.stderr, "提示: --resume 只对单附件内容生效")
	}

	sum := &downloadSummary{}
	for _, f := range it.Files {
		rel := f.RelPath()
		dest, err := destinationPath(*out, rel, single)
		if err != nil {
			fmt.Fprintf(g.stderr, "跳过不安全的路径 %s：%v\n", rel, err)
			sum.Failed = append(sum.Failed, rel)
			continue
		}
		opts := client.DownloadOptions{
			Dest:   dest,
			Force:  *force,
			Resume: *resume && single,
			OnProgress: func(done, total int64) {
				if tty && total > 0 {
					fmt.Fprintf(g.stderr, "\r%s：%d%%", rel, done*100/total)
				}
			},
		}
		err = c.DownloadFile(ctx, it.ID, f, opts)
		if tty {
			fmt.Fprint(g.stderr, "\r\x1b[K")
		}
		if errors.Is(err, client.ErrExists) {
			fmt.Fprintf(g.stderr, "跳过已存在：%s\n", dest)
			sum.Skipped = append(sum.Skipped, dest)
			continue
		}
		if err != nil {
			// 单个文件失败不影响其余文件，最后再汇总报错。
			fmt.Fprintf(g.stderr, "下载失败 %s：%v\n", dest, err)
			sum.Failed = append(sum.Failed, dest)
			continue
		}
		sum.Downloaded = append(sum.Downloaded, downloadedFile{Path: dest, Size: f.Size})
		if !g.json {
			fmt.Fprintf(g.stdout, "已下载 %s（%s）\n", dest, fmtBytes(f.Size))
		}
	}

	if g.json {
		if err := printJSON(g.stdout, getJSON{
			Downloaded: sum.Downloaded,
			Skipped:    sum.Skipped,
		}); err != nil {
			return err
		}
	}
	// 有失败时返回聚合错误（退出码 1），全部成功才算成功。
	if !sum.withNoneFailed() {
		return sum.err()
	}
	return nil
}
