//go:build windows

package winclip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// DIBToPNG 把剪贴板里的 CF_DIB 数据（BITMAPINFO 结构 + 像素）编码成 PNG。
func DIBToPNG(raw []byte) ([]byte, error) {
	img, err := decodeDIB(raw)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("PNG 编码失败: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeDIB 解析 CF_DIB（不含 BITMAPFILEHEADER）。
func decodeDIB(raw []byte) (*image.RGBA, error) {
	if len(raw) < 40 {
		return nil, errors.New("DIB 数据不完整")
	}
	le := binary.LittleEndian
	biSize := int(le.Uint32(raw[0:4]))
	if biSize < 40 || biSize > len(raw) {
		return nil, errors.New("DIB 头大小非法")
	}
	width := int(int32(le.Uint32(raw[4:8])))
	rawHeight := int(int32(le.Uint32(raw[8:12])))
	bitCount := int(le.Uint16(raw[14:16]))
	compression := le.Uint32(raw[16:20])
	clrUsed := int(le.Uint32(raw[32:36]))

	if width <= 0 || width > 20000 {
		return nil, fmt.Errorf("DIB 宽度非法: %d", width)
	}
	height := rawHeight
	topDown := false
	if height < 0 {
		topDown = true
		height = -height
	}
	if height <= 0 || height > 20000 {
		return nil, fmt.Errorf("DIB 高度非法: %d", rawHeight)
	}
	switch compression {
	case 0: // BI_RGB
	case 3: // BI_BITFIELDS
		if bitCount != 16 && bitCount != 32 {
			return nil, fmt.Errorf("不支持的位域格式: %d 位", bitCount)
		}
	default:
		return nil, fmt.Errorf("不支持的压缩方式: %d", compression)
	}
	switch bitCount {
	case 1, 4, 8, 16, 24, 32:
	default:
		return nil, fmt.Errorf("不支持的位深: %d", bitCount)
	}

	// 位域掩码：头里自带（biSize >= 52）或紧跟 40 字节头之后。
	var rMask, gMask, bMask, aMask uint32
	offset := biSize
	if compression == 3 {
		if biSize >= 52 {
			rMask = le.Uint32(raw[40:44])
			gMask = le.Uint32(raw[44:48])
			bMask = le.Uint32(raw[48:52])
			if biSize >= 56 {
				aMask = le.Uint32(raw[52:56])
			}
		} else {
			if len(raw) < 40+12 {
				return nil, errors.New("DIB 位域掩码缺失")
			}
			rMask = le.Uint32(raw[40:44])
			gMask = le.Uint32(raw[44:48])
			bMask = le.Uint32(raw[48:52])
			offset += 12
		}
	}

	// 调色板。
	var palette color.Palette
	if bitCount <= 8 {
		n := clrUsed
		if n == 0 {
			n = 1 << bitCount
		}
		if n < 0 || offset+n*4 > len(raw) {
			return nil, errors.New("DIB 调色板不完整")
		}
		palette = make(color.Palette, n)
		for i := 0; i < n; i++ {
			p := raw[offset+i*4:]
			palette[i] = color.RGBA{R: p[2], G: p[1], B: p[0], A: 255}
		}
		offset += n * 4
	}

	stride := ((width*bitCount + 31) / 32) * 4
	need := offset + stride*height
	if need > len(raw) {
		return nil, fmt.Errorf("DIB 像素数据不完整: 需要 %d 字节，只有 %d", need, len(raw))
	}

	// 32 位 DIB 的 alpha 通道经常全为 0（Windows 截图），此时视为不透明。
	opaque32 := true
	if bitCount == 32 {
		for y := 0; y < height && opaque32; y++ {
			row := raw[offset+y*stride:]
			for x := 0; x < width; x++ {
				if row[x*4+3] != 0 {
					opaque32 = false
					break
				}
			}
		}
	}

	r5 := newMask(rMask)
	g5 := newMask(gMask)
	b5 := newMask(bMask)
	a5 := newMask(aMask)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcRow := y
		if !topDown {
			srcRow = height - 1 - y
		}
		row := raw[offset+srcRow*stride:]
		dst := img.Pix[y*img.Stride:]
		switch bitCount {
		case 32:
			for x := 0; x < width; x++ {
				p := row[x*4:]
				a := uint8(255)
				if !opaque32 {
					a = p[3]
				}
				dst[x*4+0] = p[2]
				dst[x*4+1] = p[1]
				dst[x*4+2] = p[0]
				dst[x*4+3] = a
			}
		case 24:
			for x := 0; x < width; x++ {
				p := row[x*3:]
				dst[x*4+0] = p[2]
				dst[x*4+1] = p[1]
				dst[x*4+2] = p[0]
				dst[x*4+3] = 255
			}
		case 16:
			for x := 0; x < width; x++ {
				v := uint32(binary.LittleEndian.Uint16(row[x*2:]))
				var r, g, b, a uint8
				switch {
				case rMask != 0 || gMask != 0 || bMask != 0:
					r, g, b = r5.get(v), g5.get(v), b5.get(v)
					if aMask != 0 {
						a = a5.get(v)
					} else {
						a = 255
					}
				default: // 默认 5-5-5
					r = uint8((v >> 10) & 0x1f)
					g = uint8((v >> 5) & 0x1f)
					b = uint8(v & 0x1f)
					r, g, b = r<<3|r>>2, g<<3|g>>2, b<<3|b>>2
					a = 255
				}
				dst[x*4+0], dst[x*4+1], dst[x*4+2], dst[x*4+3] = r, g, b, a
			}
		case 8:
			for x := 0; x < width; x++ {
				img.Set(x, y, paletteAt(palette, int(row[x])))
			}
		case 4:
			for x := 0; x < width; x++ {
				b := row[x/2]
				idx := int(b >> 4)
				if x%2 == 1 {
					idx = int(b & 0x0f)
				}
				img.Set(x, y, paletteAt(palette, idx))
			}
		case 1:
			for x := 0; x < width; x++ {
				idx := int((row[x/8] >> (7 - uint(x%8))) & 1)
				img.Set(x, y, paletteAt(palette, idx))
			}
		}
	}
	return img, nil
}

func paletteAt(p color.Palette, i int) color.Color {
	if i >= 0 && i < len(p) {
		return p[i]
	}
	return color.RGBA{A: 255}
}

// mask 描述一个位域（如 0xF800 表示红色占高 5 位）。
type mask struct {
	shift uint
	bits  uint
	max   uint32
}

func newMask(m uint32) mask {
	if m == 0 {
		return mask{}
	}
	shift := uint(0)
	for m&1 == 0 {
		m >>= 1
		shift++
	}
	bits := uint(0)
	for m&1 == 1 {
		m >>= 1
		bits++
	}
	max := uint32(1)<<bits - 1
	if bits >= 32 {
		max = ^uint32(0)
	}
	return mask{shift: shift, bits: bits, max: max}
}

func (m mask) get(v uint32) uint8 {
	if m.bits == 0 {
		return 0
	}
	raw := (v >> m.shift) & m.max
	return uint8(raw * 255 / m.max)
}
