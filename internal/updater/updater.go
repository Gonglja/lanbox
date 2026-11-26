package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 默认更新源。
const DefaultBaseURL = "https://github.com/Gonglja/lanbox"

// 检查与清单请求的超时。
const checkTimeout = 15 * time.Second

var (
	// ErrUpToDate 表示远端版本不比本机新（不算失败）。
	ErrUpToDate = errors.New("已是最新版本")
	// ErrNoAsset 表示该版本没有当前平台的产物。
	ErrNoAsset = errors.New("该版本没有本平台产物")
)

// Options 是构造 Updater 的输入。
type Options struct {
	Current  string       // 本机版本，与 -X main.version 同值
	Asset    string       // 当前平台产物名，如 lanbox-linux-amd64
	BaseURL  string       // github 模式基地址；空则用 DefaultBaseURL
	Manifest string       // 非空则走 manifest 模式（latest.json 地址）
	Token    string       // 私有仓库用，作为 Bearer token
	Client   *http.Client // 可为 nil（默认带代理与 15s 超时）
	Log      *log.Logger  // 可为 nil
}

// Release 是一次检查得到的远端版本信息。
type Release struct {
	Version   string    `json:"version"` // 规范化后，无 v 前缀
	Tag       string    `json:"tag"`     // 原始 tag，如 v1.2.4
	Notes     string    `json:"notes"`   // github 模式为空
	Published time.Time `json:"published"`
	AssetName string    `json:"asset_name"`
	AssetURL  string    `json:"asset_url"`
	Size      int64     `json:"size"` // 未知为 0
	SHA256    string    `json:"sha256"`
}

// Error 标出失败发生在哪个阶段，方便 HTTP 层翻译成人话。
type Error struct {
	Stage string // check / download / verify / replace / permission
	Err   error
}

func (e *Error) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func failf(stage, format string, args ...any) error {
	return &Error{Stage: stage, Err: fmt.Errorf(format, args...)}
}

// Updater 是更新源客户端（不保存状态；缓存由调用方维护）。
type Updater struct {
	opts Options
	cur  Version
	http *http.Client
}

// New 校验参数并返回 Updater。
func New(o Options) (*Updater, error) {
	if o.Asset == "" {
		return nil, errors.New("updater: 未指定 asset 名")
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.Client == nil {
		o.Client = &http.Client{Timeout: checkTimeout, Transport: transport()}
	}
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", log.LstdFlags)
	}
	return &Updater{opts: o, cur: parseLocal(o.Current), http: o.Client}, nil
}

// Current 返回本机版本（规范化形式）。
func (u *Updater) Current() string { return u.cur.String() }

// Asset 返回当前平台的产物名。
func (u *Updater) Asset() string { return u.opts.Asset }

// AssetName 按 GOOS/GOARCH 推出产物名（服务端与 agent 共用）。
func AssetName(product, goos, goarch string) string {
	arch := goarch
	if arch == "arm" {
		arch = "armv7"
	}
	name := product + "-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Check 查询远端最新版本。远端不比本机新时返回 (Release, ErrUpToDate)。
func (u *Updater) Check(ctx context.Context) (Release, error) {
	var (
		rel Release
		err error
	)
	if u.opts.Manifest != "" {
		rel, err = u.checkManifest(ctx)
	} else {
		rel, err = u.checkGitHub(ctx)
	}
	if err != nil {
		return rel, err
	}
	latest, perr := parseVersion(rel.Version)
	if perr != nil {
		return rel, failf("check", "远端版本号 %q 无法解析: %w", rel.Version, perr)
	}
	if compareVersion(latest, u.cur) <= 0 {
		return rel, ErrUpToDate
	}
	return rel, nil
}

// checkGitHub 走静态下载路径：{base}/releases/latest/download/SHA256SUMS
// 302 到 {base}/releases/download/<tag>/SHA256SUMS，从最终 URL 里拿 tag（零 API 调用）。
func (u *Updater) checkGitHub(ctx context.Context) (Release, error) {
	sumsURL := u.opts.BaseURL + "/releases/latest/download/SHA256SUMS"
	body, final, err := u.get(ctx, sumsURL)
	if err != nil {
		return Release{}, failf("check", "获取 SHA256SUMS 失败: %w", err)
	}
	tag, err := tagFromURL(final)
	if err != nil {
		return Release{}, failf("check", "%v", err)
	}
	sum, ok := parseSums(body, u.opts.Asset)
	if !ok {
		return Release{}, fmt.Errorf("%w: %s（%s）", ErrNoAsset, u.opts.Asset, tag)
	}
	return Release{
		Version:   normalizeVersion(tag),
		Tag:       tag,
		AssetName: u.opts.Asset,
		AssetURL:  u.opts.BaseURL + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(u.opts.Asset),
		SHA256:    sum,
	}, nil
}

// manifest 是自定义更新源的清单格式。
type manifest struct {
	Version   string          `json:"version"`
	Published string          `json:"published"`
	Notes     string          `json:"notes"`
	Assets    []manifestAsset `json:"assets"`
}

type manifestAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// checkManifest 拉取 latest.json（适配 Gitea、内网静态站、镜像）。
func (u *Updater) checkManifest(ctx context.Context) (Release, error) {
	body, final, err := u.get(ctx, u.opts.Manifest)
	if err != nil {
		return Release{}, failf("check", "获取更新清单失败: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Release{}, failf("check", "更新清单不是合法 JSON: %w", err)
	}
	if m.Version == "" {
		return Release{}, failf("check", "更新清单缺少 version 字段")
	}
	var picked *manifestAsset
	for i := range m.Assets {
		if m.Assets[i].Name == u.opts.Asset {
			picked = &m.Assets[i]
			break
		}
	}
	if picked == nil {
		return Release{}, fmt.Errorf("%w: %s（%s）", ErrNoAsset, u.opts.Asset, m.Version)
	}
	if !validSHA256(picked.SHA256) {
		return Release{}, failf("check", "更新清单里 %s 的 sha256 缺失或非法", picked.Name)
	}
	assetURL := picked.URL
	if assetURL == "" {
		return Release{}, failf("check", "更新清单里 %s 缺少 url", picked.Name)
	}
	if ref, err := url.Parse(assetURL); err == nil && !ref.IsAbs() {
		assetURL = final.ResolveReference(ref).String()
	}
	rel := Release{
		Version:   normalizeVersion(m.Version),
		Tag:       m.Version,
		Notes:     m.Notes,
		AssetName: picked.Name,
		AssetURL:  assetURL,
		Size:      picked.Size,
		SHA256:    strings.ToLower(picked.SHA256),
	}
	if m.Published != "" {
		if t, err := time.Parse(time.RFC3339, m.Published); err == nil {
			rel.Published = t
		}
	}
	return rel, nil
}

// get 发 GET 请求（带 token），返回 body 与最终 URL（跟随重定向之后）。
func (u *Updater) get(ctx context.Context, rawURL string) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "lanbox-updater/"+u.cur.String())
	if u.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+u.opts.Token)
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Request.URL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
	if err != nil {
		return nil, nil, err
	}
	return body, resp.Request.URL, nil
}

// maxManifestBytes 限制清单/SHA256SUMS 的读取量（防止被塞大文件）。
const maxManifestBytes = 1 << 20

// tagFromURL 从 .../releases/download/<tag>/<file> 里取 tag。
func tagFromURL(u *url.URL) (string, error) {
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := len(segs) - 3; i >= 0; i-- {
		if segs[i] == "download" {
			tag, err := url.PathUnescape(segs[i+1])
			if err != nil || tag == "" {
				break
			}
			return tag, nil
		}
	}
	return "", fmt.Errorf("无法从 %s 解析版本号（更新源不是标准 Release 路径？）", u.Redacted())
}

// parseSums 在 SHA256SUMS 里找文件名对应的校验值。
func parseSums(body []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		got := strings.TrimPrefix(fields[len(fields)-1], "*")
		if got != name {
			continue
		}
		if sum := strings.ToLower(fields[0]); validSHA256(sum) {
			return sum, true
		}
	}
	return "", false
}

// validSHA256 检查是不是 64 位十六进制。
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// transport 返回尊重系统代理的 Transport。
func transport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyFromEnvironment
	return tr
}
