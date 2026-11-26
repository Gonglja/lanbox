//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"sync"

	"fyne.io/systray"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	iconGreen  = makeTrayIcon(color.RGBA{R: 34, G: 197, B: 94, A: 255})
	iconYellow = makeTrayIcon(color.RGBA{R: 234, G: 179, B: 8, A: 255})
	iconRed    = makeTrayIcon(color.RGBA{R: 239, G: 68, B: 68, A: 255})
)

// tray 是托盘图标与菜单。
type tray struct {
	app *App

	openCh   chan struct{}
	pauseCh  chan struct{}
	uploadCh chan struct{}
	quitCh   chan struct{}

	mu        sync.Mutex
	pauseItem *systray.MenuItem
}

// startTray 启动托盘（Windows 上 systray.Run 会阻塞，放到独立 goroutine）。
func startTray(app *App) *tray {
	t := &tray{
		app:      app,
		openCh:   make(chan struct{}, 1),
		pauseCh:  make(chan struct{}, 1),
		uploadCh: make(chan struct{}, 1),
		quitCh:   make(chan struct{}, 1),
	}
	go systray.Run(t.onReady, func() {})
	go t.loop()
	return t
}

func (t *tray) onReady() {
	systray.SetIcon(iconRed)
	systray.SetTooltip("lanbox：未登录")

	open := systray.AddMenuItem("打开窗口", "显示主窗口")
	t.mu.Lock()
	t.pauseItem = systray.AddMenuItem("暂停自动上传", "暂停/恢复剪贴板自动上传")
	t.mu.Unlock()
	upload := systray.AddMenuItem("立即上传剪贴板", "把当前剪贴板内容上传到服务器")
	systray.AddSeparator()
	quit := systray.AddMenuItem("退出", "退出 lanbox 客户端")

	forward := func(m *systray.MenuItem, ch chan struct{}) {
		go func() {
			for range m.ClickedCh {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}()
	}
	forward(open, t.openCh)
	t.mu.Lock()
	pauseItem := t.pauseItem
	t.mu.Unlock()
	forward(pauseItem, t.pauseCh)
	forward(upload, t.uploadCh)
	forward(quit, t.quitCh)
}

func (t *tray) loop() {
	for {
		select {
		case <-t.openCh:
			t.app.showWindow()
		case <-t.pauseCh:
			st := t.app.GetState()
			t.app.Pause(!st.Paused)
		case <-t.uploadCh:
			if err := t.app.UploadClipboardNow(); err != nil {
				t.app.setLastErr(err.Error())
			}
		case <-t.quitCh:
			t.app.quit()
			return
		}
	}
}

// update 刷新图标、提示与暂停菜单文案。
func (t *tray) update(st UIState) {
	icon := iconGreen
	tip := "lanbox · 监听中"
	switch {
	case st.ReconnectStopped:
		icon = iconRed
		tip = "lanbox：连接已中断"
	case st.Reconnecting:
		icon = iconYellow
		tip = "lanbox · 正在重连"
	case !st.LoggedIn:
		icon = iconRed
		tip = "lanbox：未登录"
	case st.Paused:
		icon = iconYellow
		tip = "lanbox · 已暂停"
	case !st.Enabled:
		icon = iconYellow
		tip = "lanbox · 监听已关闭"
	case st.Active > 0:
		tip = "lanbox · 正在上传"
	}
	if st.LastErr != "" {
		tip += "\n" + st.LastErr
	}
	systray.SetIcon(icon)
	systray.SetTooltip(tip)

	t.mu.Lock()
	item := t.pauseItem
	t.mu.Unlock()
	if item != nil {
		if st.Paused {
			item.SetTitle("恢复自动上传")
		} else {
			item.SetTitle("暂停自动上传")
		}
	}
}

// showWindow 从托盘恢复窗口。
func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	wailsruntime.WindowShow(a.ctx)
	wailsruntime.WindowUnminimise(a.ctx)
	wailsruntime.WindowSetAlwaysOnTop(a.ctx, true)
	wailsruntime.WindowSetAlwaysOnTop(a.ctx, false)
}

// quit 真正退出：停止监听与上传，然后关闭窗口。
func (a *App) quit() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	systray.Quit()
	if a.ctx != nil {
		wailsruntime.Quit(a.ctx)
	}
}

// makeTrayIcon 生成 16×16 的 ICO（实心圆点），避免携带二进制资源。
func makeTrayIcon(c color.RGBA) []byte {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy, r := 7.5, 7.5, 6.2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			d := dx*dx + dy*dy
			switch {
			case d <= (r-1)*(r-1):
				img.Set(x, y, c)
			case d <= r*r:
				// 边缘做一档抗锯齿
				img.Set(x, y, color.RGBA{R: c.R, G: c.G, B: c.B, A: 160})
			}
		}
	}
	xorSize := size * size * 4
	andSize := ((size + 31) / 32) * 4 * size
	imageSize := 40 + xorSize + andSize

	var buf bytes.Buffer
	// ICONDIR
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	// ICONDIRENTRY
	buf.WriteByte(size)
	buf.WriteByte(size)
	buf.WriteByte(0)
	buf.WriteByte(0)
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(imageSize))
	binary.Write(&buf, binary.LittleEndian, uint32(6+16))
	// BITMAPINFOHEADER（高度写两倍：XOR + AND 蒙版）
	binary.Write(&buf, binary.LittleEndian, uint32(40))
	binary.Write(&buf, binary.LittleEndian, int32(size))
	binary.Write(&buf, binary.LittleEndian, int32(size*2))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(xorSize))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	// 像素数据自底向上，BGRA
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			r8, g8, b8, a8 := img.At(x, y).RGBA()
			buf.WriteByte(byte(b8 >> 8))
			buf.WriteByte(byte(g8 >> 8))
			buf.WriteByte(byte(r8 >> 8))
			buf.WriteByte(byte(a8 >> 8))
		}
	}
	// AND 蒙版：全 0 表示按 alpha 显示
	buf.Write(make([]byte, andSize))
	return buf.Bytes()
}
