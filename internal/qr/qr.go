// Package qr 是零依赖的 QR Code 编码器（字节模式、纠错等级 M、版本 1–10）。
//
// 只做 lanbox 需要的事：把一条连接地址（最多 213 字节）编成二维码。
// 输出是矩阵/SVG 字符串，因此不需要引入任何第三方库，也不必写 PNG。
//
// 实现遵循 ISO/IEC 18004：字节模式段、Reed–Solomon 纠错（GF(256)）、
// 8 种掩码 + 罚分选优、格式信息与（版本 ≥7 的）版本信息。
// 正确性由 testdata/golden.json 保证：那份数据由 Python 的 qrcode 库
// 在相同版本/纠错等级/掩码下生成，逐个模块比对。
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// ErrTooLong 表示内容超出支持的容量（版本 10 + 纠错等级 M ≈ 213 字节）。
var ErrTooLong = errors.New("qr: 内容超出容量（最多 213 字节）")

// Code 是编码结果。
type Code struct {
	Version int
	Mask    int
	Size    int
	Modules [][]bool // Modules[y][x]，true 表示深色模块
}

// Encode 选择最小可用版本与最优掩码。
func Encode(text string) (*Code, error) {
	return encode(text, -1)
}

// EncodeWithMask 强制使用指定掩码（供测试与固定输出使用）。
func EncodeWithMask(text string, mask int) (*Code, error) {
	if mask < 0 || mask > 7 {
		return nil, fmt.Errorf("qr: 掩码必须是 0–7，得到 %d", mask)
	}
	return encode(text, mask)
}

const (
	maxVersion = 10
	eccLevelM  = 0 // 格式信息里的纠错等级两位：M = 00
)

// 纠错等级 M 下每个版本的块参数（下标 0 不用）。
var (
	eccPerBlock = [maxVersion + 1]int{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26}
	blocksCount = [maxVersion + 1]int{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5}
)

// dataCodewords 返回某版本在 M 等级下的数据码字总数。
func dataCodewords(ver int) int {
	raw := rawDataModules(ver)/8 - eccPerBlock[ver]*blocksCount[ver]
	return raw
}

// rawDataModules 返回可用于数据与纠错的总模块数（不含功能图形）。
func rawDataModules(ver int) int {
	res := (16*ver+128)*ver + 64
	if ver >= 2 {
		na := ver/7 + 2
		res -= (25*na-10)*na - 55
		if ver >= 7 {
			res -= 36
		}
	}
	return res
}

func encode(text string, forcedMask int) (*Code, error) {
	data := []byte(text)
	ver := 0
	for v := 1; v <= maxVersion; v++ {
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		if 4+countBits+8*len(data) <= dataCodewords(v)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, ErrTooLong
	}

	codewords := buildCodewords(data, ver)
	all := addECCAndInterleave(codewords, ver)

	m := newMatrix(ver)
	m.drawFunctionPatterns()
	m.placeData(all)

	mask := forcedMask
	if mask < 0 {
		mask = m.bestMask()
	} else {
		m.applyMask(mask)
	}
	m.drawFormatBits(mask)
	if ver >= 7 {
		m.drawVersionBits()
	}
	return &Code{Version: ver, Mask: mask, Size: m.size, Modules: m.modules}, nil
}

// ---------- 数据段 ----------

type bitBuffer struct {
	bits []bool
}

func (b *bitBuffer) append(val uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		b.bits = append(b.bits, val>>uint(i)&1 != 0)
	}
}

func (b *bitBuffer) len() int { return len(b.bits) }

// bytes 把位流按 8 位对齐打包。
func (b *bitBuffer) bytes() []byte {
	out := make([]byte, (len(b.bits)+7)/8)
	for i, bit := range b.bits {
		if bit {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	return out
}

// buildCodewords 生成「模式 + 长度 + 数据 + 终止符 + 填充」后的数据码字。
func buildCodewords(data []byte, ver int) []byte {
	capacity := dataCodewords(ver) * 8
	var bb bitBuffer
	bb.append(0b0100, 4) // 字节模式
	countBits := 8
	if ver >= 10 {
		countBits = 16
	}
	bb.append(uint32(len(data)), countBits)
	for _, c := range data {
		bb.append(uint32(c), 8)
	}
	if rest := capacity - bb.len(); rest > 0 {
		term := 4
		if rest < term {
			term = rest
		}
		bb.append(0, term)
	}
	for bb.len()%8 != 0 {
		bb.append(0, 1)
	}
	pad := [2]byte{0xEC, 0x11}
	for i := 0; bb.len() < capacity; i++ {
		bb.append(uint32(pad[i%2]), 8)
	}
	return bb.bytes()
}

// addECCAndInterleave 按块切分、逐块计算纠错码字，再按标准交织。
//
// 块结构不写死表：由总码字数与块数推出「短块数据码字数」和「短块个数」，
// 与标准表（如 8-M = 2×38 + 2×39）等价，避免抄表出错。
func addECCAndInterleave(data []byte, ver int) []byte {
	total := rawDataModules(ver) / 8
	numBlocks := blocksCount[ver]
	eccLen := eccPerBlock[ver]
	numShort := numBlocks - total%numBlocks
	shortTotal := total / numBlocks // 短块总码字数（含纠错）
	dataShort := shortTotal - eccLen
	divisor := rsDivisor(eccLen)

	blocks := make([][]byte, numBlocks)
	used := 0
	for i := 0; i < numBlocks; i++ {
		n := dataShort
		if i >= numShort {
			n++
		}
		chunk := data[used : used+n]
		used += n
		blk := make([]byte, 0, shortTotal+1)
		blk = append(blk, chunk...)
		if i < numShort {
			blk = append(blk, 0) // 占位字节，交织时跳过
		}
		blocks[i] = append(blk, rsRemainder(chunk, divisor)...)
	}

	out := make([]byte, 0, total)
	for i := 0; i <= shortTotal; i++ {
		for j := 0; j < numBlocks; j++ {
			if i == dataShort && j < numShort {
				continue
			}
			out = append(out, blocks[j][i])
		}
	}
	return out
}

// ---------- GF(256) 与 Reed–Solomon ----------

var (
	gfExp [512]byte
	gfLog [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[byte(x)] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsDivisor 返回 (x-α^0)…(x-α^(n-1)) 的系数（去掉首项 1）。
func rsDivisor(n int) []byte {
	p := []byte{1}
	for i := 0; i < n; i++ {
		root := gfExp[i]
		next := make([]byte, len(p)+1)
		for k := 0; k < len(p); k++ {
			next[k] ^= p[k]                // × x
			next[k+1] ^= gfMul(p[k], root) // × α^i
		}
		p = next
	}
	return p[1:]
}

// rsRemainder 计算数据除以生成多项式的余式（即纠错码字）。
func rsRemainder(data, divisor []byte) []byte {
	res := make([]byte, len(divisor))
	for _, b := range data {
		factor := b ^ res[0]
		copy(res, res[1:])
		res[len(res)-1] = 0
		if factor != 0 {
			for i, g := range divisor {
				res[i] ^= gfMul(g, factor)
			}
		}
	}
	return res
}

// ---------- 矩阵 ----------

type matrix struct {
	size      int
	version   int
	modules   [][]bool
	function  [][]bool
	alignment []int
}

func newMatrix(ver int) *matrix {
	size := ver*4 + 17
	m := &matrix{size: size, version: ver}
	m.modules = make([][]bool, size)
	m.function = make([][]bool, size)
	for i := range m.modules {
		m.modules[i] = make([]bool, size)
		m.function[i] = make([]bool, size)
	}
	m.alignment = alignmentPositions(ver)
	return m
}

// alignmentPositions 返回校正图形中心坐标（版本 1 没有校正图形）。
func alignmentPositions(ver int) []int {
	if ver == 1 {
		return nil
	}
	n := ver/7 + 2
	step := 26
	if ver != 32 {
		step = (ver*4 + n*2 + 1) / (n*2 - 2) * 2
	}
	res := make([]int, n)
	for i := 0; i < n; i++ {
		res[i] = ver*4 + 10 - step*(n-1-i)
	}
	return res
}

func (m *matrix) setFunction(x, y int, dark bool) {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return
	}
	m.modules[y][x] = dark
	m.function[y][x] = true
}

// drawFunctionPatterns 画定位/定时/校正图形，并预留格式信息与版本信息区域。
func (m *matrix) drawFunctionPatterns() {
	// 定时图形
	for i := 0; i < m.size; i++ {
		m.setFunction(6, i, i%2 == 0)
		m.setFunction(i, 6, i%2 == 0)
	}
	// 三个定位图形（含分隔符）
	m.drawFinder(3, 3)
	m.drawFinder(m.size-4, 3)
	m.drawFinder(3, m.size-4)
	// 校正图形
	for _, y := range m.alignment {
		for _, x := range m.alignment {
			if (x == 6 && y == 6) || (x == 6 && y == m.size-7) || (x == m.size-7 && y == 6) {
				continue
			}
			m.drawAlignment(x, y)
		}
	}
	// 预留格式信息（先按亮色占位，稍后写入真实比特）
	m.drawFormatBits(0)
	// 预留版本信息
	if m.version >= 7 {
		m.drawVersionBits()
	}
}

func (m *matrix) drawFinder(x, y int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			dist := max(abs(dx), abs(dy))
			m.setFunction(x+dx, y+dy, dist != 2 && dist != 4)
		}
	}
}

func (m *matrix) drawAlignment(x, y int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			m.setFunction(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

// placeData 按之字形从右下角开始铺数据位；功能模块跳过。
func (m *matrix) placeData(data []byte) {
	i := 0
	right := m.size - 1
	for right >= 1 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < m.size; vert++ {
			upward := ((right + 1) & 2) == 0
			y := vert
			if upward {
				y = m.size - 1 - vert
			}
			for j := 0; j < 2; j++ {
				x := right - j
				if m.function[y][x] {
					continue
				}
				if i < len(data)*8 {
					m.modules[y][x] = data[i>>3]>>uint(7-i&7)&1 != 0
					i++
				}
			}
		}
		right -= 2
	}
}

// maskBit 返回该掩码下模块是否需要取反（x 列、y 行）。
func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	case 7:
		return ((x+y)%2+x*y%3)%2 == 0
	}
	return false
}

// applyMask 只对非功能模块取反。
func (m *matrix) applyMask(mask int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if !m.function[y][x] && maskBit(mask, x, y) {
				m.modules[y][x] = !m.modules[y][x]
			}
		}
	}
}

// bestMask 依次试 8 种掩码，取罚分最低者。
func (m *matrix) bestMask() int {
	best, bestScore := -1, 0
	for mask := 0; mask < 8; mask++ {
		m.applyMask(mask)
		m.drawFormatBits(mask)
		score := m.penalty()
		if best < 0 || score < bestScore {
			best, bestScore = mask, score
		}
		m.applyMask(mask) // 异或两次即还原
	}
	m.applyMask(best)
	m.drawFormatBits(best)
	return best
}

func (m *matrix) drawFormatBits(mask int) {
	data := eccLevelM<<3 | mask
	rem := data << 10
	for i := 14; i >= 10; i-- {
		if rem&(1<<uint(i)) != 0 {
			rem ^= 0x537 << uint(i-10)
		}
	}
	bits := (data<<10 | rem) ^ 0x5412

	bit := func(i int) bool { return bits>>uint(i)&1 != 0 }

	// 第一份：左上角
	for i := 0; i <= 5; i++ {
		m.setFunction(8, i, bit(i))
	}
	m.setFunction(8, 7, bit(6))
	m.setFunction(8, 8, bit(7))
	m.setFunction(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		m.setFunction(14-i, 8, bit(i))
	}
	// 第二份：左下 + 右上
	for i := 0; i < 8; i++ {
		m.setFunction(m.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.setFunction(8, m.size-15+i, bit(i))
	}
	// 固定的深色模块
	m.setFunction(8, m.size-8, true)
}

func (m *matrix) drawVersionBits() {
	rem := m.version << 12
	for i := 17; i >= 12; i-- {
		if rem&(1<<uint(i)) != 0 {
			rem ^= 0x1F25 << uint(i-12)
		}
	}
	bits := m.version<<12 | rem
	for i := 0; i < 18; i++ {
		dark := bits>>uint(i)&1 != 0
		a := m.size - 11 + i%3
		b := i / 3
		m.setFunction(a, b, dark)
		m.setFunction(b, a, dark)
	}
}

// penalty 按标准的四条规则打分（越小越好）。
func (m *matrix) penalty() int {
	total := 0

	// 规则 1：行/列上连续同色 ≥5 个
	for y := 0; y < m.size; y++ {
		total += runPenalty(func(x int) bool { return m.modules[y][x] }, m.size)
	}
	for x := 0; x < m.size; x++ {
		total += runPenalty(func(y int) bool { return m.modules[y][x] }, m.size)
	}

	// 规则 2：2×2 同色块
	for y := 0; y < m.size-1; y++ {
		for x := 0; x < m.size-1; x++ {
			c := m.modules[y][x]
			if c == m.modules[y][x+1] && c == m.modules[y+1][x] && c == m.modules[y+1][x+1] {
				total += 3
			}
		}
	}

	// 规则 3：1:1:3:1:1 的「定位图形」序列，左右任一侧有 4 个亮模块即算一次。
	// 符号外部的静区按亮色计入，所以两侧各补 4 个亮模块后统一处理边界。
	seq := make([]bool, m.size+8)
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			seq[x+4] = m.modules[y][x]
		}
		total += 40 * finderLikePatterns(seq)
	}
	for x := 0; x < m.size; x++ {
		for y := 0; y < m.size; y++ {
			seq[y+4] = m.modules[y][x]
		}
		total += 40 * finderLikePatterns(seq)
	}

	// 规则 4：深色比例偏离 50%
	dark := 0
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if m.modules[y][x] {
				dark++
			}
		}
	}
	totalModules := m.size * m.size
	k := abs(dark*20-totalModules*10) / totalModules
	total += k * 10
	return total
}

func runPenalty(at func(int) bool, n int) int {
	total, run := 0, 1
	for i := 1; i < n; i++ {
		if at(i) == at(i-1) {
			run++
			continue
		}
		if run >= 5 {
			total += 3 + (run - 5)
		}
		run = 1
	}
	if run >= 5 {
		total += 3 + (run - 5)
	}
	return total
}

// finderLikePatterns 统计一行/一列里「像定位图形」的序列个数（每条只算一次）。
func finderLikePatterns(seq []bool) int {
	core := [...]bool{true, false, true, true, true, false, true}
	n := 0
	for i := 0; i+len(core) <= len(seq); i++ {
		match := true
		for j, want := range core {
			if seq[i+j] != want {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if lightRun(seq, i-4, i) || lightRun(seq, i+len(core), i+len(core)+4) {
			n++
		}
	}
	return n
}

// lightRun 判断 [from,to) 区间是否全是亮模块（越界部分视为亮）。
func lightRun(seq []bool, from, to int) bool {
	for i := from; i < to; i++ {
		if i >= 0 && i < len(seq) && seq[i] {
			return false
		}
	}
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------- 输出 ----------

// SVG 生成带静区（quietZone 个模块）的矢量二维码。
// 用 1 个单位的模块网格 + shape-rendering=crispEdges，缩放不失真。
func (c *Code) SVG(quietZone int) string {
	if quietZone < 0 {
		quietZone = 4
	}
	size := c.Size + quietZone*2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="100%%" height="100%%" shape-rendering="crispEdges" role="img" aria-label="QR">`, size, size)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/>`, size, size)
	b.WriteString(`<path fill="#000" d="`)
	for y := 0; y < c.Size; y++ {
		x := 0
		for x < c.Size {
			if !c.Modules[y][x] {
				x++
				continue
			}
			run := 1
			for x+run < c.Size && c.Modules[y][x+run] {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x+quietZone, y+quietZone, run, run)
			x += run
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}
