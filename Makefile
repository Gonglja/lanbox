# lanbox 构建入口
#
#   make            等价于 make dist（全部平台的静态二进制 + 校验和）
#   make build      只编本机架构
#   make dist       交叉编译 linux/amd64、linux/arm64、linux/armv7、linux/386
#                   同时产出 CLI（lanbox-cli）与 Windows 客户端（lanbox-agent）
#   make windows    只编 Windows 的 exe（amd64/arm64，服务端 + CLI + 客户端）
#   make agent      只编 Windows 客户端（lanbox-agent，含托盘与剪贴板监听）
#   make client     把纯 web 客户端打成单文件 dist/lanbox-client.html
#   make test       跑全部测试
#   make check      gofmt + go vet + test（提交前自检）
#   make clean      清掉 dist/ 与本机二进制
#
# 所有产物都是 CGO_ENABLED=0 的静态二进制，可直接拷到目标机器运行。

APP      := lanbox
CMD      := ./cmd/lanbox
CLI      := lanbox-cli
CLI_CMD  := ./cmd/lanbox-cli
AGENT    := lanbox-agent
AGENT_CMD := ./cmd/lanbox-agent
# Wails 需要 desktop,production 标签；GUI 程序用 -H windowsgui 不带控制台窗口。
AGENT_TAGS := desktop,production
AGENT_LDFLAGS = $(LDFLAGS) -H=windowsgui
DIST     := dist
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  := -trimpath
LDFLAGS  := -s -w -X main.version=$(VERSION)

# 交叉编译目标：os/arch[/GOARM]。Windows 单独成组，产物带 .exe 后缀。
TARGETS  := linux/amd64 linux/arm64 linux/arm/7 linux/386
WINTARGETS := windows/amd64 windows/arm64

HOST_OS   := $(shell go env GOOS)
HOST_ARCH := $(shell go env GOARCH)
# 在 Windows 上本机编译时产物需要 .exe 后缀。
ifeq ($(HOST_OS),windows)
EXE := .exe
else
EXE :=
endif

PREFIX ?= /usr/local

.PHONY: all build dist windows agent client checksums test vet fmt check run install clean help

all: dist

## build: 编译本机架构到 ./lanbox 与 ./lanbox-cli
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(APP)$(EXE) $(CMD)
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(CLI)$(EXE) $(CLI_CMD)
	@echo "已生成 ./$(APP)$(EXE) 与 ./$(CLI)$(EXE)（$(HOST_OS)/$(HOST_ARCH)，版本 $(VERSION)）"

## dist: 交叉编译全部目标（Linux + Windows）到 dist/
dist:
	@mkdir -p $(DIST)
	@set -eu; \
	for t in $(TARGETS); do \
		os=$${t%%/*}; \
		rest=$${t#*/}; \
		arch=$${rest%%/*}; \
		goarm=$${rest#*/}; \
		if [ "$$goarm" = "$$rest" ]; then goarm=""; fi; \
		suffix=""; \
		if [ -n "$$goarm" ]; then suffix="v$$goarm"; fi; \
		name=$(APP)-$$os-$$arch$$suffix; \
		echo "==> $$name  ($$os/$$arch GOARM=$${goarm:-n/a})"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$goarm \
			go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$$name $(CMD); \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$goarm \
			go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(CLI)-$$os-$$arch$$suffix $(CLI_CMD); \
	done
	@$(MAKE) --no-print-directory windows
	@$(MAKE) --no-print-directory client
	@$(MAKE) --no-print-directory checksums

## windows: 交叉编译 Windows exe（amd64/arm64，服务端 + CLI + 客户端）
## 在 Windows 上（没有 POSIX shell）请改用：powershell -ExecutionPolicy Bypass -File build-windows.ps1
windows:
	@mkdir -p $(DIST)
	@set -eu; \
	for t in $(WINTARGETS); do \
		os=$${t%%/*}; \
		arch=$${t#*/}; \
		name=$(APP)-$$os-$$arch.exe; \
		echo "==> $$name  ($$os/$$arch)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$$name $(CMD); \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(CLI)-$$os-$$arch.exe $(CLI_CMD); \
		echo "==> $(AGENT)-$$os-$$arch.exe  ($$os/$$arch, GUI)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -tags $(AGENT_TAGS) $(GOFLAGS) -ldflags "$(AGENT_LDFLAGS)" \
				-o $(DIST)/$(AGENT)-$$os-$$arch.exe $(AGENT_CMD); \
	done

## agent: 编译本机的 Windows 客户端（lanbox-agent.exe，托盘 + 剪贴板监听）
agent:
ifeq ($(HOST_OS),windows)
	CGO_ENABLED=0 go build -tags $(AGENT_TAGS) $(GOFLAGS) \
		-ldflags "$(AGENT_LDFLAGS)" -o $(AGENT)$(EXE) $(AGENT_CMD)
	@echo "已生成 ./$(AGENT)$(EXE)（版本 $(VERSION)）"
else
	@echo "lanbox-agent 只能在 Windows 上运行；交叉编译请用 make windows" >&2
	@exit 1
endif

## client: 把纯 web 客户端（client/）打成单文件 dist/lanbox-client.html
client:
	@mkdir -p $(DIST)
	go run ./tools/buildclient -root . -out $(DIST)/lanbox-client.html

## checksums: 生成并打印 dist/SHA256SUMS
checksums:
	@cd $(DIST) && sha256sum $(APP)-* > SHA256SUMS && cat SHA256SUMS

## test: 跑全部测试
test:
	go test ./... -count=1

## vet: 静态检查
vet:
	go vet ./...

## fmt: 格式化全部 Go 代码
fmt:
	gofmt -l -w .

## check: 提交前自检（格式 + 静态检查 + 测试）
check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "以下文件未格式化："; echo "$$out"; exit 1; fi
	go vet ./...
	go test ./... -count=1

## run: 本机前台运行（数据在 ./data）
run:
	go run $(CMD) -config config.json

## install: 安装到 PREFIX/bin（默认 /usr/local/bin）
install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 0755 $(APP) $(DESTDIR)$(PREFIX)/bin/$(APP)
	install -m 0755 $(CLI) $(DESTDIR)$(PREFIX)/bin/$(CLI)
	@echo "已安装 $(APP) 与 $(CLI) 到 $(DESTDIR)$(PREFIX)/bin"

## clean: 清理构建产物
clean:
	rm -rf $(DIST) $(APP) $(CLI) $(AGENT) $(APP).exe $(CLI).exe $(AGENT).exe

## help: 列出可用目标
help:
	@echo "用法: make [目标]"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
