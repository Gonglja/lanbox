package qr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// goldenCase 由 Python 的 qrcode 库在相同版本/纠错等级/掩码下生成，
// 见 testdata/golden.json 的说明（生成脚本见文件注释）。
type goldenCase struct {
	Text    string   `json:"text"`
	Version int      `json:"version"`
	Mask    int      `json:"mask"`
	Auto    bool     `json:"auto"`
	Rows    []string `json:"rows"`
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatalf("读取 golden.json 失败: %v", err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("解析 golden.json 失败: %v", err)
	}
	if len(cases) < 20 {
		t.Fatalf("golden 用例太少: %d", len(cases))
	}
	return cases
}

func renderRows(c *Code) []string {
	rows := make([]string, c.Size)
	for y := 0; y < c.Size; y++ {
		var b strings.Builder
		for x := 0; x < c.Size; x++ {
			if c.Modules[y][x] {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		rows[y] = b.String()
	}
	return rows
}

// TestGoldenMatrices 逐个模块比对：覆盖版本选择、纠错分块与交织、
// 掩码 + 罚分选优（auto 用例）、格式信息与版本信息。
func TestGoldenMatrices(t *testing.T) {
	for _, tc := range loadGolden(t) {
		name := tc.Text
		if len(name) > 24 {
			name = name[:24] + "…"
		}
		t.Run(name+"/"+maskName(tc), func(t *testing.T) {
			var (
				code *Code
				err  error
			)
			if tc.Auto {
				code, err = Encode(tc.Text)
			} else {
				code, err = EncodeWithMask(tc.Text, tc.Mask)
			}
			if err != nil {
				t.Fatalf("编码失败: %v", err)
			}
			if code.Version != tc.Version {
				t.Fatalf("版本应为 %d，得到 %d", tc.Version, code.Version)
			}
			if tc.Auto && code.Mask != tc.Mask {
				t.Fatalf("罚分选优应选掩码 %d，得到 %d", tc.Mask, code.Mask)
			}
			if code.Size != len(tc.Rows) {
				t.Fatalf("尺寸应为 %d，得到 %d", len(tc.Rows), code.Size)
			}
			got := renderRows(code)
			for y := range tc.Rows {
				if got[y] != tc.Rows[y] {
					t.Fatalf("第 %d 行不匹配\n期望 %s\n得到 %s", y, tc.Rows[y], got[y])
				}
			}
		})
	}
}

func maskName(tc goldenCase) string {
	if tc.Auto {
		return "auto"
	}
	return "mask" + string(rune('0'+tc.Mask))
}

// TestAutoMaskMatchesReference 用 Nayuki 的参考实现（qrcodegen）做对照：
// 自动掩码选择必须与它一致，且矩阵逐模块相同。
func TestAutoMaskMatchesReference(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden_auto.json")
	if err != nil {
		t.Fatalf("读取 golden_auto.json 失败: %v", err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("解析 golden_auto.json 失败: %v", err)
	}
	if len(cases) < 4 {
		t.Fatalf("对照用例太少: %d", len(cases))
	}
	for _, tc := range cases {
		code, err := Encode(tc.Text)
		if err != nil {
			t.Fatalf("%q 编码失败: %v", tc.Text, err)
		}
		if code.Version != tc.Version || code.Mask != tc.Mask {
			t.Fatalf("%q 应选 v%d/mask%d，得到 v%d/mask%d",
				tc.Text, tc.Version, tc.Mask, code.Version, code.Mask)
		}
		got := renderRows(code)
		for y := range tc.Rows {
			if got[y] != tc.Rows[y] {
				t.Fatalf("%q 第 %d 行与参考实现不一致\n期望 %s\n得到 %s", tc.Text, y, tc.Rows[y], got[y])
			}
		}
	}
}

// TestSVGMatchesModules 保证 SVG 里画的方块与矩阵一致（扫描端看到的就是它）。
func TestSVGMatchesModules(t *testing.T) {
	code, err := Encode("https://lanbox.local:8443")
	if err != nil {
		t.Fatal(err)
	}
	svg := code.SVG(4)
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("SVG 结构不对: %s", svg)
	}
	grid := parseSVG(t, svg, code.Size+8)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			want := code.Modules[y][x]
			got := grid[y+4][x+4]
			if got != want {
				t.Fatalf("模块 (%d,%d) 不一致：SVG=%v 矩阵=%v", x, y, got, want)
			}
		}
	}
	// 静区必须是白的
	for i := 0; i < code.Size+8; i++ {
		if grid[0][i] || grid[3][i] || grid[i][0] || grid[i][3] {
			t.Fatal("静区不是空白")
		}
	}
}

// parseSVG 把 SVG 里的水平游程还原成网格。
func parseSVG(t *testing.T, svg string, size int) [][]bool {
	t.Helper()
	grid := make([][]bool, size)
	for i := range grid {
		grid[i] = make([]bool, size)
	}
	d := svg[strings.Index(svg, ` d="`)+4 : strings.Index(svg, `"/></svg>`)]
	for _, cmd := range strings.Split(d, "z") {
		cmd = strings.TrimPrefix(cmd, "M")
		if cmd == "" {
			continue
		}
		var x, y, w, neg int
		if n, err := fmt.Sscanf(cmd, "%d %dh%dv1h-%d", &x, &y, &w, &neg); err != nil || n != 4 {
			t.Fatalf("游程解析失败 %q: %v", cmd, err)
		}
		if neg != w {
			t.Fatalf("游程 %q 左右宽度不一致", cmd)
		}
		if x < 0 || y < 0 || y >= size || x+w > size {
			t.Fatalf("游程越界: %d,%d w=%d", x, y, w)
		}
		for i := 0; i < w; i++ {
			grid[y][x+i] = true
		}
	}
	return grid
}

func TestRejectsTooLong(t *testing.T) {
	if _, err := Encode(strings.Repeat("a", 400)); err != ErrTooLong {
		t.Fatalf("超长内容应返回 ErrTooLong，得到 %v", err)
	}
	// 213 字节能放进版本 10-M
	code, err := Encode(strings.Repeat("a", 180))
	if err != nil {
		t.Fatalf("180 字节应能编码: %v", err)
	}
	if code.Version > 10 {
		t.Fatalf("版本超范围: %d", code.Version)
	}
}

func TestEncodeWithMaskRejectsBadMask(t *testing.T) {
	if _, err := EncodeWithMask("x", 8); err == nil {
		t.Fatal("掩码 8 应被拒绝")
	}
}
