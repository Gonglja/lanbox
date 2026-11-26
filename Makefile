# lanbox 构建入口
#
#   make            等价于 make dist（四个平台的静态二进制 + 校验和）
#   make build      只编本机架构
#   make dist       交叉编译 linux/amd64、linux/arm64、linux/armv7、linux/386
#   make test       跑全部测试
#   make check      gofmt + go vet + test（提交前自检）
#   make clean      清掉 dist/ 与本机二进制
#
# 所有产物都是 CGO_ENABLED=0 的静态二进制，可直接拷到目标机器运行。

APP      := lanbox
CMD      := ./cmd/lanbox
DIST     := dist
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  := -trimpath
LDFLAGS  := -s -w -X main.version=$(VERSION)

# 交叉编译目标：os/arch[/GOARM]
TARGETS  := linux/amd64 linux/arm64 linux/arm/7 linux/386

HOST_OS   := $(shell go env GOOS)
HOST_ARCH := $(shell go env GOARCH)

PREFIX ?= /usr/local

.PHONY: all build dist checksums test vet fmt check run install clean help

all: dist

## build: 编译本机架构到 ./lanbox
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(APP) $(CMD)
	@echo "已生成 ./$(APP)（$(HOST_OS)/$(HOST_ARCH)，版本 $(VERSION)）"

## dist: 交叉编译全部目标到 dist/
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
	done
	@$(MAKE) --no-print-directory checksums

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
	@echo "已安装到 $(DESTDIR)$(PREFIX)/bin/$(APP)"

## clean: 清理构建产物
clean:
	rm -rf $(DIST) $(APP)

## help: 列出可用目标
help:
	@echo "用法: make [目标]"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
