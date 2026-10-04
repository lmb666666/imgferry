# imgferry 开发与发布常用命令
BINARY := imgferry
PKG    := ./cmd/imgferry
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/lmb666666/imgferry/internal/cli.version=$(VERSION)

.PHONY: build test race vet fmt golden mockup release clean

build: ## 构建二进制（注入版本号）
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test: ## 全量测试
	go test ./...

race: ## 全量测试 + 竞态检测
	go test -race ./...

vet: ## 静态检查
	go vet ./...

fmt: ## 格式化
	gofmt -w .

golden: ## 刷新 TUI 黄金快照
	UPDATE_GOLDEN=1 go test ./internal/tui

mockup: ## 依据本地设计规格刷新可视化评审稿（需要 docs/，该目录不入库）
	python3 docs/gen_mockup.py

release: ## 交叉编译各平台二进制到 dist/
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 $(PKG)
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 $(PKG)
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 $(PKG)
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 $(PKG)
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe $(PKG)
	@ls -lh dist/

clean: ## 清理构建产物
	rm -rf dist $(BINARY)
