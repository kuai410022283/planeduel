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
BINPREFIX  := $(BINDIR)/$(APP)-server
OUT        := dist/$(APP)_$(PLATFORM)_v$(VERSION).fpk

FNPACK_VER ?= 1.2.3
FNPACK     ?= $(CURDIR)/fnpack
FNPACK_URL ?= https://static2.fnnas.com/fnpack/fnpack-$(FNPACK_VER)-linux-amd64

ARCHS      := amd64:x86_64 arm64:aarch64
GOFLAGS    := -trimpath
LDFLAGS    := -s -w

.PHONY: help build build-amd64 build-arm64 test vet fmt run pack fnpack clean all

help: ## 显示可用命令
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## 交叉编译 x86_64 + aarch64 两个架构到打包目录（platform = all）
	@mkdir -p $(BINDIR)
	@for pair in $(ARCHS); do \
		arch=$${pair%%:*}; suffix=$${pair##*:}; \
		echo ">> $(APP) v$(VERSION)  linux/$$arch -> planeduel-server-$$suffix"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch \
			go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINDIR)/planeduel-server-$$suffix ./cmd/server || exit 1; \
		chmod +x $(BINDIR)/planeduel-server-$$suffix; \
	done
	@ls -lh $(BINDIR)/planeduel-server-*

build-amd64: ## 只构建 x86_64
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINDIR)/planeduel-server-x86_64 ./cmd/server
	@chmod +x $(BINDIR)/planeduel-server-x86_64 && ls -lh $(BINDIR)/planeduel-server-x86_64

build-arm64: ## 只构建 aarch64
	@mkdir -p $(BINDIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINDIR)/planeduel-server-aarch64 ./cmd/server
	@chmod +x $(BINDIR)/planeduel-server-aarch64 && ls -lh $(BINDIR)/planeduel-server-aarch64

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
	@# Windows 上打出来的包容易丢可执行位，这里统一补上
	@chmod +x $(PKGDIR)/cmd/* $(BINDIR)/$(APP)-server-* 2>/dev/null || true
	@cd $(PKGDIR) && "$(FNPACK)" build
	@mv $(PKGDIR)/$(APP).fpk $(OUT)
	@cd dist && (sha256sum $(notdir $(OUT)) > $(notdir $(OUT)).sha256 2>/dev/null || \
		shasum -a 256 $(notdir $(OUT)) > $(notdir $(OUT)).sha256 2>/dev/null || true)
	@ls -lh $(OUT)

all: test pack ## 测试 + 打包

clean: ## 清理构建产物
	@rm -rf bin dist/*.fpk dist/*.sha256 dist/pkg.tar.gz $(BINPREFIX)-* dist/pkg/$(APP)/app/$(APP)/$(APP)-server $(FNPACK)
	@echo ">> 已清理"
