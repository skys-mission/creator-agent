# 工具参考

## 内置工具

| 工具 | 说明 | 能力 | MaxResultChars |
|---|---|---|---|
| `read` | 读文件内容 | 只读 · 可并发 | 20000（硬编码，会落盘） |
| `write` | 创建/覆盖文件 | 写 | — （不落盘） |
| `edit` | 精确编辑（`old_string`→`new_string`，唯一匹配） | 写 | — （不落盘） |
| `bash` | 执行 shell 命令（可注入[沙箱](#bash-沙箱)） | 写 | 20000（可配） |
| `grep` | 正则搜索文件内容 | 只读 · 可并发 | 20000（可配） |
| `glob` | 文件名匹配（支持 `**`） | 只读 · 可并发 | 20000（可配） |
| `task` | subagent：派生只读子会话探索，返回结论 | 写 · 不可中断 | 10000 |
| `skill` | 按 name 加载 skill body（Skills 系统） | 只读 · 可并发 | 10000 |
| `todo_write` | 任务进度列表（会话级） | 写 · 串行 · 免审批 | — |

> `read`/`write`/`edit`/`todo_write` 的能力值是构造时硬编码，**不受配置覆盖**；`bash`/`grep`/`glob`/`task`/`skill` 走[三层继承](config.md)可配。

### Fail-closed 能力声明

工具通过 `ToolInfo` 声明能力，零值 = 最保守：不声明 `ReadOnly` → 视为写；不声明 `ConcurrencySafe` → 视为不可并行；不声明 `MaxResultChars` → 不落盘。

### 并发分批

连续的 `ReadOnly && ConcurrencySafe` 工具并行执行（受 `MaxConcurrency=10` 限制）；写工具 / 不可并行的工具串行执行（作为 fence）。

### 大结果落盘

工具输出超过 `MaxResultChars` 时，loop 层自动落盘到临时文件，回传给模型的是预览（前 2000 字符）+ 文件路径。错误结果（`IsError`）不落盘——业务错误要直接喂给模型。

## task（subagent）

派生一个**只读子会话**执行独立探索任务，返回结论摘要。主 agent 把大块探索委托给子 agent，子 agent 在隔离 context 跑完只返回结论——主 context 不被中间过程污染（subagent = 上下文压缩机制）。

安全护栏：默认禁止递归 task（子会话工具集不含 task）；子会话默认只读（只给 read/grep/glob）；复用主循环 + system prompt（prompt cache 共享）；子会话步数收紧（默认 10）。

输入：`{description: "简述", prompt: "完整指令"}`

## skill（Skills 系统）

Skills 采用 [Agent Skills 标准](https://agentskills.io/specification)：每个 skill 是一个**文件夹**，内含 `SKILL.md`（YAML frontmatter + Markdown 正文），skill 名 = **文件夹名**。

```
refactor-go/
└── SKILL.md
```

```markdown
---
name: refactor-go
description: Guide for refactoring Go code. Use when the user asks to refactor Go code.
allowed-tools: Read Grep Bash(go:*)
---
# Refactoring Go
1. Run go vet first ...
```

frontmatter 字段：`name`/`description` 必需（`description` 同时说明「做什么 + 何时用」）；`allowed-tools` 可选（空格分隔，取每个 token `(` 前的基名映射到内置工具：Read/Write/Edit/Bash/Grep/Glob/Task），用于在该 skill 激活时收紧工具白名单；`license`/`compatibility`/`metadata` 解析保留备用。

**数据流**：Skills middleware 把所有 skill 的 **name + description** 注入 system prompt；正文 **不进** system prompt（防膨胀）。模型决定用某 skill 时调 `skill` 工具（input `{name}`），正文作为 tool result 进上下文；若该 skill 声明了 `allowed-tools`，本轮后续工具调用被限制在白名单内。

**预算**：注入摘要总字节超 `budget`（默认 25000）时降级为 names-only。

**来源**（项目级覆盖同名全局）：全局 `~/.creator/skills/<name>/SKILL.md`；项目 `./.creator/skills/<name>/SKILL.md`（向上查至 .git 根）；以及 config `skills.dirs` 指定的额外目录。

## todo_write（任务进度）

让 agent 在多步任务（≥3 步）中维护任务进度列表，给用户可见性、也帮模型规划。

设计取舍（让能力受限的模型不易出错）：单工具、替换式全量写（每次传完整列表）；schema 极简 `{todos: [{content, status}]}`；status 四态 `planned`/`pending`/`in_progress`/`completed`。

- `planned`：已列入计划但**未承诺执行**（"列计划/找点事做/评估"时全填 planned）
- `pending`：已确认要做，排队中
- `in_progress`：正在做（同一时刻仅一项）
- `completed`：已做完

写操作 + 串行化（避免并发替换竞态）+ **免审批**（会话级内存状态，无文件/命令副作用）。会话级存活，`/clear` 清空。每次 `todo_write` 的返回值即是当前列表的紧凑摘要（`[ ]` 计划/待办 / `[~]` 进行中 / `[x]` 已完成 + 完成进度），作为 tool result 进入上下文，模型与用户据此看到进度。

## MCP 工具

在 `mcp.json` 配置 server 后，启动时连接外部 MCP server，把它的工具适配为 core.Tool（[配置见 config.md](config.md)）。MCP 工具的 `ReadOnly`/`ConcurrencySafe` 默认 fail-closed（协议不携带），可在 `mcp.json` 该 server 的 `tools` 字段按工具名声明覆盖。

## bash 超时

默认 **60s** 超时（可配，硬上限防长命令卡住）。超时杀整个进程组（含 `&` 后台子进程），返回明确提示（`command timed out after 60s and was killed`）让模型知道是时间限制而非偶发失败。输出缓冲上限 1 MiB（超限截断 + 提示；大输出由 loop 层 spill 落盘）。

## bash 沙箱

启用 `sandbox.enabled=true` 后，bash 在 OS 级沙箱内执行：macOS 用 `sandbox-exec`（禁写 cwd 外 + 禁网络），Linux 用 `bwrap`（unshare-all + 只读绑系统目录 + cwd 可写），其他平台/缺二进制时启用报错（fail-closed）。**默认 `sandbox.enabled=false`（NoopSandbox）**。

> ⚠️ 未启用时 bash 以当前用户权限执行，权限层的危险命令检测是 best-effort 启发式（按 `&&`/`||`/`|`/`;` 拆分子命令匹配），无法拦截 flag 变体、命令替换、子 shell 等规避。**生产/不可信 prompt 场景应启用 sandbox**，以 OS 级硬隔离兜底。
