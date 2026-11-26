//go:build windows

package winclip

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"testing"
)

// buildDIB 拼一个最小可用的 CF_DIB。
func buildDIB(width, height int, bpp uint16, topDown bool, compression uint32, clrUsed uint32, palette []byte, masks []uint32, pixels []byte) []byte {
	var buf bytes.Buffer
	hdr := make([]byte, 40)
	le := binary.LittleEndian
	le.PutUint32(hdr[0:4], 40)
	le.PutUint32(hdr[4:8], uint32(int32(width)))
	h := int32(height)
	if topDown {
		h = -h
	}
	le.PutUint32(hdr[8:12], uint32(h))
	le.PutUint16(hdr[12:14], 1)
	le.PutUint16(hdr[14:16], bpp)
	le.PutUint32(hdr[16:20], compression)
	le.PutUint32(hdr[32:36], clrUsed)
	buf.Write(hdr)
	for _, m := range masks {
		var tmp [4]byte
		le.PutUint32(tmp[:], m)
		buf.Write(tmp[:])
	}
	buf.Write(palette)
	buf.Write(pixels)
	return buf.Bytes()
}

func TestDecodeDIB24BottomUp(t *testing.T) {
	// 2x2、24 位：每行 6 字节，需补齐到 4 的倍数（8 字节）；缓冲区第一行是图像底行。
	pix := []byte{
		0, 0, 255, 255, 0, 0, 0, 0, // 底行：红、蓝（BGR 存储）
		0, 255, 0, 255, 255, 255, 0, 0, // 顶行：绿、白
	}
	img, err := decodeDIB(buildDIB(2, 2, 24, false, 0, 0, nil, nil, pix))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := img.At(0, 0); got != (color.RGBA{R: 0, G: 255, B: 0, A: 255}) {
		t.Fatalf("左上角应为绿色，得到 %v", got)
	}
	if got := img.At(0, 1); got != (color.RGBA{R: 255, G: 0, B: 0, A: 255}) {
		t.Fatalf("左下角应为红色，得到 %v", got)
	}
	if got := img.At(1, 0); got != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("右上角应为白色，得到 %v", got)
	}
	if got := img.At(1, 1); got != (color.RGBA{R: 0, G: 0, B: 255, A: 255}) {
		t.Fatalf("右下角应为蓝色，得到 %v", got)
	}
}

func TestDecodeDIBRowPadding(t *testing.T) {
	// 宽度 1、24 位：每行 3 字节，需补齐到 4 字节。
	pix := []byte{
		10, 20, 30, 0,
		40, 50, 60, 0,
	}
	img, err := decodeDIB(buildDIB(1, 2, 24, false, 0, 0, nil, nil, pix))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := img.At(0, 0); got != (color.RGBA{R: 60, G: 50, B: 40, A: 255}) {
		t.Fatalf("顶行颜色错误: %v", got)
	}
	if got := img.At(0, 1); got != (color.RGBA{R: 30, G: 20, B: 10, A: 255}) {
		t.Fatalf("底行颜色错误: %v", got)
	}
}

func TestDecodeDIB32Alpha(t *testing.T) {
	// alpha 全 0 视为不透明（Windows 截图常见）。
	pix := []byte{
		1, 2, 3, 0,
		4, 5, 6, 0,
	}
	img, err := decodeDIB(buildDIB(2, 1, 32, false, 0, 0, nil, nil, pix))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := img.At(0, 0); got != (color.RGBA{R: 3, G: 2, B: 1, A: 255}) {
		t.Fatalf("alpha 全 0 应视为不透明，得到 %v", got)
	}

	// 有非 0 alpha 时保留 alpha。
	pix2 := []byte{
		1, 2, 3, 128,
		4, 5, 6, 0,
	}
	img2, err := decodeDIB(buildDIB(2, 1, 32, false, 0, 0, nil, nil, pix2))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := img2.At(0, 0); got != (color.RGBA{R: 3, G: 2, B: 1, A: 128}) {
		t.Fatalf("alpha 应保留，得到 %v", got)
	}
}

func TestDecodeDIBTopDown(t *testing.T) {
	pix := []byte{
		0, 255, 0, 0, // 第一行是图像顶行（24 位、宽 1，补齐 4 字节）
		0, 0, 255, 0,
	}
	img, err := decodeDIB(buildDIB(1, 2, 24, true, 0, 0, nil, nil, pix))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := img.At(0, 0); got != (color.RGBA{R: 0, G: 255, B: 0, A: 255}) {
		t.Fatalf("自上而下 DIB 顶行解析错误: %v", got)
	}
}

func TestDecodeDIBPalette8(t *testing.T) {
	palette := []byte{
		0, 0, 0, 0, // 0: 黑
		0, 0, 255, 0, // 1: 红
	}
	pix := []byte{1, 0, 0, 0} // 行填充到 4 字节
	img, err := decodeDIB(buildDIB(2, 1, 8, false, 0, 2, palette, nil, pix))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := img.At(0, 0); got != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("调色板 1 应为红色，得到 %v", got)
	}
	if got := img.At(1, 0); got != (color.RGBA{A: 255}) {
		t.Fatalf("调色板 0 应为黑色，得到 %v", got)
	}
}

func TestDecodeDIB16BitMasks(t *testing.T) {
	// 565：0xF800/0x07E0/0x001F
	pix := []byte{0x00, 0xF8, 0, 0} // 纯红（宽 1 的 16 位行补齐到 4 字节）
	raw := buildDIB(1, 1, 16, false, 3, 0, nil, []uint32{0xF800, 0x07E0, 0x001F}, pix)
	img, err := decodeDIB(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := img.At(0, 0); got != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("16 位 565 纯红解析错误: %v", got)
	}
}

func TestDecodeDIBTruncated(t *testing.T) {
	full := buildDIB(2, 2, 24, false, 0, 0, nil, nil, make([]byte, 16))
	if _, err := decodeDIB(full[:len(full)-1]); err == nil {
		t.Fatal("截断的 DIB 应报错")
	}
	if _, err := decodeDIB([]byte{1, 2, 3}); err == nil {
		t.Fatal("过短的 DIB 应报错")
	}
}

func TestDIBToPNG(t *testing.T) {
	pix := []byte{0, 0, 255, 0}
	out, err := DIBToPNG(buildDIB(1, 1, 24, false, 0, 0, nil, nil, pix))
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("PNG 解码失败: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Fatalf("尺寸错误: %v", b)
	}
	r, g, bl, _ := img.At(0, 0).RGBA()
	if r != 0xffff || g != 0 || bl != 0 {
		t.Fatalf("颜色错误: %d %d %d", r, g, bl)
	}
}
