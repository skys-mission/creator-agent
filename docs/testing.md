# 测试

creator-agent 的测试分三层，由"最快最确定"到"最接近真实"，逐层验证。

## 1. 单元测试（默认，零网络）

```bash
go test ./...                 # 全部单元测试
go test -race ./...           # 加竞态检测（推荐本地跑）
go test -cover ./...          # 覆盖率
```

单元测试**全部 mock**，不连任何真实模型 API、不碰网络、不需要 key。CI 跑的就是这一层（`go test -race`）。

覆盖率目标（当前）：

| 包 | 覆盖率 | 说明 |
|----|--------|------|
| `cmd/creator-agent/tui` | ~57% | TUI 状态机 + 渲染 |
| `core/middlewares` | ~89% | 拦截层 |
| `core/adapters/openai` | ~89% | provider adapter（其余靠真实 API 测） |
| `core/builtins` | ~84% | 内置工具 |
| `core` | ~79% | 核心 loop / session / 工具执行 |
| `config` | ~84% | 配置加载 |
| `core/mcp` | ~89% | MCP client adapter（真传输路径靠真实 server 测） |
| `cmd/creator-agent` | ~44% | CLI 装配 / headless / dumb REPL（TTY/网络分支难单测） |
| `cmd/creator-agent/tui/terminal` | ~22% | 输入字节流解码（rawinput.go，含 esc-timeout）走单测；termios/平台 raw 输入仍靠真实终端验证 |

### TUI 测试策略（重点）

TUI 是最难测的部分（依赖真 TTY、终端尺寸、异步事件）。本项目用**纯函数抽取 + 状态断言 + 自建 Screen 的 back-buffer 检查**，全部走单元测试，不上人工：

- `handleKey`：穷尽按键分支（Enter 提交、↑↓ 翻历史、Tab 补全、Ctrl+C 中断/退出、审批按钮 ←/→、Esc 退出、Ctrl+Y 复制、滚动键、diff overlay 开关/滚动）。通过 `NewEventKey(...)` 构造事件喂给 `handleTcellEvent`。
- `handleSlashCommand`：斜杠命令分派（`/clear` `/model` `/models` `/new` `/sessions` `/agents` `/variants` `/mode` `/modes` `/mcps` `/themes` 等均有专门 `*_picker_test.go` / `session_test.go` 用例覆盖各 picker 的 toggle/导航/参数分支）。
- `handleEvent`：每个 `core.Event`（文本/thinking/工具开始/工具增量/工具结果/usage/finish/error）。
- 渲染：构造 `App`（backed by 自建 `Screen`），`render(a)` 后用 `GetContents()`/`GetCursor()` 检查 back buffer 的 cell 内容与光标位置（不需真 TTY）。
- 自研渲染层：`Screen.Present()` 的输出字节级断言（CUP 序列、SGR、宽字符 follow-cell 跳过、增量 diff），`wrapStyledLine` 的换行，`inputBuffer` 的 rune/display-width。
- `startStream`：mock agent 驱动正常流 / error 流 / ctx 取消三条路径。
- history：`saveHistory`/`loadHistory`/`appendHistory` 往返 + 损坏降级 + 限长。
- 交互细节：diff overlay 开关/滚动、OSC 52 复制序列格式、布局迁移后无遗留 ASCII 边框/右侧 todo 面板（回归断言）、输入框光标位置。

测 TUI 不需要真终端——`Screen` 的双缓冲（front/back）天然支持单测读 back buffer。`App` 是单 goroutine 持有的可变状态（事件循环 `runLoop` 独占），所有外部事件（键鼠/resize/agent stream/spinner）通过 channel 转发进 `a.events`，handler 直接改 `App` 字段后 `render(a)` 刷屏。

## 2. 真实 API 集成测试（opt-in，需 key）

这一层用**真实模型 API** 端到端验证，覆盖 mock 测不到的：流式解析真 chunk、provider/loop/工具的完整协同、session 多轮记忆。

### 安全机制（key 不进 git）

双重门控，默认 `go test ./...` 和 CI 完全不编译这些测试：

1. **build tag**：每个文件首行 `//go:build integration`。不加 `-tags=integration` 根本不参与编译。
2. **env 门控**：测试开头读 `CREATOR_AGENT_TEST_API_KEY`，为空 → `t.Skip` 跳过（不发任何网络请求）。

```bash
# 默认：零网络（integration 测试不编译）
go test ./...                              # ✅ 不联网

# 加 tag 但没 key：全部 Skip（零网络）
go test -tags=integration ./...            # ✅ 不联网（SKIP）

# 设 key + 加 tag：才真正打 API
export CREATOR_AGENT_TEST_API_KEY=sk-...   # 你的 key
export CREATOR_AGENT_TEST_BASE_URL=https://api.deepseek.com  # 可选，默认 deepseek
export CREATOR_AGENT_TEST_MODEL=deepseek-v4-flash           # 可选
go test -tags=integration -v ./... -run Real # 🔵 真联网
```

源码里 grep 不到任何 key 字面量。**测试 key 由开发者自行准备，写在不进 git 的本地文件里。**

### 环境变量

| 变量 | 必填 | 默认 | 说明 |
|------|------|------|------|
| `CREATOR_AGENT_TEST_API_KEY` | 是 | — | API key（为空则 Skip） |
| `CREATOR_AGENT_TEST_BASE_URL` | 否 | `https://api.deepseek.com` | OpenAI 兼容端点 |
| `CREATOR_AGENT_TEST_MODEL` | 否 | `deepseek-v4-flash` | 模型名 |

换模型只改 env，代码零改动（OpenAI 兼容通用）：OpenAI 官方 / 智谱 / Kimi / 通义 / ollama 等皆可。

### 测试清单

**provider 层**（`core/adapters/openai/integration_realapi_test.go`）—— adapter 流式解析：

| 测试 | 验证 |
|------|------|
| `TestRealStreamBasicText` | 基础流式文本输出、无 MError |
| `TestRealStreamUsage` | usage 统计（token > 0） |
| `TestRealStreamReasoning` | reasoning_content（thinking 增量）解析 |
| `TestRealStreamMultiTurn` | 多轮上下文，智能断言回答含植入信息 |
| `TestRealStreamBadKey` | 错误 key → ClientError(4xx) 分类 |

**agent 层**（`cmd/creator-agent/integration_realapi_test.go`）—— ★ 长程核心：

| 测试 | 验证 |
|------|------|
| `TestRealAgentReadsFile` | **完整 loop**：模型决定调 read → 执行真文件读 → 回填 → 二轮回答。哨兵串断言 |
| `TestRealAgentMultiTurnSession` | session 多轮记忆（SessionStore），第二轮回忆第一轮信息 |
| `TestRealAgentDeterministicMath` | 确定性智能断言（12+8=20） |
| `TestRealAgentFinishReason` | 正常结束（非 step_limit/error） |
| `TestRealAgentUsageRecorded` | 长程跑完有 usage 事件 |
| `TestRealAgentMissingFile` | 读不存在文件 → 工具 IsError → 模型优雅回应 |
| `TestRealTodoWrite` | todo_write 工具在真实 loop 里的写入与回读 |

### 智能断言说明

- **哨兵串**：`ReadsFile` 在文件里埋唯一随机串（如 `ZEBRA-TANGO-MANGO-7291`），模型不可能瞎猜中。回答含此串 = 真读了文件。
- **确定性数学**：`DeterministicMath` 用有唯一正确答案的题（12+8=20），断言含正确数字，比开放式问答可靠。
- **session 记忆**：`MultiTurnSession` 第一轮植入密码，第二轮问，断言回忆正确——验证 `SessionStore` 跨 `Stream` 调用的记忆。

## 3. 诊断

```bash
CREATOR_AGENT_DEBUG=1 ./creator-agent   # 每轮发给模型的消息 + 工具调用打到 stderr
```

集成测试失败时设 `CREATOR_AGENT_DEBUG=1` 能看到完整的消息序列和工具调用，定位是 adapter 还是 loop 的问题。

### 崩溃/花屏排查工具链

TUI 崩溃或渲染异常难复现时，按以下工具逐步定位（均不改业务代码；TUI 自身崩溃日志在 `~/.creator/tui-crash.log`）：

| 工具 | 命令 | 能发现 |
|------|------|--------|
| race | `go test -race ./...` / `go run -race ./cmd/creator-agent` | 数据竞争（并发读写同一内存） |
| efence | `GODEBUG=efence=1 ./creator-agent` | use-after-free（电子围栏，每页一个分配，释放即不可写） |
| checkptr | `go run -gcflags=all=-d=checkptr ./cmd/creator-agent` | `unsafe.Pointer` 越界 / 对齐错误 |

注意：Go 1.25 的 `mallocgcSmallNoscan` 分配路径与 efence 不完全兼容，efence 下偶发的 SIGBUS 多属误报；checkptr 改变分配时序，常能把普通版必崩的路径变成"不崩但暴露逻辑 bug"，适合定位渲染类问题。

## 给 AI 编码助手的提示

- 改 `core` / adapter 后：跑 `go test -race ./...`（单测）+ `go test -tags=integration ./... -run Real`（真实，需 key）。
- 改 TUI：`cmd/creator-agent/tui` 的测试是纯单元测试，加新分支时在对应的 `*_test.go` 补对应用例（渲染相关用自建 `Screen` 的 `GetContents`/`GetCursor` 检查 back buffer）。
- **永远不要**把测试 key 写进 `.go` / `.md` / config.toml / CI。key 只在环境变量和本地不进 git 的文件。
- 新增真实测试：文件首行加 `//go:build integration`，开头用 `requireReal`/`requireEnv` 门控。
