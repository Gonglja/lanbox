package main

import (
	"fmt"
	"io"
)

// progressPrinter 在 TTY 上就地刷新上传进度，非 TTY 时每 10% 报一次。
func progressPrinter(stderr io.Writer, isTTY bool) func(sent, total int64) {
	lastPct := int64(-10)
	return func(sent, total int64) {
		if total <= 0 {
			return
		}
		pct := sent * 100 / total
		if isTTY {
			fmt.Fprintf(stderr, "\r已发送 %s / %s（%d%%）", fmtBytes(sent), fmtBytes(total), pct)
			return
		}
		if pct-lastPct >= 10 {
			lastPct = pct
			fmt.Fprintf(stderr, "已发送 %d%%\n", pct)
		}
	}
}

// finishProgress 收尾，避免 TTY 上的进度行残留。
func finishProgress(stderr io.Writer, isTTY bool) {
	if isTTY {
		fmt.Fprint(stderr, "\r\x1b[K")
	}
}
