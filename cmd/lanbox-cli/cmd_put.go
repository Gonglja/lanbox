package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"lanbox/internal/client"
)

// maxStdinText 是标准输入文本的上限：1MiB，远大于服务端默认 64KiB 文本上限。
const maxStdinText = 1 << 20

// cmdPut 发布文字与/或文件、目录。
func cmdPut(ctx context.Context, g *globals, args []string, stdin *os.File) error {
	fs := g.subFS("put")
	text := fs.String("m", "", "随附件一起发布的文字")
	recursive := fs.Bool("r", false, "递归上传目录")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	// "-" 表示从标准输入读文本，只能出现一次，且不能与 -m 同时使用。
	dashCount := 0
	filePaths := make([]string, 0, len(pos))
	for _, p := range pos {
		if p == "-" {
			dashCount++
			continue
		}
		filePaths = append(filePaths, p)
	}
	if dashCount > 1 {
		return fmt.Errorf("%w: 只能出现一个 -", errUsage)
	}
	if dashCount == 1 && *text != "" {
		return fmt.Errorf("%w: -m 与 - 不能同时使用", errUsage)
	}

	body := *text
	if dashCount == 1 {
		// 多读一个字节就能判断是否超限，避免静默截断用户输入。
		raw, err := io.ReadAll(io.LimitReader(stdin, maxStdinText+1))
		if err != nil {
			return fmt.Errorf("读取标准输入失败: %w", err)
		}
		if len(raw) > maxStdinText {
			return fmt.Errorf("%w: 标准输入超过 %d 字节上限，请改用 -m 或文件上传", errUsage, maxStdinText)
		}
		body = string(raw)
	}

	var files []client.Upload
	if len(filePaths) > 0 {
		ups, err := collectUploads(filePaths, *recursive)
		if err != nil {
			return err
		}
		files = ups
	}
	if body == "" && len(files) == 0 {
		return fmt.Errorf("%w: 请提供文字（-m 或 -）或文件路径", errUsage)
	}

	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	tty := isTerminal(os.Stderr.Fd())
	it, err := c.Put(ctx, body, files, progressPrinter(g.stderr, tty))
	finishProgress(g.stderr, tty)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, it)
	}
	fmt.Fprintf(g.stdout, "已发布 %s（%d 个附件，共 %s）\n", it.ID, len(it.Files), fmtBytes(it.TotalSize()))
	return nil
}
