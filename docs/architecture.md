# 架构

## 目录结构

```
cmd/creator-agent/   # CLI 入口（main + repl + headless + setup + tui/ + agents/approve/compact/mcp/tools/title/cleanup/ui）
core/                # 核心库（agent loop / 工具 / 中间件 / 防腐层）
  ├── adapters/openai/  # 防腐层（唯一碰 openai-go）
  ├── builtins/         # 内置工具（read/write/edit/bash/grep/glob + task + skill + todo + sandbox）
  ├── mcp/              # MCP client adapter（连外部 MCP server 取工具）
  └── middlewares/      # agentsmd / microcompact / permission / summarization / reactive / userhooks / skills / automemory（+ cache/compress/system_inject 为支撑文件）
config/              # 配置加载
```

## 核心原则

**core 是库**：所有 agent 逻辑只在 core，CLI 只是调用 core 的薄入口（不含业务逻辑）。

**防腐层**：openai-go 只活在 `core/adapters/openai/`。core 对外只暴露自己的类型（`Agent` / `Tool` / `ModelProvider` / `Message` / `Event`），不出现 SDK 类型。多 provider 按 `adapters/<vendor>/` 分包，config `type` 字段切换（v0.1 openai）。边界检查：

```bash
grep -rn "openai/openai-go" core/ cmd/ config/ --include="*.go" | grep -v "adapters/openai" | grep -v "_test.go"
# 应为空
```

**自建 agent loop**：迭代 + 单 `RunState`，不信任 `stop_reason`（用"本轮是否产生 tool_use"判退出），步数硬上限（默认 25）。

## Agent loop

```
Stream(ctx, input)
  ├─ load session history（SessionID 非空时）
  ├─ prepend system prompt
  ├─ BeforeAgent middleware（AgentsMd/Skills/AutoMemory 注入；可拒绝）
  └─ RunForked(ctx, cfg, state, ch)   ← 主会话与子会话共用同一循环
       └─ runLoop（迭代至 MaxSteps）
            ├─ 检 ctx（canceled → FinishCanceled）
            ├─ BeforeModel（MicroCompact 清旧 tool_result / Summarization 压缩）
            ├─ runModelTurn（调模型，转发事件，收集 tool_use）
            │   ├─ 瞬时错误（429/5xx）→ 应用级指数退避重试（1s→3s，≤2 次）
            │   └─ 持续错误 → OnError middleware
            ├─ AfterModel
            ├─ 无 tool_use → FinishStop
            └─ executeTools（并发分批 + 大结果落盘）
```

## 错误处理三层

1. **SDK 连接级重试**（openai-go `WithMaxRetries=3`）：建立阶段的瞬时 408/409/429。
2. **应用级退避重试**（`loop.go`）：穿透 SDK 重试的持续限流/服务端波动，对 `RateLimitedError`(429) / `ServerError`(5xx) 在流开始前指数退避（1s→3s，≤2 次）。不重试 ClientError(4xx 非 429)、ctx 取消、未分类错误。
3. **OnError middleware**（`Reactive`）：context-length 类错误 → 激进压缩后重试本轮，带断路器（连续失败 ≥2 次熔断）。

错误最终上报按类型分类（`core.UserHint`，TUI 的 `coreUserHint` 包一层显示提示）：429 提示稍后重试、5xx 提示换 profile、401/403 提示检查配置。

## Middleware

**6-hook 接口**（`BeforeAgent` / `AfterAgent` / `BeforeModel` / `AfterModel` / `WrapTool` / `OnError`）+ `BaseMiddleware` 空实现。拦截/改写走 middleware（callbacks 只是只读观测层）。

装配顺序（外→内），条件项仅在对应配置开启时加入：

1. `AgentsMd` — 注入 AGENTS.md 项目记忆
2. `Skills` — 注入 skill frontmatter 摘要（用户开启）
3. `MicroCompact` — 清旧 tool_result（纯结构重写，零成本）
4. `UserHooks` — 用户 shell 钩子（配置了 hooks 时）
5. `Permission` — 权限模式（`default`/`trust`/`auto`/`readonly`，风险分级阈值）+ allow/deny 规则 + bash 复合拆分；始终装配，运行时 `/mode` 切换无需重建（`ModeController` 共享）
6. `Summarization` — autoCompact（预测式，超阈值触发模型摘要）
7. `Reactive` — 反应式 compact（context-limit 错误兜底，OnError）
8. `AutoMemory` — 会话后记忆提取 + 下次注入（用户开启）

## Session 持久化

`SessionStore` 接口：`MemoryStore`（内存）/ `JSONFileStore`（`~/.creator/sessions/<id>.json`，原子写 + 进程内 mutex + 目录 flock + 路径穿越防护）。REPL/TUI 用 `JSONFileStore`（sessionID 固定 `repl`），跨重启保留对话；headless 无状态。

## 子会话（RunForked）

`core/agent.go` 的 `RunForked` 是 subagent / compact / memory 的共享 forked-runner：复用主循环（cache prefix 共享），`NewForkedConfig` 继承 Model/SystemPrompt/Middlewares、换 Tools、新建独立 MemoryStore。task 工具用它起只读子会话；AutoMemory 用它做记忆提取。

## TUI 渲染层

`cmd/creator-agent/tui/` 是自研的全屏 TUI（不依赖 bubbletea/tcell 等框架）。分层：

- `terminal/`（`terminal.go` + 平台文件 `terminal_{darwin,linux,unix,windows}.go` + `rawinput.go`）：termios raw 模式、alt screen、SIGWINCH resize（unix；Windows 暂走 REPL 降级）；`rawinput.go` 的 `PumpInput` 读 stdin 投 channel，`Decode` 做 xterm 兼容输入解码（方向键/PgUp/Dn/Home/End/Enter/Backspace/Delete/Tab/Esc/Ctrl-\*、Alt+Enter）。`terminal_bridge.go` 把这些原语 re-export 成 tui 包内的别名。
- `style.go`：Color（RGB 打包进 uint32，ColorDefault 哨兵）+ Style（fg/bg/bold/italic）+ SGR 序列生成（增量 diff）。
- `screen.go`：双缓冲 cell 网格（front/back）+ 坐标守恒的 `Surface` 抽象（`Sub` 裁剪、宽字符不溢出边界）。`Present()`（`Sync` 全量 / `Show` 增量）按 run 输出——run 起点发 CUP、run 内顺序写靠终端光标推进、样式边界重新 CUP。宽字符（CJK/emoji）的 follow cell 在 buffer 自动填充空格但输出时跳过（终端按宽度推进）。
- `theme.go`：调色板（`palette`）+ 主题切换（`applyTheme` 经 `palettePtr` 原子指针，无锁 race-free）+ Style 构造器（`styleText()` 等运行时读 `pal()`，主题切换即时生效）。
- `render.go` / `state.go` / `markdown.go` / `layout_claude.go`：渲染骨架、`App` 单 goroutine 状态、轻量 markdown→styledLine 渲染（按 `theme+src` 缓存，主题切换自动失效）+ 宽字符感知换行。`layout_claude.go` 实现 Claude Code 风格的全部视觉（点引导的消息块、横线包裹的输入框）。

并发模型：单 goroutine（`runLoop`）独占 `App` 状态；键鼠/resize/agent stream/spinner 全部 channel 转发进 `a.events`，handler 改状态后 `render(a)` 刷屏（默认增量 `Show`，启动/resize/overlay 切换才全量 `Sync`）。

### 渲染不变量与已知陷阱

以下每条都来自一次真实 bug 修复，是 TUI 渲染层**必须遵守的不变量**：

1. **每个 DrawXXX 必须完全填充其矩形区域**。双缓冲的 `back` buffer 不每帧重置，`Show()` 只输出本帧改动的 cell；绘制函数若不擦背景，上一帧残留字形会从短行处渗透（曾导致授权块的文件路径与按钮被 prompt footer 的 "Tab … ^P …" 串污染）。`drawInputBox*` / `drawApprovalBlock*` 都先 `fillRect` 清空整块再画内容。

2. **授权块的操作按钮必须锚定到块底部可见**。`renderApprovalLines` 行序为 `[📄path, diff…, prompt, buttons]`，write/edit 的 `fullFileDiff` 对整文件生成 diff；授权块高度被 cap 到 `h-4`，若从顶往下画、超出即截断，大文件 diff 会把末尾的 `Allow/Deny` 挤出屏幕——用户看到一墙 diff 却无法操作，表现为"卡在授权"。`drawApprovalLinesAnchored` 把 prompt+buttons 锚定底部，diff 溢出时保留头部（path + diff 开头）与尾部、丢弃中段（完整 diff 仍可按 `d` 在 overlay 查看）。**后续优化**：中段 diff 被直接丢弃仍不友好，计划改为可滚动 diff 区 + 固定底部按钮。

3. **panic 恢复时，鼠标捕获禁用必须先于 alt screen 退出**。`recoverMain` 恢复序列若漏掉 `\x1b[?1006l\x1b[?1000l`，崩溃后终端仍处鼠标捕获态，继续往父 shell 喷 SGR 鼠标报告（`CSI <btn;col;row M/m`）变成乱码。序列集中在 `terminal.Restore`，顺序为「禁鼠标 → 显光标 → 退 alt screen」。

**已知开放问题**：write 工具触发时偶发 `fatal error: found bad pointer`（heap corruption，不可 recover）。已排除本仓库代码（无 unsafe、`-race` 干净、`-gcflags=all=-d=checkptr` 无报告），疑为 Go 1.25 runtime 在该路径的分配问题；受依赖（mcp-go 要求 ≥1.25.5）限制暂无法降级 Go 复现定位。排查工具链见 `docs/testing.md` 诊断章节。

## 开发

```bash
make test        # 全部测试（含 race）
make test-cover  # 覆盖率
make vet         # 静态检查
make fmt         # 格式化
```
