package updater

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// githubSource 是一个最小的 GitHub Release 静态路径假源。
func githubSource(t *testing.T, tag, sums string) (*httptest.Server, *string) {
	t.Helper()
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/releases/latest/download/SHA256SUMS":
			http.Redirect(w, r, "/releases/download/"+tag+"/SHA256SUMS", http.StatusFound)
		case "/releases/download/" + tag + "/SHA256SUMS":
			fmt.Fprint(w, sums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &auth
}

func newTestUpdater(t *testing.T, o Options) *Updater {
	t.Helper()
	u, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return u
}

func TestCheckGitHub(t *testing.T) {
	sums := testSum + "  lanbox-linux-amd64\n" + strings.Repeat("f", 64) + " *lanbox-cli-linux-amd64\n"
	ts, auth := githubSource(t, "v1.2.4", sums)

	u := newTestUpdater(t, Options{Current: "1.2.3", Asset: "lanbox-linux-amd64", BaseURL: ts.URL, Token: "tok"})
	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.Version != "1.2.4" || rel.Tag != "v1.2.4" {
		t.Fatalf("版本解析错误: %+v", rel)
	}
	if rel.SHA256 != testSum {
		t.Fatalf("sha256 = %q", rel.SHA256)
	}
	want := ts.URL + "/releases/download/v1.2.4/lanbox-linux-amd64"
	if rel.AssetURL != want {
		t.Fatalf("asset url = %q，期望 %q", rel.AssetURL, want)
	}
	if *auth != "Bearer tok" {
		t.Fatalf("Authorization = %q", *auth)
	}
	if u.Current() != "1.2.3" || u.Asset() != "lanbox-linux-amd64" {
		t.Fatalf("Current/Asset = %q/%q", u.Current(), u.Asset())
	}
}

func TestCheckUpToDate(t *testing.T) {
	ts, _ := githubSource(t, "v1.2.4", testSum+"  lanbox-linux-amd64\n")
	for _, cur := range []string{"1.2.4", "v1.2.4", "1.3.0", "2.0.0"} {
		u := newTestUpdater(t, Options{Current: cur, Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
		rel, err := u.Check(context.Background())
		if !errors.Is(err, ErrUpToDate) {
			t.Fatalf("current=%s 期望 ErrUpToDate，得到 %v", cur, err)
		}
		if rel.Version != "1.2.4" {
			t.Fatalf("ErrUpToDate 也要带回远端版本，得到 %+v", rel)
		}
	}
}

func TestCheckPrereleaseIsNewerForDev(t *testing.T) {
	ts, _ := githubSource(t, "v1.2.4-rc1", testSum+"  lanbox-linux-amd64\n")
	u := newTestUpdater(t, Options{Current: "dev", Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("dev 版本应该能看到预发布版: %v", err)
	}
	if rel.Version != "1.2.4-rc1" {
		t.Fatalf("version = %q", rel.Version)
	}
}

func TestCheckMissingAsset(t *testing.T) {
	ts, _ := githubSource(t, "v1.2.4", testSum+"  lanbox-linux-arm64\n")
	u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
	if _, err := u.Check(context.Background()); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("期望 ErrNoAsset，得到 %v", err)
	}
}

func TestCheckBadTag(t *testing.T) {
	ts, _ := githubSource(t, "nightly", testSum+"  lanbox-linux-amd64\n")
	u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
	_, err := u.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "无法解析") {
		t.Fatalf("期望版本解析错误，得到 %v", err)
	}
}

func TestCheckHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64", BaseURL: ts.URL})
	_, err := u.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("期望 HTTP 404，得到 %v", err)
	}
	var ue *Error
	if !errors.As(err, &ue) || ue.Stage != "check" {
		t.Fatalf("期望 Stage=check，得到 %v", err)
	}
}

func TestCheckManifest(t *testing.T) {
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/up/latest.json" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{
			"version": "v1.3.0",
			"published": "2026-11-27T10:00:00Z",
			"notes": "- 修好了什么",
			"assets": [
				{"name": "lanbox-linux-amd64", "url": "files/lanbox-linux-amd64", "size": 123, "sha256": %q},
				{"name": "lanbox-windows-amd64.exe", "url": "https://example.com/lanbox.exe", "sha256": %q}
			]
		}`, testSum, testSum)
	}))
	defer ts.Close()

	u := newTestUpdater(t, Options{
		Current:  "1.2.0",
		Asset:    "lanbox-linux-amd64",
		Manifest: ts.URL + "/up/latest.json",
		Token:    "tok",
	})
	rel, err := u.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.Version != "1.3.0" || rel.Tag != "v1.3.0" {
		t.Fatalf("版本 = %+v", rel)
	}
	if rel.Size != 123 || rel.SHA256 != testSum || rel.Notes == "" {
		t.Fatalf("清单字段没取到: %+v", rel)
	}
	if want := ts.URL + "/up/files/lanbox-linux-amd64"; rel.AssetURL != want {
		t.Fatalf("相对 URL 解析错误: %q，期望 %q", rel.AssetURL, want)
	}
	if auth != "Bearer tok" {
		t.Fatalf("Authorization = %q", auth)
	}
	if rel.Published.IsZero() {
		t.Fatal("published 没解析")
	}
}

func TestCheckManifestErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // 期望的错误子串；空表示 ErrNoAsset
	}{
		{"不是 JSON", "<html>404</html>", "合法 JSON"},
		{"缺 version", `{"assets":[]}`, "缺少 version"},
		{"缺 sha256", `{"version":"1.3.0","assets":[{"name":"lanbox-linux-amd64","url":"x"}]}`, "sha256"},
		{"缺 url", `{"version":"1.3.0","assets":[{"name":"lanbox-linux-amd64","sha256":"` + testSum + `"}]}`, "缺少 url"},
		{"缺资产", `{"version":"1.3.0","assets":[]}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, c.body)
			}))
			defer ts.Close()
			u := newTestUpdater(t, Options{Current: "1.0.0", Asset: "lanbox-linux-amd64", Manifest: ts.URL + "/latest.json"})
			_, err := u.Check(context.Background())
			if err == nil {
				t.Fatal("应该报错")
			}
			if c.want == "" {
				if !errors.Is(err, ErrNoAsset) {
					t.Fatalf("期望 ErrNoAsset，得到 %v", err)
				}
				return
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误 %q 不含 %q", err, c.want)
			}
		})
	}
}

func TestParseSumsFormats(t *testing.T) {
	sums := strings.Join([]string{
		"",
		"# 注释",
		testSum + "  lanbox-linux-amd64",
		testSum + " *lanbox-windows-amd64.exe",
		"short  lanbox-linux-386",
	}, "\n")
	if got, ok := parseSums([]byte(sums), "lanbox-windows-amd64.exe"); !ok || got != testSum {
		t.Fatalf("带星号的行没解析: %q %v", got, ok)
	}
	if _, ok := parseSums([]byte(sums), "lanbox-linux-386"); ok {
		t.Fatal("非法 sha256 不该被接受")
	}
	if _, ok := parseSums([]byte(sums), "不存在"); ok {
		t.Fatal("不存在的文件不该匹配")
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct{ product, goos, goarch, want string }{
		{"lanbox", "linux", "amd64", "lanbox-linux-amd64"},
		{"lanbox", "linux", "arm", "lanbox-linux-armv7"},
		{"lanbox", "linux", "arm64", "lanbox-linux-arm64"},
		{"lanbox", "windows", "amd64", "lanbox-windows-amd64.exe"},
		{"lanbox-agent", "windows", "arm64", "lanbox-agent-windows-arm64.exe"},
	}
	for _, c := range cases {
		if got := AssetName(c.product, c.goos, c.goarch); got != c.want {
			t.Fatalf("AssetName(%q,%q,%q)=%q，期望 %q", c.product, c.goos, c.goarch, got, c.want)
		}
	}
}

func TestNewRequiresAsset(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("缺少 asset 应该报错")
	}
}
