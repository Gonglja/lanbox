package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"lanbox/internal/client"
)

// summarize 把多行文本压成单行并截断到 40 字。
func summarize(text string) string {
	flat := strings.Join(strings.Fields(text), " ")
	runes := []rune(flat)
	if len(runes) <= 40 {
		return flat
	}
	return string(runes[:40]) + "…"
}

// fmtBytes 输出人类可读的字节数。
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// renderItems 输出内容列表表格。
func renderItems(w io.Writer, items []client.Item) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\t时间\t发布者\t附件\t状态\t文本")
	for _, it := range items {
		status := "未取走"
		if it.Taken {
			status = "已取走"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d 个 / %s\t%s\t%s\n",
			it.ID, it.CreatedAt.Local().Format("01-02 15:04"), it.CreatedBy,
			len(it.Files), fmtBytes(it.TotalSize()), status, summarize(it.Text))
	}
	return tw.Flush()
}

// renderFiles 输出某条内容的附件表格。
func renderFiles(w io.Writer, files []client.File) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "FID\t路径\t大小\t下载")
	for _, f := range files {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", f.ID, f.RelPath(), fmtBytes(f.Size), f.Downloads)
	}
	return tw.Flush()
}
