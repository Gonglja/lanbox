// lanbox-cli 是 lanbox 的命令行客户端。
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

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

// errAuth 表示认证或会话问题（未登录、会话过期、会话与目标地址不匹配）。
var errAuth = errors.New("认证/会话问题")

// globals 是全部子命令共享的选项。
type globals struct {
	server   string
	json     bool
	insecure bool
	caFile   string
	pinFP    string
	stdout   io.Writer
	stderr   io.Writer
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	g := &globals{stdout: stdout, stderr: stderr}
	fs := g.globalFS("lanbox-cli", stderr)
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		usage(stdout)
		return exitOK
	}
	// 第一个非选项参数是子命令；它之前的选项归全局。
	idx, err := splitCommand(fs, args)
	if err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		return exitUsage
	}
	if _, err := parseArgs(fs, args[:idx]); err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		return exitUsage
	}
	if idx >= len(args) {
		usage(stderr)
		return exitUsage
	}

	cmd, cmdArgs := args[idx], args[idx+1:]
	ctx := context.Background()
	switch cmd {
	case "login":
		err = cmdLogin(ctx, g, cmdArgs, os.Stdin)
	case "logout":
		err = cmdLogout(ctx, g, cmdArgs)
	case "whoami":
		err = cmdWhoami(ctx, g, cmdArgs)
	case "status":
		err = cmdStatus(ctx, g, cmdArgs)
	case "update":
		err = cmdUpdate(ctx, g, cmdArgs, os.Stdin)
	case "discover":
		err = cmdDiscover(g, cmdArgs)
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
	case "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "未知命令: %s\n", cmd)
		usage(stderr)
		return exitUsage
	}
	if err != nil {
		return printAndCode(err, stderr)
	}
	return exitOK
}

// exitCode 把错误映射为退出码，便于单测与统一语义。
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, errUsage) {
		return exitUsage
	}
	if errors.Is(err, errAuth) {
		return exitAuth
	}
	var ae *client.Error
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return exitAuth
		}
		return exitFail
	}
	var ua x509.UnknownAuthorityError
	if errors.As(err, &ua) {
		return exitNetwork
	}
	if isNetworkErr(err) {
		return exitNetwork
	}
	return exitFail
}

// printAndCode 打印错误（必要时附上解决办法）并返回对应退出码。
func printAndCode(err error, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		var ae *client.Error
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			fmt.Fprintln(stderr, "提示: 会话已失效，请重新运行 lanbox-cli login")
		}
		var ua x509.UnknownAuthorityError
		if errors.As(err, &ua) {
			fmt.Fprint(stderr, `提示: 自签证书未被信任。三种办法：
  1) 用 -ca <cert.pem> 指定服务端证书
  2) 把 cert.pem 导入系统信任库
  3) 加 -insecure 临时跳过校验（不安全）
`)
		}
	}
	return exitCode(err)
}

// globalFS 建立注册了全部全局选项的 FlagSet。
// 子命令的 FlagSet 也用它，这样 login -s URL、put x --json 都能生效。
func (g *globals) globalFS(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&g.server, "s", g.server, "服务地址，如 https://192.168.1.9（默认 443 端口）")
	fs.BoolVar(&g.json, "json", g.json, "输出机器可读 JSON")
	fs.BoolVar(&g.insecure, "insecure", g.insecure, "跳过 TLS 证书校验（不安全）")
	fs.StringVar(&g.caFile, "ca", g.caFile, "信任指定的 PEM 证书文件")
	return fs
}

// subFS 建立子命令的 FlagSet，并注册全部全局选项。
func (g *globals) subFS(name string) *flag.FlagSet { return g.globalFS(name, g.stderr) }

func usage(w io.Writer) {
	fmt.Fprint(w, `用法: lanbox-cli [全局选项] <命令> [参数]

全局选项（可写在子命令前后）:
  -s URL        服务地址（也可用 LANBOX_URL）
  -json         输出 JSON
  -insecure     跳过 TLS 校验（不推荐）
  -ca FILE      信任指定 PEM 证书

命令:
  login [-u 用户名]        登录并保存会话
  logout [--json]          注销并删除本地会话
  whoami                   显示当前身份
  status                   查看服务、内容统计（管理员含磁盘/用户/mDNS）
  update [--check|--apply|--rollback] [--yes]
                           查看/检查/执行在线更新（升级与回滚需要管理员）
  discover [-timeout 2s]   列出局域网内的 lanbox 实例（-json 输出机器可读）
  put [-m 文本] [-r] 路径…  发布文字/文件/目录（- 表示从 stdin 读文本）
  ls [-n 数量] [-q 关键词] [--todo] [-o]  列出内容（-o 同时打印附件）
  files ID                 列出某条内容的附件
  get ID [-o 目录] [--zip 文件] [--force] [--resume] [--json]
  taken ID [--undo]        标记/取消已取走
  rm ID [--json]           删除整条内容
  rm-file ID FID [--json]  删除单个附件

用 -- 结束选项解析，其后的参数一律当作路径，例如 put -r -- -s。
`)
}

// resolveServer 按 -s > LANBOX_URL > 会话文件 的顺序确定目标服务地址。
// allowDiscover 为 true 时（只有 login 会这样用），前三者都没有才做局域网 mDNS 自动发现。
// 显式地址与会话里的地址不一致时属于会话问题，提示重新登录。
func resolveServer(g *globals, sess *client.Session, allowDiscover bool) (string, error) {
	explicit := strings.TrimRight(g.server, "/")
	if explicit == "" {
		explicit = strings.TrimRight(os.Getenv("LANBOX_URL"), "/")
	}
	if explicit != "" {
		if sess != nil && sess.URL != "" && explicit != strings.TrimRight(sess.URL, "/") {
			return "", fmt.Errorf("%w: 会话属于 %s，请先运行 lanbox-cli login -s %s", errAuth, sess.URL, explicit)
		}
		return explicit, nil
	}
	if sess != nil && sess.URL != "" {
		return sess.URL, nil
	}
	if !allowDiscover {
		return "", fmt.Errorf("%w: 尚未登录，请先运行 lanbox-cli login -s <地址>", errAuth)
	}
	base, fp, err := decideServer(2 * time.Second)
	if err != nil {
		return "", err
	}
	g.pinFP = fp
	return base, nil
}

// newClient 读取会话并按全局选项组装客户端。
// 没有会话时仍会先解析地址（可能是 mDNS 自动发现），再提示到该地址登录。
func newClient(g *globals) (*client.Client, *client.Session, error) {
	sess, err := loadSessionOptional()
	if err != nil {
		return nil, nil, err
	}
	base, err := resolveServer(g, sess, false)
	if err != nil {
		return nil, nil, err
	}
	if sess == nil {
		return nil, nil, fmt.Errorf("%w: 尚未登录，请先运行 lanbox-cli login -s <地址>", errAuth)
	}
	if sess.PinFP != "" && g.pinFP == "" {
		g.pinFP = sess.PinFP
	}
	httpc, err := httpClient(g)
	if err != nil {
		return nil, nil, err
	}
	c, err := client.New(client.Options{BaseURL: base, SID: sess.SID, HTTP: httpc})
	if err != nil {
		return nil, nil, err
	}
	return c, sess, nil
}

// httpClient 按 -insecure / -ca 构造 HTTP 客户端，最低 TLS 1.2。
func httpClient(g *globals) (*http.Client, error) {
	trans := http.DefaultTransport.(*http.Transport).Clone()
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if g.pinFP != "" && !g.insecure && g.caFile == "" {
		want := strings.ToLower(strings.TrimSpace(g.pinFP))
		tlsCfg.InsecureSkipVerify = true
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("服务器未提供证书")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := hex.EncodeToString(sum[:])
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				return fmt.Errorf("证书指纹不匹配（通告 %s，实际 %s）；若证书已更换，请重新 discover 或使用 -ca", shortFP(want), shortFP(got))
			}
			return nil
		}
		fmt.Fprintf(g.stderr, "已按 mDNS 通告固定证书指纹 sha256:%s\n", shortFP(want))
	}
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
	fs := g.subFS("login")
	user := fs.String("u", "admin", "用户名")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	// login 只认显式地址：此时还没有会话文件可参考。
	server, err := resolveServer(g, nil, true)
	if err != nil {
		return err
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
	sess.PinFP = g.pinFP
	if err := saveSession(sess); err != nil {
		return err
	}
	if g.json {
		return printJSON(g.stdout, map[string]string{"url": sess.URL, "username": sess.Username})
	}
	fmt.Fprintf(g.stdout, "已登录 %s（%s）\n", sess.Username, sess.URL)
	return nil
}

func cmdLogout(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("logout")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
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
	if g.json {
		return printJSON(g.stdout, map[string]string{"logged_out": sess.Username})
	}
	fmt.Fprintf(g.stdout, "已退出 %s（%s）\n", sess.Username, sess.URL)
	return nil
}

func cmdWhoami(ctx context.Context, g *globals, args []string) error {
	fs := g.subFS("whoami")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
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
