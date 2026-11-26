package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"lanbox/internal/client"
)

// cmdPut 发布文字与/或文件、目录。
func cmdPut(ctx context.Context, g *globals, args []string, stdin *os.File) error {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	text := fs.String("m", "", "随附件一起发布的文字")
	recursive := fs.Bool("r", false, "递归上传目录")
	if err := fs.Parse(flagsFirst(args, "-m")); err != nil {
		return errUsage
	}

	body := *text
	var files []client.Upload
	filePaths := make([]string, 0, len(fs.Args()))
	for _, p := range fs.Args() {
		if p == "-" {
			raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
			if err != nil {
				return fmt.Errorf("读取标准输入失败: %w", err)
			}
			if body == "" {
				body = string(raw)
			}
			continue
		}
		filePaths = append(filePaths, p)
	}
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
