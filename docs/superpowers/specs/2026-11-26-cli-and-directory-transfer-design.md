# lanbox 命令行客户端 + 目录上传下载设计

日期：2026-11-26
状态：待实现
范围：新增 `cmd/lanbox-cli` 与 `internal/client`；服务端扩展「保留相对路径的上传」与「流式 zip 下载」；网页端补齐目录上传/展示/下载。不改认证语义、不改单文件下载接口。

## 1. 目标

1. 提供 `lanbox-cli`：在终端完成登录、发布文字与文件、列表、下载、标记已取走、删除。
2. 服务端与 CLI **都支持目录**：一次上传一棵目录树，一条内容保留其相对路径；可按树还原下载。
3. 网页端同样支持拖入目录、按路径树展示、一键 zip 下载整条内容。
4. 保持项目既有硬约束：单个静态二进制、零第三方 Go 依赖、`CGO_ENABLED=0`。

## 2. 现有约束与关键发现

| 约束/事实 | 位置 | 影响 |
|---|---|---|
| `multipart.Part.FileName()` 强制 `filepath.Base()`（RFC 7578 要求丢弃目录信息） | Go 标准库 | **相对路径不能走 `filename=`**，必须另找通道 |
| `FormData` 无法为单个 part 设置自定义头 | 浏览器 API | 网页端也不能用 `X-Rel-Path` 之类 |
| 页面内字段名（`Content-Disposition: name=`）两端都可控 | multipart 协议 | 结论：**相对路径放在字段名里**：`name="files:photos/2024/a.jpg"` |
| `store.SanitizeName()` 用 `path.Base()` 压平文件名 | `internal/store/files.go` | 现有一切附件都是单层，`File.Name` 不含路径 |
| `store.MaxFilesPerItem = 20` | `internal/store/store.go` | 目录必然撞上限，需可配置且大幅提高 |
| README「不做的事」包含 zip 打包下载 | `README.md` | 本设计**有意推翻**该条，理由见 §5 |
| 磁盘布局 `files/<fid>__<basename>` | `internal/store/files.go` | 落盘按 `fid` 定位，路径只存元数据，不参与目录选择 |
| CSP `default-src 'self'` | `internal/httpapi/middleware.go` | 网页端不得内联脚本 |

## 3. 选型：客户端代码放哪

| 方案 | 做法 | 评价 |
|---|---|---|
| **A（选定）** | `internal/client` 承载 HTTP 交互与会话，`cmd/lanbox-cli` 只做 flag、渲染、退出码 | 可用 `httptest` 挂真实 `httpapi.Server` 做集成测试；CLI 逻辑可单测；后续可复用 |
| B | 全部写在 `cmd/lanbox-cli/main.go` | 少一层，但 HTTP/压缩/进度/flag 混在一起，`main` 上千行且测不动 |
| C | 客户端独立 module/仓库 | 分发干净，但版本要两边锁，CI 翻倍；当前只有一个使用方 |

边界约定：`internal/client` **不 import `internal/store`**，自己定义线上协议的 DTO（`Item`/`File`/`ListResult`/`Error`）。服务端字段变化由 `internal/httpapi` 的序列化结果负责，客户端只认线上契约。

## 4. 服务端：相对路径上传

### 4.1 数据模型

```go
type File struct {
    ID        string `json:"id"`
    Path      string `json:"path"` // 新增：相对路径，如 "photos/2024/a.jpg"
    Name      string `json:"name"` // basename，兼容旧前端与磁盘后缀
    Size      int64  `json:"size"`
    SHA256    string `json:"sha256"`
    Downloads int64  `json:"downloads"`
}
```

- **零迁移**：旧 `meta.json` 没有 `path`。读取侧统一走导出的 `func (f File) RelPath() string`，三级回退 `Path → Name → ID`（双空只会出现在外部写入的 meta；退化为 ID 保证 zip 条目名、`ls`、去重集合都拿不到空字符串）。
- 磁盘布局不变：`items/<id>/files/<fid>__<basename>`。磁盘名收敛为一个函数 `diskName(f File)`，写（`AddFile`）、读（`FilePath`）、删（`DeleteFile`）共用，避免三处各自拼装后走偏（曾出现「读得到、删不掉」的孤儿文件）。
- 搜索按 `RelPath()` + `Name` 双比较，所以目录名也能命中；旧数据行为等价。
- 旧前端的 `f.name`、`link download` 属性、单文件下载接口全部保持可用。

### 4.2 `SanitizeRelPath(raw string) (string, error)`

错误统一为 `ErrBadPath`（`internal/store`）。

1. `\` → `/`。
2. 若以 `/` 开头、或匹配盘符形式 `^[A-Za-z]:/`（盘符后必须是分隔符，避免把 `a:b.txt` 误判为绝对路径）→ 拒绝。
3. 按 `/` 切段，**先清洗再判定**（顺序很重要，否则 `" .."` 这类输入清洗后会重组出 `..`）：
   - 删掉 `0x00-0x1f`、`0x7f` 与控制字符集中的 `/\:*?"<>|`，再 `TrimSpace`；**保留前导点**（`.env`、`.gitignore` 必须能传）；
   - 清洗后为 `""` 或 `"."` → **丢弃**（`a//b`、`a/./b` → `a/b`）；
   - 清洗后为 `".."` → **拒绝**（不做静默改写，防止 `..`、`.\x00.`、`" \t.."` 逃逸）；
   - 清洗后单段 rune 数 > 120 → 拒绝（**不截断**，避免两条不同路径截断后撞名）。
4. 结果段数 > 16 → 拒绝；连接后字节数 > 1024 → 拒绝。
5. 全部段被丢弃 → 拒绝。

> 与 `SanitizeName` 的差异仅一处：不剥前导点。理由是路径场景下 dotfile 是正常内容。
> 「先清洗再判定」是关键：清洗会把 `" .."`、`".\x00."`、`" \t.."` 变成 `".."`，只有在清洗后判定才能堵住穿越。

### 4.3 `AddFile` 的变化

签名改为 `AddFile(itemID, relPath string, declaredSize int64, r io.Reader) (*Item, error)`。

1. `SanitizeRelPath` 失败 → `ErrBadPath`，不建临时文件。
2. 数量上限：`len(item.Files) >= opts.MaxFiles` → `ErrTooManyFiles`。
3. 单条总字节上限：`remaining := opts.MaxItemBytes - sum(files.Size)`；读取上限取 `min(MaxFileBytes, remaining+1)`；写完 `n > remaining` → 删除该文件、返回 `ErrItemTooLarge`。
4. 撞名去重：键为 `strings.ToLower(relPath)`（大小写不敏感，避免在 Windows/macOS 解压时互相覆盖）。命中时对 basename 按 `a.txt` → `a~2.txt` → `a~3.txt` 递增，直到不冲突；尝试 1000 次仍冲突返回错误。
5. `Name = path.Base(最终路径)`，`Path = 最终路径`。

### 4.4 上传处理器（`POST /api/items`）

逐个 part 判定：

| 字段名 | 处理 |
|---|---|
| `text` | 文本，语义不变（上限 `max_text_bytes`） |
| `files:<relpath>` | 用 `relpath` 作为路径；`part.FileName()` 只用于错误提示，**不参与路径** |
| `files`（旧网页/旧脚本） | 路径 = `part.FileName()`（等价于今天的行为） |
| 其它且 `FileName()==""` | 忽略 |

`files:` 后为空、或清洗失败 → 400 `bad_path`，并清理已写入的内容（沿用现有 `cleanup()`）。其余逻辑（先建内容后回填文本、SSE 广播、日志）不变。

### 4.5 配置新增

| 字段 | 默认 | 环境变量 | 校验 |
|---|---|---|---|
| `max_files_per_item` | 500 | `LANBOX_MAX_FILES_PER_ITEM` | 1..10000 |
| `max_item_size_mb` | 2048 | `LANBOX_MAX_ITEM_SIZE_MB` | > 0 |

单文件仍受 `max_file_size_mb` 约束，两者取 min；不强制 `max_item_size_mb >= max_file_size_mb`（允许更严的单条限制），在 README 中说明。

### 4.6 错误码

| code | 状态 | 触发 |
|---|---|---|
| `bad_path` | 400 | `SanitizeRelPath` 拒绝 |
| `item_too_large` | 413 | 单条累计超过 `max_item_size_mb` |
| `no_files` | 400 | 对无附件的内容请求 zip |
| `too_many_files` | 400 | 消息改为带上实际上限（`max_files_per_item`） |

## 5. 服务端：流式 zip 下载

`GET /api/items/{id}/zip`（`withAuth`）：

1. `store.Get(id)`；`len(files) == 0` → 400 `no_files`（必须在写响应体之前判定）。
2. 头：`Content-Type: application/zip`、`Content-Disposition: attachment; filename="lanbox-<id>.zip"`、`Cache-Control: no-store`、**不设 Content-Length**（chunked）。
3. `zip.NewWriter(w)`，逐附件：`CreateHeader{Name: relPath, Method: zip.Store, Modified: item.CreatedAt}` → `io.Copy` → `Close()`；全部完成后 `zw.Close()`。
4. 用 `Store`（不压缩）：ARM 小主机省 CPU，附件多为已压缩媒体；CRC32 由 `zip.Writer` 计算。
5. 条目名再次防御：空或含 `..` 段则回退到 `Name`。
6. 读盘失败只能记日志并中断（响应已开始，客户端会看到损坏 zip）；请求失败路径（未写头前）用标准错误体。

推翻「不做 zip」的理由：目录下载没有 zip 就名不副实（100 个文件点 100 次）。代价可控——`archive/zip` 是标准库、内存恒定、只新增一个路由；单文件下载仍走原接口并保留 Range。

已知限制（写进 README）：zip 不支持断点续传；单附件 > 4GB 时不受支持（默认 `max_file_size_mb=1024`，不会触发）。

## 6. CLI：`cmd/lanbox-cli`

### 6.1 命令面

```
lanbox-cli [-s URL] [--json] [-insecure] [-ca FILE] <command> [args]
```

| 命令 | 用法 | 说明 |
|---|---|---|
| `login` | `login [-u USER]` | 密码来源优先级：`LANBOX_PASSWORD` → 管道 stdin → 终端无回显；成功后写会话文件 |
| `logout` | `logout` | 调 `POST /api/logout`，删除本地会话（服务端不可达也删除，并提示） |
| `whoami` | `whoami` | `GET /api/me` |
| `put` | `put [-m TEXT] [-r] PATH...` | `-` 表示从 stdin 读文本；不带 `-r` 时目录报用法错误；进度写 stderr |
| `ls` | `ls [-n N] [-q TEXT] [--todo] [--json]` | 默认 20 条 |
| `files` | `files ID` | 列出该内容的附件：`FID · 路径 · 大小 · 下载次数`（`rm-file` 的 FID 来源） |
| `get` | `get ID [-o DIR] [--zip FILE] [--force] [--resume]` | 默认按相对路径还原 |
| `taken` | `taken ID [--undo]` | `PATCH /api/items/{id}` |
| `rm` | `rm ID` | 删除整条内容 |
| `rm-file` | `rm-file ID FID` | 删除单个附件 |

### 6.2 会话与地址

- 会话文件：`$XDG_CONFIG_HOME/lanbox/session.json`，未设则 `~/.config/lanbox/session.json`；目录 0700、文件 0600。
- 内容：`{"url":"https://192.168.1.9:8443","username":"admin","sid":"…","saved_at":"…"}`。
- 地址优先级：`-s` > `LANBOX_URL` > 会话文件里的 `url`。
- 若显式传入的地址与会话里的 `url` 不一致 → 提示「会话属于 <url>，请先 `login -s <url>`」，退出码 4。避免把令牌发给另一台机器。
- 认证失败（401）→ 提示重新登录，退出码 4。

### 6.3 上传细节

- 目录展开：`put -r photos` → 字段名 `files:photos/2024/a.jpg`（**目录名本身进路径**，这样多次 `put -r` 不会平铺混淆）。
- 跳过符号链接（防循环）、跳过空目录（不产生空路径 part）。
- 客户端不做数量/大小预检（上限可配置，唯一真源在服务端）；`too_many_files` / `item_too_large` / `file_too_large` 的中文消息原样打印，退出码 1。
- 进度：`io.Pipe` + 计数 writer；总长已知（文件大小 + 文本长度）时显示 `已发送/总量` 与百分比，TTY 用 `\r` 就地刷新，非 TTY 安静。请求使用 chunked 编码（不设 Content-Length）。
- 成功后打印 `已发布 <id>（N 个附件，共 X）`；`--json` 打印服务端返回的 item JSON。

### 6.4 下载细节

- 无附件、纯文本内容 → 只打印文本到 stdout（`--json` 除外），不写文件。
- 单附件：路径里没有目录时直接落到 `<DIR>/<basename>`；带目录时按相对路径还原。
- 多附件/含目录：`<DIR>/<relPath>`，自动 `MkdirAll(0750)`。
- 目标已存在：默认**跳过并提示**（提示写 stderr），`--force` 覆盖——`--resume` 不会绕过这条规则。
- `--resume`：仅单附件；从同目录的 `<dest>.part` 已有字节发 `Range` 续传，结束后校验 sha256 并 rename。
- 写入使用 `<name>.part` 临时文件，sha256 校验通过后 `rename` 成目标名；校验失败删除临时文件、报错、退出码 1。
- `--zip FILE`：直接 `GET .../zip` 存为文件，**不自动解压**；与 `--resume` 互斥（用法错误）。

### 6.5 输出与退出码

- 人类可读输出用 `text/tabwriter`；`ls` 列：`ID · 时间 · 发布者 · 附件(数量/总大小) · 状态 · 文本摘要(≤40 字)`。
- `--json`：每条命令输出一个 JSON 值（对象或数组），不混人类文案；进度与提示仍写 stderr。
- 退出码：0 成功；1 一般错误/校验失败；2 用法错误；3 网络错误；4 认证/会话问题（未登录、过期、会话地址不匹配）。

### 6.6 TLS

- 默认完整校验；自签证书失败时打印具体错误与三种解法（`-ca`、信任证书、`-insecure`）。
- `-ca FILE`：把该 PEM 加入 `x509.CertPool`（优先使用系统池 + 追加）。
- `-insecure`：`InsecureSkipVerify`，并在 stderr 打警告。
- 会话文件**不保存** TLS 选项。

### 6.7 无回显读密码

- `cmd/lanbox-cli/term_linux.go`：`syscall` ioctl（`TCGETS`/`TCSETS`，`unsafe.Pointer`）关回显，读完恢复；失败则退化为可见输入。
- `cmd/lanbox-cli/term_other.go`（`//go:build !linux`）：直接可见输入并提示「输入可见」。
- 非 TTY（管道）→ 逐行读取，不提示。

## 7. 网页端改动

以当前 `web/`（原生 ES 模块）为准：

1. 上传：每个文件以 `files:<webkitRelativePath || name>` 作为字段名发送；`web/api.js` 的 `uploadItem` 增加 `path` 参数（`File` 对象上带 `relPath`）。
2. 目录入口：工具栏加「选择文件夹」（`<input type="file" webkitdirectory multiple>`），拖拽时用 `DataTransferItem.webkitGetAsEntry()` 递归展开；`relPath` 取 `entry.fullPath` 去掉前导 `/`。空目录跳过。
3. 展示：附件按 `Path` 排序，含目录时按前缀缩进并显示目录名（淡色），单层文件保持现状。
4. 下载：「下载全部 (.zip)」按钮在 `files.length > 1 || 任一 Path 含 "/"` 时出现，指向 `/api/items/{id}/zip`；单附件仍用原链接（保留 Range 与计数）。
5. 前端预检：选中的文件数 > 500（与默认 `max_files_per_item` 一致）时提示；真实上限以服务端返回为准。

## 8. 与进行中的 Vue 改版的关系

`docs/superpowers/specs/2026-11-26-web-vue-theme-design.md` 尚未实现。本设计的服务端与 CLI 部分与它无冲突；§7 的网页改动若与 Vue 改版同批进行，需在 `frontend/src` 中实现同等能力（组件接口与 API 契约不变）。两者只共享 `files:<path>` 字段名与 `/zip` 路由，不共享代码。

## 9. 测试策略

- `internal/store`
  - `SanitizeRelPath` 表驱动：`..`、绝对路径、`C:\x`、`a//b`、`a/./b`、`.env` 保留、超深、超长、单段超 120、控制字符、全空。
  - `AddFile`：Path 与 Name 正确；大小写不同的同名去重；`MaxFiles`、`MaxItemBytes` 边界；失败不留残文件。
  - 兼容：手写无 `path` 的 `meta.json`，`Open` 后 `relPath()` 回退 `Name`，`FilePath` 正常。
- `internal/httpapi`
  - `files:<path>` 上传 → 返回 item 中 `path` 正确；旧字段 `files` 仍可用。
  - `bad_path` 400 且不留内容；`item_too_large` 413；`too_many_files` 消息带上限。
  - zip：`zip.NewReader` 解开，条目名 = `path`，内容 sha256 与 meta 一致；空附件 400 `no_files`；未登录 401。
- `internal/client`
  - `httptest.NewTLSServer` 挂真实 `httpapi.Server`，跑 `login → put(含目录) → ls → get → taken → rm` 全链路；`-ca` 用 `srv.Certificate()` 注入。
  - 会话文件权限 0600；地址不匹配时拒绝复用。
- `cmd/lanbox-cli`
  - flag 解析、目录展开、表格/JSON 渲染、退出码映射抽成纯函数单测。
- 构建：`make check` 通过；`make dist` 追加 `lanbox-cli-<os>-<arch>`（同一组 TARGETS）。

## 10. 明确不做

- 分片上传 / 上传断点续传（CLI 的 `--resume` 仅指下载单文件）。
- zip 的 Range 与断点续传、zip 内加密、zip64 场景（>4GB 单文件）。
- 保留空目录、保留符号链接、保留文件权限与 mtime（zip 的 `Modified` 用内容创建时间）。
- 服务端 API Token / 免密长效凭证。
- GUI 客户端、Windows 原生二进制（`make dist` 仍只出 linux 目标，与现状一致）。
- CLI 订阅 SSE：`ls` 是一次性快照，实时刷新仍由网页负责。

## 11. 兼容与迁移

- 旧数据零迁移；旧网页/旧脚本用的 `files` 字段仍被接受。
- 新服务端可被旧 CLI 使用（不涉及）；新 CLI 需要新服务端（`files:` 字段与 `/zip` 依赖新版本）。
- `MaxFilesPerItem = 20` 常量退化为 `max_files_per_item` 的默认值来源，配置默认值改为 500：**已有内容不受影响**（只在追加附件时检查，而当前没有追加附件的 API）。

## 12. 风险

| 风险 | 缓解 |
|---|---|
| 500 文件 × 每条内容 → 元数据与内存索引膨胀 | `max_files_per_item` 可配；`max_item_size_mb` 限制总字节；README 说明按需调低 |
| 中文/超长路径在 Windows 解压失败 | 单段 120 rune、总长 1024 字节限制；README 提示长路径问题 |
| 目录上传把整盘打包进一条内容 → 长时间占用连接 | 客户端进度可见；服务端单条上限兜底；`put -r` 不做隐式递归（必须显式 `-r`） |
| zip 中断产生半截文件 | `Content-Disposition` 明确；CLI 写临时文件后 rename |
| `files:` 字段名依赖 multipart 字段名，非标准约定 | 服务端同时接受旧 `files`；契约定死并写进 README 的 API 说明 |

## 13. 验收清单

1. `echo hi | lanbox-cli put -` → 网页 1 秒内出现该文字。
2. `lanbox-cli put -r photos -m "内网素材"` → 网页按树展示路径；CLI `ls` 显示附件数与总大小。
3. `lanbox-cli get <id> -o /tmp/x` → `/tmp/x/photos/...` 结构与 sha256 正确。
4. `lanbox-cli get <id> --zip /tmp/x.zip` → `unzip -t` 通过，条目名与路径一致。
5. 网页拖入含子目录的文件夹 → 上传成功且路径保留；「下载全部 (.zip)」可用。
6. 同名 `A.txt`/`a.txt` 同批上传 → 服务端显示第二个为 `a~2.txt`。
7. 单条超过 500 文件 / 超过 2GB → 客户端得到明确中文错误，服务端无残留目录。
8. 自签证书下三种方式（`-ca`、系统信任、`-insecure`）都能连通；错误提示可操作。
9. 会话过期后任意命令 → 提示重新登录，退出码 4。
10. `make check` 全绿；`make dist` 产出四个平台的服务端与 CLI 二进制。
