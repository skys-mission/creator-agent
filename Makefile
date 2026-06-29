.PHONY: build run test test-cover bench vet fmt tidy install clean dev-sandbox help

BINARY := creator-agent

build: ## 编译二进制到当前目录
	go build -o $(BINARY) ./cmd/creator-agent

run: build ## 编译并进入交互模式
	./$(BINARY)

test: ## 跑全部测试（含 race 检测）
	go test -race ./...

test-fast: ## 快速测试（无 race 检测，本地迭代用）
	go test ./...

test-cover: ## 测试 + 覆盖率（含 race 检测）
	go test -race ./... -cover

bench: ## 跑稳定的基准测试（wrapStyledLine / truncateStrW 等纯函数热路径）
	@# 真实运行时每帧只渲染一次；这里只跑不涉及复杂渲染状态的稳定 bench。
	@# 想看渲染 bench 可手动单跑：go test -run='^$$' -bench=. ./cmd/creator-agent/tui/
	go test -run='^$$' -bench='BenchmarkWrapStyledLine|BenchmarkTruncateStrW' -benchmem ./cmd/creator-agent/tui/

vet: ## 静态检查
	go vet ./...

fmt: ## 格式化代码
	gofmt -s -w .

tidy: ## 整理依赖
	go mod tidy

install: ## 安装到 GOBIN
	go install ./cmd/creator-agent

clean: ## 清理构建产物
	rm -f $(BINARY)

dev-sandbox: ## 一键隔离 dev/测试环境（自动编译 + 一次性沙箱 + 用完即焚；传 prompt: ARGS='-- "hi"')
	@scripts/dev-sandbox.sh $(ARGS)

help: ## 显示本帮助
	@awk 'BEGIN {FS = ":.*##"; printf "用法:\n  make <target>\n\ntargets:\n"} /^[a-zA-Z_-]+.*:.*?##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
