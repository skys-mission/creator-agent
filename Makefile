.PHONY: build test test-fast test-cover bench vet fmt tidy clean help

# 构建一体二进制（默认本机直调交互 / serve 开 gRPC / rpc 待 P4），并保留全仓编译检查。
build: ## 构建 creator-agent 二进制 + 全仓编译检查
	go build -o creator-agent ./cmd/creator-agent
	go build ./...

test: ## 跑全部测试（含 race 检测）
	go test -race ./...

test-fast: ## 快速测试（无 race 检测，本地迭代用）
	go test ./...

test-cover: ## 测试 + 覆盖率（含 race 检测）
	go test -race ./... -cover

bench: ## 跑全部基准测试（纯函数热路径；目前覆盖小，重建时逐块补回）
	go test -run='^$$' -bench=. -benchmem ./...

vet: ## 静态检查
	go vet ./...

fmt: ## 格式化代码
	gofmt -s -w .

tidy: ## 整理依赖
	go mod tidy

clean: ## 清理构建产物
	rm -f creator-agent creator-agent.race

# scripts/dev-sandbox.sh（隔离沙箱，用完即焚）已保留，但它需要可执行入口；
# CLI 重建后把 dev-sandbox / dev-blank 两个 target 加回来即可。

help: ## 显示本帮助
	@awk 'BEGIN {FS = ":.*##"; printf "用法:\n  make <target>\n\ntargets:\n"} /^[a-zA-Z_-]+.*:.*?##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
