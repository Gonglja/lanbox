package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinary 造一个带本平台魔数的假二进制（测试在 Linux 与 Windows 上都能跑）。
func fakeBinary(payload string) []byte {
	if runtime.GOOS == "windows" {
		return append([]byte{'M', 'Z', 0x90, 0x00}, []byte(payload)...)
	}
	return append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, []byte(payload)...)
}

func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// assetServer 提供一个固定的资产下载地址。
func assetServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/asset" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestDownloadOK(t *testing.T) {
	blob := fakeBinary(strings.Repeat("bin", 1000))
	ts := assetServer(t, blob)
	dir := t.TempDir()
	u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64"})

	var lastDone, lastTotal int64
	progress := func(done, total int64) { lastDone, lastTotal = done, total }

	rel := Release{Version: "1.2.4", AssetName: "lanbox-linux-amd64", AssetURL: ts.URL + "/asset",
		SHA256: shaOf(blob), Size: int64(len(blob))}
	path, err := u.Download(context.Background(), dir, rel, progress)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("临时文件不在目标目录: %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取下载结果: %v", err)
	}
	if !bytes.Equal(got, blob) {
		t.Fatal("下载内容不一致")
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm()&0o100 == 0 {
			t.Fatalf("缺少可执行位: %v %v", st.Mode(), err)
		}
	}
	if lastDone != int64(len(blob)) || lastTotal != int64(len(blob)) {
		t.Fatalf("进度回调 = %d/%d", lastDone, lastTotal)
	}
}

func TestDownloadFailures(t *testing.T) {
	blob := fakeBinary("好的二进制")
	ts := assetServer(t, blob)
	dir := t.TempDir()
	newWith := func(rel Release) *Updater {
		u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64"})
		return u
	}
	base := func() Release {
		return Release{Version: "1.2.4", AssetName: "lanbox-linux-amd64", AssetURL: ts.URL + "/asset",
			SHA256: shaOf(blob), Size: int64(len(blob))}
	}

	cases := []struct {
		name   string
		mutate func(*Release)
		want   string
		stage  string
	}{
		{"摘要不符", func(r *Release) { r.SHA256 = strings.Repeat("f", 64) }, "校验失败", "verify"},
		{"大小不符", func(r *Release) { r.Size = 1 }, "大小不符", "verify"},
		{"缺校验值", func(r *Release) { r.SHA256 = "" }, "拒绝下载", "verify"},
		{"缺下载地址", func(r *Release) { r.AssetURL = "" }, "缺少下载地址", "download"},
		{"HTTP 404", func(r *Release) { r.AssetURL = ts.URL + "/nope" }, "HTTP 404", "download"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rel := base()
			c.mutate(&rel)
			u := newWith(rel)
			_, err := u.Download(context.Background(), dir, rel, nil)
			if err == nil {
				t.Fatal("应该失败")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误 %q 不含 %q", err, c.want)
			}
			var ue *Error
			if !errors.As(err, &ue) || ue.Stage != c.stage {
				t.Fatalf("阶段 = %+v，期望 %s", err, c.stage)
			}
		})
	}

	// 失败后不能留下临时文件。
	if left, _ := filepath.Glob(filepath.Join(dir, tempPrefix+"*.tmp")); len(left) != 0 {
		t.Fatalf("残留临时文件: %v", left)
	}
}

func TestDownloadRejectsHTML(t *testing.T) {
	html := []byte("<html><body>请登录</body></html>")
	ts := assetServer(t, html)
	dir := t.TempDir()
	u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64"})
	rel := Release{Version: "1.2.4", AssetName: "lanbox-linux-amd64", AssetURL: ts.URL + "/asset", SHA256: shaOf(html)}
	_, err := u.Download(context.Background(), dir, rel, nil)
	if err == nil || !strings.Contains(err.Error(), "可执行文件") {
		t.Fatalf("期望魔数错误，得到 %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, tempPrefix+"*.tmp")); len(left) != 0 {
		t.Fatalf("残留临时文件: %v", left)
	}
}

func TestDownloadContextCancel(t *testing.T) {
	blob := fakeBinary("x")
	ts := assetServer(t, blob)
	u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rel := Release{Version: "1.2.4", AssetName: "lanbox-linux-amd64", AssetURL: ts.URL + "/asset", SHA256: shaOf(blob)}
	if _, err := u.Download(ctx, t.TempDir(), rel, nil); err == nil {
		t.Fatal("取消后应该失败")
	}
}

func TestTempName(t *testing.T) {
	if got := tempName("1.2.4"); got != ".lanbox-update-1.2.4.tmp" {
		t.Fatalf("tempName = %q", got)
	}
	if got := tempName("v1/../2"); strings.ContainsAny(got, "/\\") || got == "" {
		t.Fatalf("tempName 没清理路径字符: %q", got)
	}
	if got := tempName(""); got != tempPrefix+"new.tmp" {
		t.Fatalf("空版本号 tempName = %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:                    "512 B",
		1536:                   "1.5 KB",
		20 * 1024 * 1024:       "20.0 MB",
		3 * 1024 * 1024 * 1024: "3.0 GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Fatalf("humanBytes(%d)=%q，期望 %q", in, got, want)
		}
	}
	_ = fmt.Sprint()
}
