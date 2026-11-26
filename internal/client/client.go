package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Options 构造一个已登录的客户端。
type Options struct {
	BaseURL string
	SID     string
	HTTP    *http.Client // 为空时用 http.DefaultClient
}

// Client 是 lanbox 的 HTTP 客户端。
type Client struct {
	base  *url.URL
	sid   string
	httpc *http.Client
}

// New 校验地址并构造客户端。
func New(o Options) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(o.BaseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("服务器地址无效: %q", o.BaseURL)
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return nil, fmt.Errorf("服务器地址必须以 http:// 或 https:// 开头: %q", o.BaseURL)
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	return &Client{base: base, sid: o.SID, httpc: o.HTTP}, nil
}

// BaseURL 返回规范化后的服务地址。
func (c *Client) BaseURL() string { return c.base.String() }

// do 发起一次请求，并统一处理会话 Cookie 与结构化错误。
// 返回的 resp 由调用方负责 Close；状态码 >= 400 时已转成 *Error。
func (c *Client) do(ctx context.Context, method, p string, body io.Reader, ctype string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+p, body)
	if err != nil {
		return nil, err
	}
	if c.sid != "" {
		req.Header.Set("Cookie", "sid="+c.sid)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, parseError(resp)
	}
	return resp, nil
}

// parseError 把错误响应体解析成结构化 Error，读体上限 64KB。
func parseError(resp *http.Response) error {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(raw, &body)
	msg := body.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	if msg == "" {
		msg = resp.Status
	}
	return &Error{Status: resp.StatusCode, Code: body.Error.Code, Message: msg}
}

// Login 用用户名密码换取会话令牌。这里不能带 Cookie，因此单独发请求。
func Login(ctx context.Context, baseURL, username, password string, httpc *http.Client) (*Session, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/api/login", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, parseError(resp)
	}
	var me User
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return nil, fmt.Errorf("解析登录响应失败: %w", err)
	}
	sid := ""
	for _, ck := range resp.Cookies() {
		if ck.Name == "sid" {
			sid = ck.Value
		}
	}
	if sid == "" {
		return nil, fmt.Errorf("登录成功但服务端没有下发会话令牌")
	}
	return &Session{
		URL:      strings.TrimRight(baseURL, "/"),
		Username: me.Username,
		SID:      sid,
		SavedAt:  timeNow(),
	}, nil
}

// Me 返回当前登录身份。
func (c *Client) Me(ctx context.Context) (*User, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/me", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("解析身份响应失败: %w", err)
	}
	return &u, nil
}

// Logout 注销当前会话。
func (c *Client) Logout(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/logout", nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ListQuery 是列表参数。
type ListQuery struct {
	Limit  int
	Before string
	Search string
	Todo   bool
}

// List 列出内容。
func (c *Client) List(ctx context.Context, q ListQuery) (*ListResult, error) {
	v := url.Values{}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Before != "" {
		v.Set("before", q.Before)
	}
	if q.Search != "" {
		v.Set("q", q.Search)
	}
	if q.Todo {
		v.Set("filter", "todo")
	} else {
		v.Set("filter", "all")
	}
	resp, err := c.do(ctx, http.MethodGet, "/api/items?"+v.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res ListResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("解析列表响应失败: %w", err)
	}
	return &res, nil
}

// GetItem 读取单条内容。
func (c *Client) GetItem(ctx context.Context, id string) (*Item, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/items/"+url.PathEscape(id), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var it Item
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("解析内容响应失败: %w", err)
	}
	return &it, nil
}

// Taken 设置或取消"已取走"标记。
func (c *Client) Taken(ctx context.Context, id string, taken bool) (*Item, error) {
	body, err := json.Marshal(map[string]bool{"taken": taken})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPatch, "/api/items/"+url.PathEscape(id), bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var it Item
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		return nil, fmt.Errorf("解析内容响应失败: %w", err)
	}
	return &it, nil
}

// DeleteItem 删除整条内容。服务端返回 204 空体，不解析 JSON。
func (c *Client) DeleteItem(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/items/"+url.PathEscape(id), nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// DeleteFile 删除单个附件。服务端返回 204 空体，不解析 JSON。
func (c *Client) DeleteFile(ctx context.Context, itemID, fileID string) error {
	resp, err := c.do(ctx, http.MethodDelete,
		"/api/items/"+url.PathEscape(itemID)+"/files/"+url.PathEscape(fileID), nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// timeNow 是便于测试替换的时间源。
var timeNow = time.Now
