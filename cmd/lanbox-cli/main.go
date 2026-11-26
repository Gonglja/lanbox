// lanbox-cli 是 lanbox 的命令行客户端。
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"lanbox/internal/client"
)

// 退出码约定。
const (
	exitOK      = 0
	exitFail    = 1
	exitUsage   = 2
	exitNetwork = 3
	exitAuth    = 4
)

// errUsage 表示参数用法错误。
var errUsage = errors.New("用法错误")

// globals 是全部子命令共享的选项。
type globals struct {
	server   string
	json     bool
	insecure bool
	caFile   string
	verbose  bool
	stdout   io.Writer
	stderr   io.Writer
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	g := &globals{stdout: stdout, stderr: stderr}
	// 全局选项允许写在子命令后面，先挑出来再解析。
	globalArgs, rest := splitGlobals(args)
	fs := flag.NewFlagSet("lanbox-cli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&g.server, "s", "", "服务地址，如 https://192.168.1.9:8443")
	fs.BoolVar(&g.json, "json", false, "输出机器可读 JSON")
	fs.BoolVar(&g.insecure, "insecure", false, "跳过 TLS 证书校验（不安全）")
	fs.StringVar(&g.caFile, "ca", "", "信任指定的 PEM 证书文件")
	fs.BoolVar(&g.verbose, "v", false, "打印请求细节")
	if err := fs.Parse(globalArgs); err != nil {
		return exitUsage
	}
	if len(rest) == 0 {
		usage(stderr)
		return exitUsage
	}

	cmd, cmdArgs := rest[0], rest[1:]
	ctx := context.Background()
	var err error
	switch cmd {
	case "login":
		err = cmdLogin(ctx, g, cmdArgs, os.Stdin)
	case "logout":
		err = cmdLogout(ctx, g)
	case "whoami":
		err = cmdWhoami(ctx, g)
	case "put":
		err = cmdPut(ctx, g, cmdArgs, os.Stdin)
	case "ls":
		err = cmdLs(ctx, g, cmdArgs)
	case "files":
		err = cmdFiles(ctx, g, cmdArgs)
	case "get":
		err = cmdGet(ctx, g, cmdArgs)
	case "taken":
		err = cmdTaken(ctx, g, cmdArgs)
	case "rm":
		err = cmdRm(ctx, g, cmdArgs)
	case "rm-file":
		err = cmdRmFile(ctx, g, cmdArgs)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "未知命令: %s\n", cmd)
		usage(stderr)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		var ae *client.Error
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			fmt.Fprintln(stderr, "提示: 会话已失效，请重新运行 lanbox-cli login")
			return exitAuth
		}
		if errors.Is(err, errUsage) {
			return exitUsage
		}
		if isNetworkErr(err) {
			return exitNetwork
		}
		return exitFail
	}
	return exitOK
}

func usage(w io.Writer) {
	fmt.Fprint(w, `用法: lanbox-cli [全局选项] <命令> [参数]

全局选项:
  -s URL        服务地址（也可用 LANBOX_URL）
  -json         输出 JSON
  -insecure     跳过 TLS 校验（不推荐）
  -ca FILE      信任指定 PEM 证书
  -v            打印请求细节

命令:
  login [-u 用户名]        登录并保存会话
  logout                   注销并删除本地会话
  whoami                   显示当前身份
  put [-m 文本] [-r] 路径…  发布文字/文件/目录（- 表示从 stdin 读文本）
  ls [-n 数量] [-q 关键词] [--todo] [-o]  列出内容（-o 同时打印附件）
  files ID                 列出某条内容的附件
  get ID [-o 目录] [--zip 文件] [--force] [--resume]
  taken ID [--undo]        标记/取消已取走
  rm ID                    删除整条内容
  rm-file ID FID           删除单个附件
`)
}

// newClient 读取会话并按全局选项组装客户端。
func newClient(g *globals) (*client.Client, *client.Session, error) {
	sess, err := loadSession()
	if err != nil {
		return nil, nil, err
	}
	if g.server != "" && strings.TrimRight(g.server, "/") != sess.URL {
		return nil, nil, fmt.Errorf("会话属于 %s，请先运行 lanbox-cli login -s %s",
			sess.URL, strings.TrimRight(g.server, "/"))
	}
	httpc, err := httpClient(g)
	if err != nil {
		return nil, nil, err
	}
	c, err := client.New(client.Options{BaseURL: sess.URL, SID: sess.SID, HTTP: httpc})
	if err != nil {
		return nil, nil, err
	}
	return c, sess, nil
}

// httpClient 按 -insecure / -ca 构造 HTTP 客户端，最低 TLS 1.2。
func httpClient(g *globals) (*http.Client, error) {
	trans := http.DefaultTransport.(*http.Transport).Clone()
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if g.insecure {
		tlsCfg.InsecureSkipVerify = true
		fmt.Fprintln(g.stderr, "警告: 已跳过 TLS 证书校验（-insecure）")
	}
	if g.caFile != "" {
		pem, err := os.ReadFile(g.caFile)
		if err != nil {
			return nil, fmt.Errorf("读取证书 %s 失败: %w", g.caFile, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("证书文件 %s 里没有可用的 PEM 证书", g.caFile)
		}
		tlsCfg.RootCAs = pool
	}
	trans.TLSClientConfig = tlsCfg
	return &http.Client{Transport: trans}, nil
}

// isNetworkErr 判断是否为连接层面的错误。
func isNetworkErr(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var oe *net.OpError
	return errors.As(err, &oe)
}

func cmdLogin(ctx context.Context, g *globals, args []string, stdin *os.File) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(g.stderr)
	user := fs.String("u", "admin", "用户名")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	server := g.server
	if server == "" {
		server = os.Getenv("LANBOX_URL")
	}
	if server == "" {
		return fmt.Errorf("%w: 请用 -s 指定服务地址，例如 -s https://192.168.1.9:8443", errUsage)
	}
	pw, err := readPassword(stdin, g.stderr)
	if err != nil {
		return err
	}
	httpc, err := httpClient(g)
	if err != nil {
		return err
	}
	sess, err := client.Login(ctx, server, *user, pw, httpc)
	if err != nil {
		return err
	}
	if err := saveSession(sess); err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, map[string]string{"url": sess.URL, "username": sess.Username})
	}
	fmt.Fprintf(g.stdout, "已登录 %s（%s）\n", sess.Username, sess.URL)
	return nil
}

func cmdLogout(ctx context.Context, g *globals) error {
	c, sess, err := newClient(g)
	if err != nil {
		return err
	}
	if err := c.Logout(ctx); err != nil {
		fmt.Fprintf(g.stderr, "提示: 服务端注销失败（%v），仍将删除本地会话\n", err)
	}
	if err := removeSession(); err != nil {
		return err
	}
	fmt.Fprintf(g.stdout, "已退出 %s（%s）\n", sess.Username, sess.URL)
	return nil
}

func cmdWhoami(ctx context.Context, g *globals) error {
	c, sess, err := newClient(g)
	if err != nil {
		return err
	}
	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, map[string]any{
			"url": sess.URL, "username": me.Username, "is_admin": me.IsAdmin,
		})
	}
	role := "普通用户"
	if me.IsAdmin {
		role = "管理员"
	}
	fmt.Fprintf(g.stdout, "%s（%s）@ %s\n", me.Username, role, sess.URL)
	return nil
}

// printJSON 输出单行 JSON。
func printJSON(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
