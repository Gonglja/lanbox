package main

import (
	"context"
	"fmt"
	"io"

	"lanbox/internal/client"
)

// cmdStatus 显示会话、服务端统计与（管理员）磁盘/用户/mDNS 状态。
func cmdStatus(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("status")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("%w: 用法 lanbox-cli status", errUsage)
	}
	c, sess, err := newClient(g)
	if err != nil {
		return err
	}
	st, err := c.Stats(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, statusJSON{Stats: *st, URL: sess.URL})
	}
	renderStatus(g.stdout, sess.URL, sess.Username, st)
	return nil
}

// statusJSON 在服务端结构上补一个顶层 url，便于脚本知道数据来自哪台机器。
type statusJSON struct {
	client.Stats
	URL string `json:"url"`
}

// renderStatus 输出人类可读的状态；非管理员没有 admin 块，磁盘/用户/mDNS 三行整体省略。
func renderStatus(w io.Writer, url, username string, st *client.Stats) {
	role := "普通用户"
	if st.Admin != nil {
		role = "管理员"
	}
	fmt.Fprintf(w, "服务   %s   可达 · %s · 运行 %s\n", url, st.Version, fmtUptime(st.UptimeS))
	fmt.Fprintf(w, "身份   %s（%s）\n", username, role)
	fmt.Fprintf(w, "内容   %d 条（未取走 %d / 已取走 %d）\n", st.Items.Total, st.Items.Todo, st.Items.Taken)
	fmt.Fprintf(w, "附件   %d 个 / %s\n", st.Files.Count, fmtBytes(st.Files.Bytes))

	a := st.Admin
	if a == nil {
		return
	}
	switch {
	case a.Disk.FreeBytes != nil:
		fmt.Fprintf(w, "磁盘   剩余 %s\n", fmtBytes(*a.Disk.FreeBytes))
	case a.Disk.Error != "":
		fmt.Fprintf(w, "磁盘   剩余未知（%s）\n", a.Disk.Error)
	default:
		fmt.Fprintln(w, "磁盘   剩余未知")
	}
	fmt.Fprintf(w, "用户   %d 个 · 监听 %s · 存储 %s\n", a.Users, a.Listen, a.ItemsDir)
	fmt.Fprintf(w, "mDNS   %s\n", mdnsLine(a.MDNS))
}

// mdnsLine 把 mDNS 状态压成一行。
func mdnsLine(m *client.StatsMDNS) string {
	if m == nil {
		return "未启用"
	}
	if !m.Enabled {
		return "未启用（配置关闭）"
	}
	if !m.Running {
		if m.Error != "" {
			return "未运行：" + m.Error
		}
		return "未运行"
	}
	if m.Host == "" {
		return "已运行"
	}
	if m.Port > 0 {
		return fmt.Sprintf("%s:%d（已运行）", m.Host, m.Port)
	}
	return m.Host + "（已运行）"
}

// fmtUptime 把秒数渲染成 3d4h / 5h6m / 7m / 8s 这类紧凑形式。
func fmtUptime(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	d, h, m, s := sec/86400, sec%86400/3600, sec%3600/60, sec%60
	switch {
	case d > 0 && h > 0:
		return fmt.Sprintf("%dd%dh", d, h)
	case d > 0:
		return fmt.Sprintf("%dd", d)
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
