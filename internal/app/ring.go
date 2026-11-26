package app

import (
	"strings"
	"sync"
)

// Ring 是保留最近 N 行的环形日志缓冲。
// 它实现 io.Writer，可直接作为 log.Logger 的输出之一（Android 只看这个）。
type Ring struct {
	mu    sync.Mutex
	max   int
	lines []string
	buf   strings.Builder
}

// NewRing 创建最多保存 max 行的环形缓冲。
func NewRing(max int) *Ring {
	if max <= 0 {
		max = 200
	}
	return &Ring{max: max, lines: make([]string, 0, max)}
}

// Write 按换行切分后逐行入环；不完整的尾行留在缓冲里等下一次写入。
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	r.buf.Write(p)
	for {
		text := r.buf.String()
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		r.append(strings.TrimRight(text[:i], "\r"))
		r.buf.Reset()
		r.buf.WriteString(text[i+1:])
	}
	return n, nil
}

func (r *Ring) append(line string) {
	if len(r.lines) == r.max {
		copy(r.lines, r.lines[1:])
		r.lines[len(r.lines)-1] = line
		return
	}
	r.lines = append(r.lines, line)
}

// String 返回当前缓冲的全部行（含尚未换行的尾行）。
func (r *Ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := strings.Join(r.lines, "\n")
	if tail := r.buf.String(); tail != "" {
		if out != "" {
			out += "\n"
		}
		out += tail
	}
	return out
}
