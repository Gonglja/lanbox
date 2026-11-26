package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"lanbox/internal/client"
)

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
		if err := c.DownloadZip(ctx, it.ID, *zipPath); err != nil {
			return err
		}
		fmt.Fprintf(g.stdout, "已保存 %s（%d 个附件）\n", *zipPath, len(it.Files))
		return nil
	}

	// 纯文字内容直接打印。
	if len(it.Files) == 0 {
		fmt.Fprint(g.stdout, it.Text)
		if it.Text != "" {
			fmt.Fprintln(g.stdout)
		}
		return nil
	}

	tty := isTerminal(os.Stderr.Fd())
	single := len(it.Files) == 1
	for _, f := range it.Files {
		rel := f.RelPath()
		if single {
			rel = filepath.Base(rel)
		}
		dest := filepath.Join(*out, filepath.FromSlash(rel))
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
		err := c.DownloadFile(ctx, it.ID, f, opts)
		if errors.Is(err, client.ErrExists) {
			fmt.Fprintf(g.stdout, "跳过已存在：%s\n", dest)
			continue
		}
		if tty {
			fmt.Fprint(g.stderr, "\r\x1b[K")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(g.stdout, "已下载 %s（%s）\n", dest, fmtBytes(f.Size))
	}
	if *resume && !single {
		fmt.Fprintln(g.stderr, "提示: --resume 只对单附件内容生效")
	}
	return nil
}
