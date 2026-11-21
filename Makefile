# 纸上打飞机 · 飞牛 fnOS 应用
#
#   make test    跑全部单测
#   make build   交叉编译 linux/amd64 服务端到打包目录
#   make pack    打包 .fpk（自动下载 fnpack）
#   make all     test + pack
#
# 依赖：go、curl、make；打包另需 fnpack（本文件会自动下载 1.2.3）

APP        := planeduel
PKGDIR     := dist/pkg/$(APP)
MANIFEST   := $(PKGDIR)/manifest
VERSION    := $(shell sed -n 's/^version[[:space:]]*=[[:space:]]*//p' $(MANIFEST) | tr -d ' ')
PLATFORM   := $(shell sed -n 's/^platform[[:space:]]*=[[:space:]]*//p' $(MANIFEST) | tr -d ' ')
BINDIR     := $(PKGDIR)/app/$(APP)
BINARY     := $(BINDIR)/$(APP)-server
OUT        := dist/$(APP)_$(PLATFORM)_v$(VERSION).fpk

FNPACK_VER ?= 1.2.3
FNPACK     ?= $(CURDIR)/fnpack
FNPACK_URL ?= https://static2.fnnas.com/fnpack/fnpack-$(FNPACK_VER)-linux-amd64

GOFLAGS    := -trimpath
LDFLAGS    := -s -w

.PHONY: help build test vet fmt run pack fnpack clean all

help: ## 显示可用命令
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## 交叉编译 linux/amd64 服务端，输出到打包目录
	@echo ">> $(APP) v$(VERSION)  linux/amd64"
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/server
	@chmod +x $(BINARY) && ls -lh $(BINARY)

test: ## 跑全部单测（规则引擎 / 房间 / 重连 / 观战 / 昵称）
	go test ./... -count=1

vet: ## 静态检查
	go vet ./...

fmt: ## 代码格式化
	gofmt -l -w ./cmd ./internal

run: ## 本地运行（管理口 http://127.0.0.1:18088）
	go run ./cmd/server -data ./data

fnpack: ## 下载 fnpack 打包工具
	@if [ -x "$(FNPACK)" ]; then \
		echo ">> fnpack 已就绪: $(FNPACK)"; \
	else \
		echo ">> 下载 fnpack $(FNPACK_VER)"; \
		curl -fSL --retry 3 -o "$(FNPACK)" "$(FNPACK_URL)"; \
		chmod +x "$(FNPACK)"; \
	fi

pack: build fnpack ## 打包成 fnOS 安装包 .fpk
	@echo ">> 打包 v$(VERSION) ($(PLATFORM))"
	@cd $(PKGDIR) && "$(FNPACK)" build
	@mv $(PKGDIR)/$(APP).fpk $(OUT)
	@cd dist && (sha256sum $(notdir $(OUT)) > $(notdir $(OUT)).sha256 2>/dev/null || \
		shasum -a 256 $(notdir $(OUT)) > $(notdir $(OUT)).sha256 2>/dev/null || true)
	@ls -lh $(OUT)

all: test pack ## 测试 + 打包

clean: ## 清理构建产物
	@rm -rf bin dist/*.fpk dist/*.sha256 dist/pkg.tar.gz $(BINARY) $(FNPACK)
	@echo ">> 已清理"
