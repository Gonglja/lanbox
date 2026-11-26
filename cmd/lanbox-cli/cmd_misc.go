package main

import (
	"context"
	"fmt"
)

// cmdTaken 标记或取消已取走。
func cmdTaken(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("taken")
	undo := fs.Bool("undo", false, "取消已取走标记")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli taken <ID> [--undo]", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	it, err := c.Taken(ctx, pos[0], !*undo)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, it)
	}
	if it.Taken {
		fmt.Fprintf(g.stdout, "%s 已标记为已取走（%s）\n", it.ID, it.TakenBy)
	} else {
		fmt.Fprintf(g.stdout, "%s 已取消已取走标记\n", it.ID)
	}
	return nil
}

// cmdRm 删除整条内容。
func cmdRm(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("rm")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("%w: 用法 lanbox-cli rm <ID>", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	if err := c.DeleteItem(ctx, pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(g.stdout, "已删除 %s\n", pos[0])
	return nil
}

// cmdRmFile 删除单个附件。
func cmdRmFile(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("rm-file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("%w: 用法 lanbox-cli rm-file <ID> <FID>", errUsage)
	}
	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	if err := c.DeleteFile(ctx, pos[0], pos[1]); err != nil {
		return err
	}
	fmt.Fprintf(g.stdout, "已删除附件 %s\n", pos[1])
	return nil
}
