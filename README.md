# creator-agent

> 开源、多模型中立的 AI coding agent。Go 单二进制，开箱即用。

> **状态：从零重建中。** 只有 TUI 渲染层被完整保留（约 19.5k 行，含 30+ 测试文件），旧的 core / config / adapters / middlewares / 工具实现已全部移除。踩坑经验浓缩在 [docs/tui.md](docs/tui.md)。

[文档](docs/) · [贡献](CONTRIBUTING.md)

---

## 现在有什么

| | |
|---|---|
| `kernel/` | 底层组件生命周期框架（[cordis](https://github.com/cordiverse/cordis) 极简版）：组件注册、按名查找、依赖序启动、逆序销毁、全局状态机。零外部依赖，选型记录与七条不变量见 [docs/kernel.md](docs/kernel.md) |
| `contract/` | TUI 消费的全部 API：领域模型（`Message` / `Event` / `ToolInfo` / `SessionStore` / `Agent`）+ 运行时控制面（`Mode` / `AllowSet` / `SandboxController`）+ 少量叶子助手。**不含任何 agent 逻辑** |
| `cmd/creator-agent/tui/` | 自研全屏 TUI，**不依赖 bubbletea / tcell**。含 `tui/terminal/`（termios raw 模式 / alt screen / SIGWINCH / 输入解码）与 `tui/i18n/` |
| `cmd/creator-agent/diag/` | 不可 recover 崩溃（heap corruption 类 `fatal error`）的落盘诊断：`fatal.log` + `trace.log` |
| `docs/tui.md` | 渲染不变量、并发模型、宽字符处理、测试策略、崩溃排查工具链 —— **踩坑经验都在这** |
| `scripts/dev-sandbox.sh` | 隔离测试沙箱（用完即焚，不碰源码仓库 / `~/.creator` / git），待 CLI 入口重建后恢复 `make` target |

## 待重建

core（agent loop / 工具 / 中间件 / 防腐层 adapters）、config 加载、MCP client、CLI 入口（main / repl / headless / setup）。

`contract/` 就是它们要实现的边界：类型形状已经定死，新 core 实现 `contract.Agent` 并把 `Event` 流喂进来即可，TUI 侧零改动。接入面见 [docs/tui.md](docs/tui.md) 第 11 节。

新 core 以 `kernel/` 为地基：每个模块实现 `kernel.Component` 注册进 `kernel.App`，启动顺序与退出销毁由框架保证（见 [docs/kernel.md](docs/kernel.md)）。

## 开发

```bash
make test        # go test -race ./...   ← 提交前门槛，CI 跑这个
make test-fast   # go test ./...         ← 本地迭代
make build       # 构建 creator-agent 二进制 + 全仓编译检查
make vet         # go vet ./...
make fmt         # gofmt -s -w .
make bench       # 纯函数热路径基准
make help        # 所有命令
```

要求 **Go 1.27+**（构建时）。运行时零依赖。

提交前确保 `make test` + `make vet` + `make fmt` 全绿。

**改 TUI 渲染逻辑前必读** [docs/tui.md](docs/tui.md) 第 4 节的三条渲染不变量——每条都来自一次真实 bug：

1. 每个 `drawXXX` 必须完全填充其矩形区域
2. 授权块按钮必须锚定到块底部可见
3. panic 恢复时禁用鼠标捕获必须先于退出 alt screen

## License

[Apache License 2.0](LICENSE)
