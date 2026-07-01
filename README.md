# creator-agent

> 开源、多模型中立的 AI coding agent。Go 单二进制，开箱即用。

[![CI](https://github.com/skys-mission/creator-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/skys-mission/creator-agent/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev/doc/install)
[![License](https://img.shields.io/github/license/skys-mission/creator-agent?color=blue)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/skys-mission/creator-agent)](https://goreportcard.com/report/github.com/skys-mission/creator-agent)

[快速开始](#快速开始) · [配置](#配置) · [工具](#工具) · [架构](#架构) · [文档](docs/) · [贡献](CONTRIBUTING.md)

---

## 为什么用 creator-agent

- 🚀 **单二进制**：Go 编译，零运行时依赖——不用装 Node / Bun / Python
- 🌐 **多模型中立**：OpenAI / DeepSeek / 通义 / 智谱 / Kimi / ollama……任何 OpenAI 兼容端点，不绑厂商
- 💬 **交互式 REPL/TUI**：多轮对话、记住上下文、流式输出、`Ctrl+C` 中断
- 🔧 **完整工具集**：`read` / `write` / `edit` / `bash` / `grep` / `glob` + `task`（subagent）+ `skill`（Skills 系统）+ `todo_write`（任务进度跟踪）
- 🛡️ **安全可控**：4 种权限模式（`default` / `trust` / `auto` / `readonly`，运行时 `/mode` 切换）+ allow/deny 规则（bash 复合命令拆分）+ opt-in OS 沙箱（macOS sandbox-exec / Linux bwrap）
- 🧠 **长对话压缩**：三层 compact（micro / auto / reactive），上下文不会爆
- ♻️ **错误自愈**：瞬时错误（429/5xx）自动指数退避重试 + 用户可读的错误分类
- 🪝 **可扩展**：用户钩子（Pre/PostToolUse/Stop）+ MCP client + Skills 系统

> 要求：Go 1.25+（构建时）。运行时零依赖（单二进制）。

## 快速开始

**安装（任选其一）：**

```bash
# 从源码
git clone https://github.com/skys-mission/creator-agent
cd creator-agent
make build

# 或 go install
go install github.com/skys-mission/creator-agent/cmd/creator-agent@latest
```

**首次运行**：无配置文件 + 无 `OPENAI_API_KEY` + 无 `-api-key` 时，TTY 下启动**全屏初始化向导**（选服务商 → 填 base URL/key/model → 确认写入 `config.toml`，路径为 `$XDG_CONFIG_HOME/creator/config.toml`，未设置 XDG 时为 `~/.creator/config.toml`）；非 TTY 环境打印文本引导并退出。忘填 key 直接跑也不会拿到 raw 401——会提示去哪改哪行。

**配置**（模板字段示例，TOML）：

```toml
default = "deepseek"

[profiles.deepseek]
type = "openai"            # provider 类型：openai（默认，OpenAI 兼容）/ openai-responses / anthropic
base_url = "https://api.deepseek.com"
api_key = "sk-xxx"
model = "deepseek-chat"

[profiles.openai]
type = "openai"
base_url = "https://api.openai.com/v1"
api_key = "sk-xxx"
model = "gpt-4o-mini"
```

或用环境变量：`OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL` / `CREATOR_AGENT_TYPE` / `CREATOR_AGENT_REQUEST_TIMEOUT`

**运行：**

```bash
./creator-agent                          # 交互模式（多轮对话）
./creator-agent "读 README 并总结"        # headless 一次性
./creator-agent -profile openai "..."    # 指定 profile
```

## 配置

优先级（高 → 低）：**flag > 环境变量 > 项目 `./.creator/config.toml` > 全局 `config.toml`（`$XDG_CONFIG_HOME/creator/` 或 `~/.creator/`） > 默认**

全局与项目配置按字段**深合并**（项目覆盖全局，未设置的字段继承全局）。

完整字段（profile / permissions / hooks / memory / skills / sandbox / tools）见 [docs/config.md](docs/config.md)。MCP server 用独立的 `mcp.json`（全局 + 项目 `./.creator/mcp.json`），skills 采用 Agent Skills 标准（`<dir>/SKILL.md`）。

## 工具

| 工具 | 说明 | 能力 |
|---|---|---|
| `read` | 读文件内容 | 只读 · 可并发 |
| `write` | 创建/覆盖文件 | 写 |
| `edit` | 精确编辑（`old_string`→`new_string`，唯一匹配） | 写 |
| `bash` | 执行 shell 命令（可注入 sandbox） | 写 |
| `grep` | 正则搜索文件内容 | 只读 · 可并发 |
| `glob` | 文件名匹配（支持 `**`） | 只读 · 可并发 |
| `task` | subagent：派生只读子会话探索，返回结论（上下文压缩） | 写 · 不可中断 |
| `skill` | 按需加载 skill body（Skills 系统，用户开启） | 只读 · 可并发 |
| `todo_write` | 任务进度列表（会话级，多步任务跟踪；免审批） | 写 · 串行 |

工具 fail-closed：不声明能力即视为写操作 + 不可并发。详见 [docs/tools.md](docs/tools.md)。

## 架构

```
cmd/creator-agent/   # CLI 入口（main + repl + headless + setup + tui/ + agents/approve/compact/mcp/tools/title/cleanup/ui）
core/                # 核心库：agent loop / 工具 / 中间件 / 防腐层
  ├── adapters/openai/ # 底层适配（官方 openai-go）—— 防腐层边界
  ├── builtins/      # 内置工具（read/write/edit/bash/grep/glob + task + skill + todo + sandbox）
  ├── mcp/           # MCP client adapter（连外部 MCP server 取工具）
  └── middlewares/   # compaction / permission / hooks / agentsmd / skills / automemory
config/              # 配置加载
```

Core 通过**防腐层**（`adapters/openai`）隔离底层 SDK：对外只暴露自己的接口，换底层不动上层。多 provider 按 `adapters/<vendor>/` 分包，config `type` 字段切换。详见 [docs/architecture.md](docs/architecture.md)。

## 开发

```bash
make build       # 编译二进制
make run         # 编译并进入交互模式
make dev-sandbox # 一键隔离 dev/测试沙箱（自动编译+一次性+用完即焚）
make test        # 跑全部测试（含 race）
make test-cover  # 测试 + 覆盖率（含 race）
make bench       # 基准测试（纯函数热路径）
make vet         # 静态检查
make fmt         # 格式化
make help        # 查看所有命令
```

## License

[Apache License 2.0](LICENSE)
