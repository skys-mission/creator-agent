# 使用指南

## 两种模式

### 交互模式（REPL / TUI）

```bash
./creator-agent          # TTY 环境进入全屏 TUI（自研终端渲染层，unix）；非 TTY（管道/测试）降级为 dumb readline REPL
```

多轮对话，每轮记住上下文。每次启动默认开新会话；想接着上次的对话用 `-c`（继续最近一次会话），或用 `-r` 在启动时弹出会话选择器挑一个恢复（详见 [session 持久化](#session-持久化)）。

**斜杠命令**（输入 `/` 即实时模糊补全，见下文）：

| 命令 | 作用 | 状态 |
|---|---|---|
| `/help` | 显示命令与键位帮助 | ✅ |
| `/clear` | 清空当前对话历史 | ✅ |
| `/cost` | 累计 token 用量与成本估算 | ✅ |
| `/model [name]` | 运行时切换 profile：无参开 picker，带参直接切（复用对话历史，无需重启；仅 TUI） | ✅ |
| `/models` | 打开 profile 选择器（模糊搜索，列表切换） | ✅ |
| `/mcps` | 列出 / 运行时启用/禁用 MCP server（toggle picker，含连接状态） | ✅ |
| `/agents` | 列出 / 切换 agent 人格（build 全工具 / plan 只读，居中 picker） | ✅ |
| `/variants` | 列出 / 切换当前 profile 的 variant（请求覆盖预设：headers/body/采样参数） | ✅ |
| `/mode [name]` | 查看 / 切换权限模式：无参开 picker，带参直接切（`default`/`trust`/`auto`/`readonly`；立即生效，无需重建） | ✅ |
| `/modes` | 打开权限模式选择器（同 `/mode` 无参） | ✅ |
| `/tools` | 列出可用工具 | ✅ |
| `/changes` | 本次会话改动文件清单 | ✅ |
| `/diff <file>` | 在全屏 diff 视图查看某文件最近一次改动（独立 viewport，可滚动） | ✅ |
| `/themes [dark|light]` | 查看 / 切换配色主题（无参数：列出并在 dark/light 间切换；带参数：设为指定主题） | ✅ |
| `/new` | 开始新会话（旧会话保留，可用 `/sessions` 切回） | ✅ |
| `/sessions` | 列出 / 切换会话（模糊搜索 picker；`/resume` 是别名） | ✅ |
| `/copy` | 将最后一条助手回复写入 `~/.creator/last-reply.md` | ✅ |
| `/rename <title>` | 重命名当前会话（持久化；手动重命名后自动标题生成停止） | ✅ |
| `/pin` | 切换当前会话的 pin 状态（pin 的会话在 `/sessions` 列表置顶） | ✅ |
| `/exit` / `/quit` | 退出 | ✅ |
| `/compact` | 手动压缩当前会话历史：异步调 LLM 把旧消息摘要化（保留 system + 摘要 + 最近若干条），替换并持久化历史；状态栏显示「🗜 Compacting…」 | ✅ |
| `/resume` | 切换会话（`/sessions` 的别名） | ✅ |

**键位（TUI 模式）：**

| 键 | 作用 |
|---|---|
| `Enter` | 提交（审批态下确认选中按钮） |
| `Alt+Enter` | 插入换行（多行输入） |
| `↑` / `↓` | 翻输入历史（输入框有内容时也可翻，按 `↓` 回到最新草稿自动恢复已输入内容）；补全菜单打开时选候选 |
| `Tab` / `Enter` | 补全菜单打开时提交选中命令；其余情况 Enter 提交输入 |
| 鼠标滚轮 | 滚动消息区；在 diff/审批视图中滚动 diff 内容 |
| `Ctrl+P` | 打开命令面板（模糊搜索全部命令，详见下文） |
| `Ctrl+Alt+K` | 打开键位面板（列出全部键位，分组显示，详见下文） |
| `PgUp` / `PgDn` | 滚动消息区 / 滚动审批 diff |
| `Ctrl+U` | 半页上滚消息区 |
| `Ctrl+T` | 展开/折叠最后一条回复的思考区 |
| `Ctrl+E` | 展开/折叠最后一个工具输出 |
| `Ctrl+W` | 删除光标前的一个词 |
| `Ctrl+N` | 输入框空时跳到下一条消息（与 `Ctrl+P` 上一条配对） |
| `Ctrl+Y` | 复制最后一条助手回复全文到剪贴板（OSC 52，需终端支持） |
| `Ctrl+X` | leader 前缀（opencode 风格两段式快捷键，见下文） |
| `Ctrl+C` | 中断当前执行（500ms 内双击退出） |
| `Ctrl+D` / `Esc` | 退出 |

**命令面板（Ctrl+P）：** 居中模态弹窗，列出全部斜杠命令及其描述。打字即触发模糊搜索（按命令名优先、描述其次，子序列匹配）。`↑`/`↓` 选择，`Enter` 或 `Tab` 运行，`Esc` 或再次 `Ctrl+P` 关闭。无参命令直接执行；带参命令（`/model`、`/diff`）会把 `/cmd ` 填回主输入框，便于继续输入参数后回车提交。

**键位面板（Ctrl+Alt+K）：** 居中模态弹窗，分组列出全部键位（Editing / Navigation / Session / Commands & Overlays），每行 `键位 ... 作用`。只读浏览——`↑`/`↓`/`PgUp`/`PgDn`/`Home`/`End` 滚动，`Ctrl+Alt+K`/`Esc`/`q` 关闭，其他键忽略（不插入输入）。用于发现不熟悉的快捷键，与 `/help`（仅斜杠命令）互补。

**Ctrl+X leader（两段式快捷键）：** 按 `Ctrl+X` 后再按一个字母触发对应命令（对齐 opencode 的 leader 序列）：

| 序列 | 作用 |
|---|---|
| `Ctrl+X m` | 打开模型 picker（同 `/models`） |
| `Ctrl+X a` | 打开 agent 人格 picker（同 `/agents`） |
| `Ctrl+X t` | 打开主题 picker（同 `/themes`） |
| `Ctrl+X v` | 打开 variant picker（同 `/variants`） |
| `Ctrl+X o` | 打开权限模式 picker（同 `/modes`） |
| `Ctrl+X p` | 打开 MCP server picker（同 `/mcps`） |
| `Ctrl+X n` | 新建会话（同 `/new`） |
| `Ctrl+X l` | 列出/切换会话（同 `/sessions`） |
| `Ctrl+X c` | 压缩当前会话（同 `/compact`） |
| `Ctrl+X k` | 打开键位面板 |
| `Ctrl+X y` | 复制最后一条助手回复 |
| `Ctrl+X e` | 展开/折叠最后一个工具输出 |
| `Ctrl+X h` | 切换思考区显示模式 |
| `Ctrl+X q` | 退出 |

**Home 界面（空会话状态）：** 当当前会话没有任何消息时，主消息区渲染为 home 屏（对齐 opencode 并增强）。从上到下：

- 居中的 `creator-agent` 字标 + 副标题；
- 当前上下文行：`agent:<当前>  ·  <model> @ <host>`（非默认 variant 时附加 `· variant:<名>`）；
- 一条 `● Tip <提示>`——每次进入 home 随机选一条（涵盖 /agents /variants /models /mcps /themes 等本仓库实际可用的命令）；
- **最近会话**（当 store 中存在其他会话时）：列出最近最多 9 条（标题 + Nm/Nh ago + shortID，当前空会话排除）。直接按数字键 `1`-`9` 即可恢复对应会话（opencode `session.quick_switch.1-9` 风格）——该快捷键仅在「idle + 输入框空 + 无补全菜单 + 无消息」的纯 home 态生效，输入任何其他字符即正常进输入框；
- 「Try:」示例提示；
- 底部键位摘要行（Enter 提交 / Alt+Enter 换行 / /help / Ctrl+P 面板 / Ctrl+C 中断）。

发送第一条消息、切换/新建会话时离开 home，下次再回到空会话会重新随机选一条 tip。

**斜杠命令实时补全：** 在输入框首个字符打 `/` 即在输入框上方弹出候选菜单（输入框上方浮窗，最多 10 项），随打字实时模糊过滤（命令名优先、描述其次，子序列匹配，与命令面板同一匹配器）。`↑`/`↓` 选择，`Tab` 或 `Enter` 提交（带参命令如 `/diff` 会自动补一个尾空格，方便接着输参数），`Esc` 关闭。打空格或退格删掉 `/` 时菜单自动关闭。仅在 `/` 位于输入首位时触发。

**文件提及（`@` 补全）：** 在输入框打 `@`（位于首位或空白字符之后）即在输入框上方弹出当前工作目录的候选菜单（最多 10 项，缓存于进程内，首次触发时扫描；跳过 `.git`/`vendor`/`node_modules`/`dist`/`build` 等目录与 `.DS_Store`/`.pyc`/`.log` 等噪声文件）。候选**含子目录**（带尾部 `/` 标记）。随打字实时模糊匹配文件相对路径（子序列匹配）。`↑`/`↓` 选择，`Enter` 提交——把 `@query` 段替换为 `@<相对路径> `（带尾空格，前置文本保留）；`Esc` 关闭。打空格或退格删掉 `@` 时菜单自动关闭。仅当 `@` 位于首位或前置空白字符、且 `@` 到行尾无空白时触发。

**目录 Tab 下钻：** 当选中的候选是**目录**（带 `/` 后缀）时，按 `Tab` 会把 `@query` 重写为 `@<目录>/` 并重新过滤，列出该目录下的子项——可多层下钻到任意深度（对齐 opencode 的 `expandDirectory`）。`Enter` 仍提交目录引用。

**行号区间引用（`@path#range`）：** 提交的 `@<路径>` 可带行号后缀：`@main.go#10`（单行）或 `@main.go#10-20`（10–20 行，含两端，1-indexed）。补全时 `#range` 后缀会被剥离再做路径模糊匹配，所以打 `@main.go#10-20` 仍能匹配到 `main.go`；提交时 `#range` 保留进文本。**提交时内容注入**：对每个 `@path`（或 `@path#range`）引用，会读取该文件（带区间则只取对应行），以 `<path>/<content>` 信封、1-indexed 行号格式追加到「Referenced files:」段，随用户消息一起发给 agent——这样 agent 真能看到引用的文件内容（之前 `@path` 仅是字面文本）。缺失/不可读/超过 64KB 的文件被静默跳过（best-effort，不报错）；目录引用（`@dir/`）不注入内容。注意：不支持 `#L10-20`（带 `L` 前缀）形式，仅 `#N`/`#N-M`/`#N-`。

**全屏 diff 视图：** 由 `/diff <file>` 或审批态按 `d` 打开，覆盖主界面，含独立 viewport（滚动不影响对话位置）。`↑/↓`/`PgUp`/`PgDn` 或鼠标滚轮滚动，`Esc`/`q` 关闭。

**审批（写操作）：** agent 改文件/跑命令前弹审批，含 diff 预览（edit 显示全文件改动，bash 高亮命令）。`←/→` 或 `↑/↓` 在 [本次允许] [本会话都允许] [拒绝] 间切换，`Enter` 确认，`d` 打开全屏 diff 视图查看完整改动，`Ctrl+C` 中断。

**主题：** 内置 `dark`（默认）、`light`、`catppuccin`（深色）三套调色板，另识别 `catppuccin-light`（仅 config 可设，picker 不单列）。启动主题由 config 的 `appearance.theme` 决定（见 [配置参考](config.md)），运行时用 `/themes` 切换——无参数：打开**实时预览 picker**（居中模态列出全部主题，`↑↓` 移动即热替换整屏调色板预览，`Enter` 确认提交、`Esc` 还原到打开时的主题，当前已提交主题用 `•` 标记）；带参数 `/themes light` 等：直接设定（快速路径，不开 picker）。切换/确认后全屏重绘，状态栏右侧显示 `theme:<名>`。预览期间仅在确认时「提交」，`Esc` 完全还原（对齐 opencode 的 live-preview 行为）。

**布局风格：** 采用 Claude Code 风格——每条助手回合/工具调用以单个 `⏺` 起头（颜色编码状态：灰=进行中、绿=完成、红=失败），用户消息为裸文本；输入框由上下两条横线（`─`）包裹 `❯` 提示符与文本，提示信息（模型名/状态/快捷键）置于下横线下方；无侧边栏（会话信息进状态行）。这种「单点 + 无栏填充」结构从根源上避免了宽字符（CJK/emoji）行接缝导致的左移漂移。

**多会话（session）：** 一个进程内可同时存在多个会话，各自独立记忆、互不干扰。会话持久化在 `~/.creator/sessions/<id>.json`（包装格式：title + 更新时间 + 消息历史），重启后保留。
- `/new`：开新会话（生成 `ses_` 开头的时序 ID），旧会话原样保留。
- `/sessions`（或别名 `/resume`）：弹出居中 picker，按最近更新降序列出全部会话，打字模糊过滤（标题优先、ID 其次），`↑↓` 选择、`Enter` 切换、`Esc` 关闭。当前会话用 `•` 标记并置顶。**会话管理**（仅在查询框为空时生效，否则字符进查询框过滤）：
  - `p` 切换选中会话的 pin——pin 的会话用 `★` 标记并自动排在列表顶部（当前会话之后），pin 状态持久化到会话文件、跨重启保留；
  - `Ctrl+R` 重命名——进入行内编辑（输入框变为 `Rename: <旧标题>`，`Enter` 保存、`Esc` 取消，空名视为取消），重命名持久化且不改动 `updatedAt`（不重排顺序）；手动重命名后自动标题生成会自动停止（不再覆盖）；
  - `Ctrl+D` 删除——两次确认：第一次按下选中行变红并提示「按 Ctrl+D 再次删除」，再次按下同会话才真正删除；移动光标或 `Esc` 取消。若删除的是当前会话，会自动开一个新会话（不会让用户停在已删会话上）。
- 切换时：恢复该会话的**文本历史**（用户/助手消息），工具调用卡片是 UI 瞬态、不跨会话恢复。正在执行（stream 中）时不允许切换。
- **自动标题**：会话的**首条用户消息**发出后，后台异步调用当前模型为该会话生成一个简短标题（对齐 opencode 的 title agent：单行、≤100 字符、与用户消息同语言、不含工具名），生成后写入 session 元数据，随即反映在 home 最近会话列表与 `/sessions` picker。生成期间标题保持默认占位（`New session - <时间>`）；生成失败则保持占位（best-effort，不打扰用户）。每个会话只生成一次（仅当标题仍为默认占位时触发）。

**模型/profile 切换：** `/models`（或 `/model` 无参）打开居中 picker，列出 `config.toml` 中配置的全部 profile（名 + `model @ host`），打字模糊过滤，`↑↓` 选择、`Enter` 切换、`Esc` 关闭；当前 profile 用 `•` 标记并置顶。切换复用对话历史、仅重置 token 计数。`/model <name>` 仍可直接文本切换（高级用法）。未配置任何 profile 时给出引导提示。

**MCP server 管理（`/mcps`）：** 打开居中 picker，列出 `config.toml` 中配置的全部 MCP server 及连接状态（`✓ enabled (N tools)` / `○ disabled` / `! failed`）。每个已连接 server 行下方缩进列出其**单个工具**（`✓ toolname` 启用 / `○ toolname` 禁用），可独立开关——禁用某工具即时把它从 agent 的工具集中隐藏（不需断开整个 server）。`↑↓` 在 server 行与 tool 行间统一选择：
- 在 **server 行**：`Enter`/`Space` 切换整个 server（启用/禁用全部工具 + 连接管理），`Tab` 折叠/展开其工具行（默认展开）；
- 在 **tool 行**：`Enter`/`Space` 切换该单个工具（server 被禁用时 tool 行变灰、不可操作）。

切换后自动重建 agent 使工具集生效（复用对话历史）。picker 切换后**不关闭**（可连续调整），`Esc` 关闭。正在执行（stream 中）时不允许切换。未配置 server 时给出引导提示。**连接进度**：启用一个未连接的 server 时，切换在后台异步执行（不冻 TUI），该 server 行显示动画 spinner + `connecting…`（经 braille 转圈），底部提示「connecting… Esc cancels」——按 `Esc`/`Ctrl+C` 可中断正在进行的连接握手。失败则在行内显示 `! failed`。per-tool 与 server 级开关均为内存态（重启回到 config 默认；粗粒度 server 与细粒度 tool 共存，比 opencode 仅 server 级更细）。

**Agent 人格切换（`/agents`）：** 打开居中 picker，列出可选 agent 人格。内置两个：`build`（默认，全工具，可执行）与 `plan`（只读，仅 read/grep/glob/task/skill/todo_write，用于调研与产出方案、不做任何改动；行内标 `[ro]`）。`↑↓` 选择，`Enter` 切换——重建 agent 应用该人格的系统提示词 + 工具白名单（复用对话历史），`Esc` 关闭。当前 agent 用 `•` 标记并置顶。非默认 agent（即 `plan`）会在状态栏右侧显示 `agent:plan`。打字模糊过滤（名字优先、描述其次）。

**Variant 切换（`/variants`）：** variant 是绑定到当前 profile 的「请求覆盖预设」——在 `config.toml` 的 profile 下用 `variants` 声明若干命名预设（headers / body 任意键 / temperature / top_p / max_tokens），运行时挑选其一即可在不改 profile 的前提下调整对模型的请求。`/variants` 打开居中 picker：第一行恒为合成的 `Default`（清除覆盖，回到 profile 默认），其后按字母序列出该 profile 声明的全部 variant（带覆盖摘要，如 `1 header, temp=0.2`）。`↑↓` 选择，`Enter` 切换——重建 provider 应用该 variant 的请求覆盖（复用对话历史），`Esc` 关闭。当前 variant 用 `•` 标记。选中 `Default` 即清除当前 variant。非默认 variant 会在状态栏右侧显示 `variant:<名>`。打字模糊过滤时 `Default` 行恒保留（便于随时重置）。该 profile 没有声明 variant 时给出引导提示。配置示例：

```toml
[profiles.openai]
model = "gpt-4o"
variant = "fast"          # 可选：profile 的默认 variant

[profiles.openai.variants.fast]
temperature = 0.2
max_tokens = 1024
[profiles.openai.variants.fast.headers]
X-Tag = "fast"
[profiles.openai.variants.fast.body]
presence_penalty = 0.5

[profiles.openai.variants.long]
top_p = 0.9
max_tokens = 8192
```

### Headless 模式（一次性）

```bash
./creator-agent "你的 prompt"
./creator-agent -profile openai "用这个 profile 跑"
```

执行一次、打印结果后退出，无多轮记忆（每次独立）。

> **写操作审批**：headless 默认用 `default` 权限模式——`write`/`edit`/`bash` 因无法交互审批而被拒绝（模型转用只读工具回答，或给出可手动执行的命令）。需要让 headless 自动执行写操作，可在 config 设 `permissions.mode: auto`（灾难性操作仍被拦）。详见 [权限模式](config.md#权限模式permission-modes)。

### 权限模式（运行时切换）

交互模式下，输入框底部状态行（`agent · model · <mode>`）始终显示当前权限模式。**`Shift+Tab`** 一键循环（`default → trust → auto → readonly → default`，最顺手）；也可 `/mode [name]` 直接切、`/modes` 或 `Ctrl+X o` 打开选择器。**立即生效、无需重建**。四种模式（`default`/`trust`/`auto`/`readonly`）的行为与决策优先级详见 [config.md · 权限模式](config.md#权限模式permission-modes)。要点：

- **`default`**：每个写操作都问（最稳，默认）
- **`trust`**：工作区内常规写 + 良性命令自动放行，危险操作仍问（日常推荐）
- **`auto`**：几乎全部放行，仅灾难性操作（`rm -rf /` 等）被拦（放手干）
- **`readonly`**：禁止一切写（只看不改）

模式每轮注入系统提示词，模型知道边界但**不能自己切换模式**（只有用户能切）；`auto` 模式下模型也不会建议切换。

## Session 持久化

交互模式（REPL/TUI）的对话历史按会话持久化到 `~/.creator/sessions/<id>.json`。**每次启动默认开一个新会话**，不会自动接上次的对话——这和大多数 AI 工具一致。需要恢复时：

- `creator-agent -c`（或 `--continue`）：直接恢复**最近一个**会话（按更新时间），历史一并载入主视图。
- `creator-agent -r`（或 `--resume`）：启动后弹出会话选择器（picker），手动挑要恢复的会话；列表里能看到历史上所有会话（含旧的 `repl` 会话）。
- 运行中也可用 `/sessions`、`/resume` 切换，或在首页按 `1-9` 快速恢复最近会话。

无任何历史会话时，`-c`/`-r` 均降级为开新会话，不报错。`/clear` 清空当前会话内存历史（session 文件下次保存时覆盖）。Headless 模式（带位置参数）是无状态的，`-c`/`-r` 会被忽略。

## 中断行为

- `Ctrl+C` 中断当前 agent 执行 → 优雅收尾（发 `FinishCanceled`），已完成的工具调用结果保留。
- 步数上限（默认 25）达到 → `FinishStepLimit`，agent 停止。
- 正常完成（模型不再调工具）→ `FinishStop`。

## 多模型切换

配置多个 profile，用 `-profile` 指定：

```bash
./creator-agent -profile deepseek   # 用 deepseek profile
./creator-agent -profile openai     # 切到 openai
```

或用环境变量临时覆盖（见 [config.md](config.md)）。TUI 模式下可用 `/model <name>` 运行时切换（仅支持 openai 兼容 profile，复用对话历史）。

## 下一步

- [配置参考](config.md) — 所有 config.toml 字段
- [工具参考](tools.md) — 内置工具 + subagent + skills
- [架构](architecture.md) — agent loop / 防腐层 / middleware
