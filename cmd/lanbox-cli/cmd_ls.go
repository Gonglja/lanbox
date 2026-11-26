package main

import (
	"context"
	"fmt"

	"lanbox/internal/client"
)

// cmdLs 列出内容，-o 时同时打印附件表。
func cmdLs(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("ls")
	limit := fs.Int("n", 20, "返回条数")
	search := fs.String("q", "", "按文本/文件名搜索")
	todo := fs.Bool("todo", false, "只看未取走")
	withFiles := fs.Bool("o", false, "同时打印附件")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	res, err := c.List(ctx, client.ListQuery{Limit: *limit, Search: *search, Todo: *todo})
	if err != nil {
		return err
	}
	if g.json {
		if *withFiles {
			return printJSON(g.stdout, res)
		}
		// 不带 -o 时输出不含附件的副本，避免 JSON 体积过大。
		brief := &client.ListResult{NextCursor: res.NextCursor}
		for _, it := range res.Items {
			it.Files = nil
			brief.Items = append(brief.Items, it)
		}
		return printJSON(g.stdout, brief)
	}
	if err := renderItems(g.stdout, res.Items); err != nil {
		return err
	}
	if *withFiles {
		for _, it := range res.Items {
			if len(it.Files) == 0 {
				continue
			}
			fmt.Fprintf(g.stdout, "\n%s:\n", it.ID)
			if err := renderFiles(g.stdout, it.Files); err != nil {
				return err
			}
		}
	}
	return nil
}

// cmdFiles 列出某条内容的附件。
func cmdFiles(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("files")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli files <ID>", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	it, err := c.GetItem(ctx, pos[0])
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, it.Files)
	}
	if len(it.Files) == 0 {
		fmt.Fprintln(g.stdout, "这条内容没有附件")
		return nil
	}
	return renderFiles(g.stdout, it.Files)
}
