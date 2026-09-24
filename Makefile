.PHONY: build test test-fast test-cover bench vet fmt tidy clean help

# 当前没有 main 包（CLI 入口待重建），build 只做编译检查。
build: ## 编译检查（暂无可执行入口）
	go build ./...

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

clean: ## 清理构建产物
	rm -f creator-agent creator-agent.race

# scripts/dev-sandbox.sh（隔离沙箱，用完即焚）已保留，但它需要可执行入口；
# CLI 重建后把 dev-sandbox / dev-blank 两个 target 加回来即可。

help: ## 显示本帮助
	@awk 'BEGIN {FS = ":.*##"; printf "用法:\n  make <target>\n\ntargets:\n"} /^[a-zA-Z_-]+.*:.*?##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
