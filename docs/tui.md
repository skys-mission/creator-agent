# TUI 渲染层：架构与踩坑经验

本仓库正处于从零重建阶段：旧的 core / config / adapters / middlewares / 工具实现已全部移除。TUI 起初被完整保留（活代码 + 全部单测），**后按重构计划砍到"输入框 + `/model-new`"的极简外壳**（砍了什么、怎么找回、重建顺序见 [docs/tui-cut.md](docs/tui-cut.md)）。本文的价值不受影响：砍掉的代码都在 git 历史与 `attic/tui-legacy/` 备份里，重建哪块就按本文哪节的不变量回来——这一层踩过的坑最多，而且每个坑都固化成了渲染不变量和测试断言。

本文是那段经验的浓缩。**改 TUI 之前必读**，尤其第 4 节的三条不变量。

## 1. 为什么单独留着它

TUI（砍掉前约 19.5k 行）是自研的全屏渲染层，**不依赖 bubbletea / tcell 等框架**。依赖面已经收敛到 `contract/` 包（纯类型 + 接口 + 少量叶子助手），不含任何 agent 逻辑，因此可以脱离 core 独立编译、独立测试。

重建 core 时，`contract/` 就是它要实现的边界：`Agent` / `Event` / `Message` / `ToolInfo` / `SessionStore` 等类型的形状已经定死，TUI 只认这些。

## 2. 分层

```
cmd/creator-agent/tui/
├── terminal/          平台层：termios raw 模式、alt screen、SIGWINCH resize、输入解码
│   ├── terminal.go + terminal_{darwin,linux,unix,windows}.go
│   ├── rawinput.go    PumpInput 读 stdin 投 channel；Decode 做 xterm 兼容输入解码
│   └── stderr_redirect*.go   把 stderr 重定向到日志，避免污染 alt screen
├── style.go           Color（RGB 打包进 uint32，ColorDefault 哨兵）+ Style + SGR 序列生成（增量 diff）
├── screen.go          双缓冲 cell 网格（front/back）+ 坐标守恒的 Surface 抽象 + Present()
├── theme.go           调色板 + 主题切换（palettePtr 原子指针）+ Style 构造器
├── markdown.go        轻量 markdown → styledLine 渲染（按 theme+src 缓存）
├── render.go          渲染骨架
├── layout_claude.go   Claude Code 风格的全部视觉（点引导消息块、横线包裹输入框）
├── state.go           App 单 goroutine 状态
├── loop.go            事件循环（runLoop / startStream）
├── input.go keys.go keybindings.go   输入缓冲、按键解析、键位
├── picker*.go approve*.go wizard*.go commands.go   各类 overlay / 选择器 / 审批 / 引导
├── i18n/              中英文字典
└── terminal_bridge.go 把 terminal 原语 re-export 成 tui 包内别名
```

`Present()`（`Sync` 全量 / `Show` 增量）按 run 输出——run 起点发 CUP，run 内顺序写靠终端光标推进，样式边界重新 CUP。

## 3. 并发模型

**单 goroutine（`runLoop`）独占 `App` 状态**；键鼠 / resize / agent stream / spinner 全部 channel 转发进 `a.events`，handler 改状态后 `render(a)` 刷屏（默认增量 `Show`，启动 / resize / overlay 切换才全量 `Sync`）。

这条不要破坏。历史上所有"状态被并发改坏"的问题都靠这个模型天然免疫；一旦引入额外 goroutine 直接改 `App` 字段，`-race` 会红，但更糟的是不红的语义错乱。

## 4. 渲染不变量与已知陷阱

**以下每条都来自一次真实 bug 修复，是渲染层必须遵守的不变量。** 新增任何 `drawXXX` 都要对着这三条自查。

### 4.1 每个 DrawXXX 必须完全填充其矩形区域

双缓冲的 `back` buffer **不每帧重置**，`Show()` 只输出本帧改动的 cell。绘制函数若不擦背景，上一帧残留字形会从短行处渗透。

> 真实事故：授权块的文件路径与按钮被 prompt footer 的 `"Tab … ^P …"` 串污染。

`drawInputBox*` / `drawApprovalBlock*` 都必须先 `fillRect` 清空整块，再画内容。**画任何块之前先问：这块上一帧可能画过什么？**

### 4.2 授权块的操作按钮必须锚定到块底部可见

`renderApprovalLines` 的行序是 `[📄path, diff…, prompt, buttons]`，write/edit 的 `fullFileDiff` 对整文件生成 diff。授权块高度被 cap 到 `h-4`：

> 真实事故：若从顶往下画、超出即截断，大文件 diff 会把末尾的 `Allow/Deny` 挤出屏幕——用户看到一墙 diff 却无法操作，表现为"卡在授权"。

`drawApprovalLinesAnchored` 把 prompt + buttons 锚定底部，diff 溢出时保留头部（path + diff 开头）与尾部、丢弃中段。完整 diff 仍可按 `d` 在 overlay 查看。

**已知未完成的改进**：中段 diff 被直接丢弃仍不友好，计划改为可滚动 diff 区 + 固定底部按钮。动这块之前先补测试锁住"按钮始终可见"。

### 4.3 panic 恢复时，鼠标捕获禁用必须先于 alt screen 退出

`recoverMain` 的恢复序列若漏掉 `\x1b[?1006l\x1b[?1000l`，崩溃后终端仍处鼠标捕获态，继续往父 shell 喷 SGR 鼠标报告（`CSI <btn;col;row M/m`）变成乱码。

顺序集中在 `terminal.Restore`，固定为「**禁鼠标 → 显光标 → 退 alt screen**」。改恢复逻辑时不要重排。

## 5. 宽字符（CJK / emoji）

宽字符占两个 cell：`screen.go` 的 buffer 里 follow cell 自动填充空格，但**输出时跳过**（终端自己按宽度推进光标）。换行由 `wrapStyledLine` 做宽字符感知换行，`truncateStrW` 按显示宽度截断。

这是最容易出"错位 / 重影 / 光标漂移"的地方。改动渲染输出路径时，务必跑 `BenchmarkWrapStyledLineCJK` 相关用例与 `wrap_test.go` / `border_test.go`。

## 6. 主题切换

调色板走 `palettePtr`（`atomic.Pointer`），**无锁 race-free**；`Style` 构造器（`styleText()` 等）运行时读 `pal()`，因此主题切换即时生效，不需要重建任何东西。

markdown 渲染按 `theme+src` 做缓存，主题切换时自动失效。新增任何"渲染结果依赖主题"的缓存时，**必须带上 theme 维度**，否则切换主题后出现旧配色残留。

## 7. 测试策略（不需要真终端）

TUI 是最难测的部分（依赖真 TTY、终端尺寸、异步事件）。本项目用**纯函数抽取 + 状态断言 + 自建 Screen 的 back-buffer 检查**，全部走单元测试，不上人工。

- `handleKey`：穷尽按键分支（Enter 提交、↑↓ 翻历史、Tab 补全、Ctrl+C 中断/退出、审批按钮 ←/→、Esc 退出、Ctrl+Y 复制、滚动键、diff overlay 开关/滚动）。通过 `NewEventKey(...)` 构造事件喂给 `handleTcellEvent`。
- `handleSlashCommand`：斜杠命令分派（`/clear` `/model` `/models` `/new` `/sessions` `/agents` `/variants` `/mode` `/modes` `/mcps` `/themes` 等均有专门 `*_picker_test.go` 用例覆盖各 picker 的 toggle/导航/参数分支）。
- `handleEvent`：每个 `contract.Event`（文本 / thinking / 工具开始 / 工具增量 / 工具结果 / usage / finish / error）。
- 渲染：构造 `App`（backed by 自建 `Screen`），`render(a)` 后用 `GetContents()` / `GetCursor()` 检查 back buffer 的 cell 内容与光标位置。
- 自研渲染层：`Screen.Present()` 的输出字节级断言（CUP 序列、SGR、宽字符 follow-cell 跳过、增量 diff）、`wrapStyledLine` 换行、`inputBuffer` 的 rune / display-width。
- `startStream`：mock agent 驱动正常流 / error 流 / ctx 取消三条路径。
- history：`saveHistory` / `loadHistory` / `appendHistory` 往返 + 损坏降级 + 限长。
- 交互细节：diff overlay 开关/滚动、OSC 52 复制序列格式、布局迁移后无遗留 ASCII 边框/右侧 todo 面板（回归断言）、输入框光标位置。

**关键**：`Screen` 的双缓冲（front/back）天然支持单测读 back buffer，所以测 TUI 不需要真终端。加新渲染分支时，用 back-buffer 断言补对应用例，而不是"跑起来看看"。

### 通用测试纪律（重建时继续沿用）

- **真实 API 测试双重门控**：文件首行 `//go:build integration` + 环境变量 `CREATOR_AGENT_TEST_API_KEY`（为空 `t.Skip`）。默认 `go test ./...` 和 CI 完全不编译这些测试。
- **智能断言优于开放式判断**：埋唯一哨兵串（模型不可能猜中）验证"真读了文件"；用确定性数学题验证输出；用跨轮记忆验证 session 持久化。
- **永远不要**把 key 写进 `.go` / `.md` / config 模板 / CI。key 只在环境变量和不进 git 的本地文件。
- 沙箱写隔离冒烟用独立 build tag（`sandboxsmoke`）门控，真正调用 `sandbox-exec` / `bwrap` 断言"cwd 内可写、cwd 外被拒"；二进制缺失时 `t.Skip`，不误报。

## 8. 平台兼容

平台文件是 `terminal_{darwin,linux,unix,windows}.go` / `stderr_redirect_{darwin,linux}.go` / `bash_{unix,other}.go` 这类后缀切分的，覆盖 termios raw 模式、进程组、原子写、stderr 重定向。

- **Windows 走 REPL 降级**（无全屏 TUI）。
- CI 在 ubuntu 和 macos 上都跑 `-race`，就是因为 darwin 路径必须覆盖。
- 改到平台相关代码时，确认**每个 GOOS 都能编译**（`GOOS=windows go build ./...` / `GOOS=linux go build ./...`）。

## 9. 崩溃 / 花屏排查工具链

TUI 崩溃或渲染异常难复现时按序上这些工具（均不改业务代码）。TUI 自身崩溃日志在 `~/.creator/tui-crash.log`。

| 工具 | 命令 | 能发现 |
|------|------|--------|
| race | `go test -race ./...` / `go run -race ./cmd/creator-agent` | 数据竞争（并发读写同一内存） |
| efence | `GODEBUG=efence=1 ./creator-agent` | use-after-free（电子围栏，每页一个分配，释放即不可写） |
| checkptr | `go run -gcflags=all=-d=checkptr ./cmd/creator-agent` | `unsafe.Pointer` 越界 / 对齐错误 |

两个坑：

- **Go 1.25 的 `mallocgcSmallNoscan` 分配路径与 efence 不完全兼容**，efence 下偶发 SIGBUS 多属误报。（工具链已升 Go 1.27，此条为 1.25 时代记录，未重新验证。）
- **checkptr 改变分配时序**，常能把"普通版必崩"变成"不崩但暴露逻辑 bug"，反而适合定位渲染类问题。

### `cmd/creator-agent/diag`：不可 recover 的崩溃专用

内存损坏类 fatal（`found bad pointer in Go heap`、`unexpected signal during runtime execution`）由 `runtime.throw` 抛出，**绕过所有 defer 和 recover**，进程内根本抓不住。唯一能事后定位的办法是**提前把面包屑落盘**：

- `fatal.log`：runtime 的完整崩溃报告（所有 goroutine 栈），经 `debug.SetCrashOutput` 接管。
- `trace.log`：粗粒度生命周期面包屑（每轮 / 每工具 / 每 stream），**无缓冲写入**，所以尾部永远反映死亡瞬间（进程崩溃不会丢已写行，只有断电会）。

两者都在数据目录（`~/.creator`）下，`Setup()` 时做一次大小轮转（超 2MiB 转成 `.1`）以限制磁盘占用。`Trace()` 单次无缓冲写、并发安全、诊断关闭时是 no-op，**保持消息短且粗粒度，别写 per-token**，否则压垮热路径。

## 10. 已知开放问题

**`write` 工具触发时偶发 `fatal error: found bad pointer`（heap corruption，不可 recover）。**

已排除本仓库代码：无 `unsafe`、`-race` 干净、`-gcflags=all=-d=checkptr` 无报告。疑为 Go 1.25 runtime 在该路径的分配问题。当时受依赖限制（mcp-go 要求 Go ≥ 1.25.5）无法降级 Go 复现定位。**工具链已升 Go 1.27.0（go.mod `go 1.27.0`），升级后未复现验证；若再现仍按本节路径排查。**

重建时如果这个复现了，**别再从头排查业务逻辑**，直接走第 9 节工具链 + `diag` 的 `fatal.log` / `trace.log`。缩小复现面后再决定是否上报 Go runtime。

## 11. 接入重建后的 core

TUI 通过 `tui.Run*` 的 option 注入依赖（见 `run.go`），全部面向 `contract/` 的接口：

| 注入项 | 类型 | 说明 |
|---|---|---|
| agent 工厂 | `func(AskResolver) (contract.Agent, context.CancelFunc, error)` | 每次工具集/模式变更可重建 |
| 会话存储 | `contract.SessionStore` | 缺省 `contract.NewMemoryStore()` |
| 权限模式 | `*contract.ModeController` | 运行时 `/mode` 切换，无需重建 |
| 审批回调 | `contract.AskResolver` | 阻塞至用户 Allow/Deny；ctx 取消时返回 deny |
| 沙箱覆盖 | `*contract.SandboxController` | 运行时 `/sandbox` 切换 |
| 模型/变体列表 | `map[string]contract.Profile` | `/model` `/variants` picker 数据源 |
| 标题生成 | `tui.TitleGenerator` | 异步生成会话标题 |
| 压缩 | `tui.Compactor` | 压缩已存历史 |
| MCP | `tui.MCPManager` | 仅暴露状态，不暴露 SDK 类型 |

新 core 落地时，实现 `contract.Agent` 并把 `Event` 流喂进来即可，TUI 侧零改动。工具能力声明（`contract.ToolInfo`）是 fail-closed 的：不声明 `ReadOnly` 视为写、不声明 `ConcurrencySafe` 视为不可并发——保持这个语义。
