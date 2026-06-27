# 贡献指南

感谢你对 creator-agent 的兴趣！这是一个开源的 AI coding agent（Go 单二进制）。本指南帮你快速上手开发。

## 开发环境

- **Go 1.25+**（构建时）。运行时零依赖（单二进制）。
- 无需 Node / Python / 其他运行时。

## 常用命令

```bash
make build       # 编译二进制
make test        # 全部测试（含 race 检测，CI 跑这个）
make test-fast   # 快速测试（无 race，本地迭代用）
make test-cover  # 测试 + 覆盖率
make bench       # 基准测试（纯函数热路径）
make vet         # 静态检查
make fmt         # 格式化（gofmt -s）
make help        # 查看所有命令
```

提交前请确保 `make test`、`make vet`、`make fmt` 全绿。

## 项目结构

```
cmd/creator-agent/   CLI 入口（薄入口，逻辑只在 core）
  └── tui/           自研全屏 TUI 渲染层
core/                核心库（agent loop / 工具 / 中间件 / 防腐层）
  ├── adapters/openai/  防腐层：隔离 openai-go SDK
  ├── builtins/         内置工具（read/write/edit/bash/grep/glob/task/skill/todo）
  ├── mcp/              MCP client adapter
  └── middlewares/      compaction / permission / hooks / agentsmd / skills / automemory
config/              配置加载
docs/                对外文档（usage/config/tools/architecture/testing）
```

**核心原则**：`core` 是库，所有 agent 逻辑只在 core。CLI 只依赖 core API。底层 LLM SDK（openai-go）只活在 `core/adapters/openai/`（防腐层）。

## 测试策略

三层测试，详见 [docs/testing.md](docs/testing.md)：

1. **单元测试**（默认，零网络）：全 mock，CI 跑的就是这层。
2. **真实 API 集成测试**（opt-in，需 key）：build tag `integration` + 环境变量 `CREATOR_AGENT_TEST_API_KEY` 双重门控。
3. **诊断**：`CREATOR_AGENT_DEBUG=1` 看每轮发给模型的消息 + 工具调用。

改 `core` / adapter 后：跑 `make test`。改 TUI：补 `cmd/creator-agent/tui` 的纯单元测试。

## 改动 core / adapter

- 保持防腐层边界：`core` 和 `cmd` 的业务代码零 SDK 直接依赖；SDK 类型只在 `core/adapters/<vendor>/`。
- 边界检查：`grep -rn "openai/openai-go" core/ cmd/ config/ --include="*.go" | grep -v "adapters/openai" | grep -v "_test.go"` 应为空。

## 安全

- **永远不要**把 API key 写进代码 / 文档 / config 模板 / CI。key 只在环境变量和不进 git 的本地文件。
- 权限层（denylist）是 best-effort 启发式；强隔离靠 Sandbox（opt-in）。生产/不可信场景应启用 sandbox。

## 提交

- 遵循 `make test` + `make vet` + `make fmt` 全绿。
- commit message 用约定式格式（`feat:` / `fix:` / `test:` / `docs:` / `perf:` / `refactor:` / `chore:`）。
- 中文 commit message 可接受（项目文档中英混用）。

## License

贡献内容遵循 [Apache 2.0](LICENSE)。
