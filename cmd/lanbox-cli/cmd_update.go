package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"lanbox/internal/updater"
)

// cmdUpdate 查看/检查/执行在线更新与回滚。
func cmdUpdate(ctx context.Context, g *globals, args []string, stdin *os.File) error {
	fs := g.subFS("update")
	check := fs.Bool("check", false, "立即检查更新（仅管理员）")
	apply := fs.Bool("apply", false, "下载并升级到最新版本，然后重启服务")
	rollback := fs.Bool("rollback", false, "回滚到上一版本并重启服务")
	yes := fs.Bool("yes", false, "跳过确认（脚本里必须显式给出）")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("%w: 用法 lanbox-cli update [--check|--apply|--rollback] [--yes]", errUsage)
	}
	n := 0
	for _, on := range []bool{*check, *apply, *rollback} {
		if on {
			n++
		}
	}
	if n > 1 {
		return fmt.Errorf("%w: --check / --apply / --rollback 只能选一个", errUsage)
	}

	c, _, err := newClient(g)
	if err != nil {
		return err
	}
	st, err := c.UpdateState(ctx)
	if err != nil {
		return err
	}
	if !st.Supported {
		return errors.New("服务端不支持在线更新")
	}

	switch {
	case *check:
		// 普通用户看状态是允许的，触发检查由服务端判权限（403 会原样返回）。
		if st, err = c.CheckUpdate(ctx); err != nil {
			return err
		}

	case *apply:
		if !st.Available {
			// 缓存的可能是上次的结果，先刷新一次再判断。
			if st, err = c.CheckUpdate(ctx); err != nil {
				return err
			}
		}
		if !st.Available {
			if !g.json {
				fmt.Fprintf(g.stdout, "版本   %s（已是最新）\n", st.Current)
			}
			return nil
		}
		if err := confirmOrFail(g, stdin, *yes, fmt.Sprintf("即将升级到 %s（%s），升级后服务会重启。",
			st.Latest, fmtBytes(st.Size))); err != nil {
			return err
		}
		version := st.Latest
		if st, err = c.ApplyUpdate(ctx, version); err != nil {
			return err
		}
		if !g.json {
			fmt.Fprintf(g.stdout, "已升级到 %s，服务正在重启（稍后用 lanbox-cli status 确认）。\n", version)
		}

	case *rollback:
		if !st.CanRollback {
			return errors.New("没有可回滚的备份（这个版本不是通过在线升级装的）")
		}
		if err := confirmOrFail(g, stdin, *yes, "即将回滚到上一版本，服务会重启。"); err != nil {
			return err
		}
		if st, err = c.RollbackUpdate(ctx); err != nil {
			return err
		}
		if !g.json {
			fmt.Fprintln(g.stdout, "已回滚，服务正在重启。")
		}
	}

	if g.json {
		return printJSON(g.stdout, st)
	}
	renderUpdate(g.stdout, st)
	return nil
}

// confirmOrFail 在 TTY 上要求输入 yes；非交互环境必须显式 --yes。
func confirmOrFail(g *globals, stdin *os.File, yes bool, prompt string) error {
	if yes {
		return nil
	}
	if stdin == nil || !isTerminal(stdin.Fd()) {
		return fmt.Errorf("%w: 非交互环境请加 --yes 确认", errUsage)
	}
	fmt.Fprintf(g.stderr, "%s\n输入 yes 继续：", prompt)
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "yes", "y":
		return nil
	}
	return errors.New("已取消")
}

// renderUpdate 输出人类可读的更新状态。
func renderUpdate(w io.Writer, st *updater.State) {
	cur := st.Current
	if st.Pending != "" {
		cur += "（待重启生效：" + st.Pending + "）"
	}
	fmt.Fprintf(w, "版本   %s\n", cur)
	switch {
	case st.CheckError != "":
		fmt.Fprintf(w, "检查   失败：%s\n", st.CheckError)
	case st.Latest == "":
		fmt.Fprintln(w, "检查   还没有检查过")
	case st.Available:
		size := ""
		if st.Size > 0 {
			size = "（" + fmtBytes(st.Size) + "）"
		}
		fmt.Fprintf(w, "最新   %s 可用%s%s\n", st.Latest, size, notesLine(st.Notes))
	default:
		fmt.Fprintf(w, "检查   已是最新（%s）\n", st.Latest)
	}
	if !st.CheckedAt.IsZero() {
		fmt.Fprintf(w, "时间   %s\n", st.CheckedAt.Local().Format("2006-01-02 15:04"))
	}
	if st.CanRollback {
		fmt.Fprintln(w, "回滚   可用（lanbox-cli update --rollback）")
	}
}

// notesLine 把更新说明压成一行（超过 80 字截断）。
func notesLine(notes string) string {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return ""
	}
	notes = strings.Join(strings.Fields(notes), " ")
	r := []rune(notes)
	if len(r) > 80 {
		return "：" + string(r[:80]) + "…"
	}
	return "：" + notes
}
