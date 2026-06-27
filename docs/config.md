# 配置参考

配置格式：**TOML**。

配置文件（两层，项目覆盖全局）：

- 全局：`$XDG_CONFIG_HOME/creator/config.toml`（设置了 `XDG_CONFIG_HOME` 时）；否则 `~/.creator/config.toml`。
- 项目级：`./.creator/config.toml`（当前工作目录）。

> 运行期数据（会话、日志、记忆、历史、skills、全局 `AGENTS.md`）始终位于 `~/.creator/`。MCP server 单独用 JSON 配置（见下文「MCP server」）。

**优先级（高 → 低）：** flag > 环境变量 > 项目 `./.creator/config.toml` > 全局 `config.toml` > 默认

**合并语义：** 全局与项目按字段**深合并**——项目里写了的键覆盖全局，没写的字段继承全局。例如项目 profile 只写 `model`，仍会继承全局该 profile 的 `api_key`/`base_url`。表（`[profiles.x]`、`[permissions]`…）按字段递归合并；数组（`allow`/`deny`/`args`…）整体替换。

**首次运行：** 无配置文件且无 API key（`OPENAI_API_KEY`/`-api-key`）时，TTY 下会启动全屏初始化向导（选服务商 → 填 base URL/key/model → 确认写入 `config.toml`）；非 TTY 环境打印文本引导并退出。

未知键（拼写错误）不会导致启动失败，而是打印一条带位置的告警后忽略。文件可声明顶层 `version`（当前 schema 版本 = 1）；高于本程序支持的版本会告警（为未来迁移预留）。

## 完整示例

```toml
# 配置 schema 版本（可选）
version = 1

# 模型 profile（必填一个）
default = "deepseek"

[profiles.deepseek]
type = "openai"              # provider 类型：openai（默认，OpenAI 兼容）/ anthropic（待 v0.2）
base_url = "https://api.deepseek.com"
api_key = "sk-xxx"
model = "deepseek-chat"
request_timeout = "10m"      # 可选，单次流式总超时（如 10m/600s）；空=默认 10m

[profiles.openai]
type = "openai"
base_url = "https://api.openai.com/v1"
api_key = "sk-xxx"
model = "gpt-4o-mini"
variant = "fast"             # 可选，profile 启动时套用的 variant 名（空=合成 "Default" 变体，无 override）

# 命名 variant：一次 LLM 调用时合并的请求 override 预设，运行时用 /variants 切换
[profiles.openai.variants.fast]
temperature = 0.2            # 采样温度 override（不写=不 override）
top_p = 0.9                  # nucleus 采样 override
max_tokens = 1024            # 输出 token 上限 override
[profiles.openai.variants.fast.headers]
X-Tag = "fast"              # 合并到每次 HTTP 请求的 header
[profiles.openai.variants.fast.body]
presence_penalty = 0.5      # 合并到 JSON 请求体的任意键

# 权限模式 + 规则（可选）
[permissions]
mode = "default"            # default | trust | auto | readonly（启动模式；运行时用 /mode 切换）
allow = ["read:*", "bash:git *"]   # 白名单提升
deny = ["bash:rm -rf *"]           # 永远拒绝

# 用户钩子（可选）—— shell 命令在工具调用前后执行
[[hooks.pre_tool_use]]
matcher = "bash"                                   # 匹配 bash 工具
command = "echo '{\"decision\":\"approve\"}'"     # stdin 收 JSON，stdout 返 JSON 决策
[[hooks.post_tool_use]]
matcher = "*"
command = "/path/to/audit.sh"
[[hooks.stop]]
command = "echo done"

# MCP server 不在本文件里配置 —— 见下文「MCP server（mcp.json）」。

# 长期记忆（可选）—— 会话后提取可记忆事实，下次注入
[memory]
enabled = true
dir = "~/.creator/memory"         # 默认

# Skills 系统（可选）—— Agent Skills 标准（文件夹 + SKILL.md），模型驱动按需加载
[skills]
enabled = true
dirs = []                         # 额外扫描目录（默认扫 ~/.creator/skills + 项目 ./.creator/skills）
budget = 25000                    # 注入摘要字节预算

# bash 沙箱（可选，opt-in）—— OS 级隔离
[sandbox]
enabled = false                   # 默认关；启用需 sandbox-exec(macOS)/bwrap(Linux)
mode = "filesystem"               # filesystem（默认）/ strict；保留字段，目前校验但未接线
allow_dirs = []                   # cwd 外额外允许写的目录

# 外观（可选）—— 配色主题 + 界面语言
[appearance]
theme = "dark"                    # dark（默认）| light；启动仅这两值生效（其余主题运行时用 /themes 切换）
language = "en"                   # en（默认）| zh；留空时按 LANG/LC_ALL 自动检测（zh* 为中文）

# 工具配置（可选）—— 三层继承：tools.<name> > tools_defaults > 工具内置默认
[tools_defaults]                  # 通用层：对所有工具生效
max_result_chars = 20000          # 单次工具结果落盘前字符上限
ignore_dirs = [".git", "vendor", "node_modules", "dist", "build"]  # grep/glob 跳过的目录名
max_depth = 20                    # grep/glob 目录遍历深度 / task 子代理递归深度
[tools.grep]
max_matches = 100                 # grep 返回的匹配行数上限
[tools.bash]
timeout = "60s"                   # bash 单命令超时
[tools.task]
max_depth = 2                     # task 子代理递归深度上限
```

## 工具配置（tools_defaults / tools）

三层继承，优先级 **高 → 低**：`tools.<name>` > `tools_defaults` > 工具内置默认。
（MCP server 的能力声明在 `mcp.json` 的 `tools` 字段里，独立于本机制。）

每个字段都是可选的（零值 = 未设置，继承下层）。`ignore_dirs` 在高层设置时**整体替换**而非合并下层。

**重要**：继承机制只对**经配置装配的工具**生效。实际可配的工具与字段：

| 字段 | 作用 | 生效工具 | 内置默认 |
|---|---|---|---|
| `max_result_chars` | 单次工具结果超过则落盘到临时文件，只回传预览 | bash / grep / glob / task / skill | 20000（task/skill 为 10000） |
| `ignore_dirs` | 目录遍历时跳过的目录名（按路径段匹配） | grep / glob | `[.git, vendor, node_modules, dist, build]` |
| `max_depth` | grep/glob 目录遍历深度；task 子代理递归深度 | grep / glob / task | grep·glob 20；task 2 |
| `max_matches` | grep 返回的匹配行数上限 | grep | 100 |
| `timeout` | bash 单命令超时（Go duration 字符串，如 `60s`/`2m`） | bash | 60s |

> `read`/`write`/`edit`/`todo_write` 的能力值是工具构造时硬编码，**不经此继承机制、不可通过配置覆盖**：read 硬编码 `max_result_chars=20000`（会落盘），write/edit/todo_write 不落盘。详见 [tools.md](tools.md)。

### 硬上限（不可配置，防崩溃）

下列维度有代码内硬上限，配置值超过时被自动钳制。数值刻意偏大，正常使用绝不触发，仅在病态输入或配置错误时兜底：

| 维度 | 硬上限 | 含义 |
|---|---|---|
| `max_result_chars` | 2 MiB | 单次工具结果字符上限（约 50 万 tokens 等效，远小于上下文窗口） |
| grep `max_matches` | 10000 | grep 匹配行数上限 |
| glob 路径数 | 10000 | glob 返回路径数上限（防超大仓库内存膨胀） |
| 遍历深度 | 50 | grep/glob 目录遍历深度 |
| task 递归深度 | 10 | 子代理嵌套深度（独立于可配的 `max_depth`） |
| todo_write 条目数 | 200 | 单次写入的 todo 条目数上限（超出的条目被丢弃，防 runaway 列表） |
| todo_write 单条字符 | 1000 | 单条 content 字符上限（按 rune 计，超出截断） |

## Variant（profile 内的请求 override 预设）

`[profiles.<name>.variants.<变体名>]` 是一组命名的请求 override 预设，运行时用 `/variants` 选择器切换（合成 "Default" 变体 = 无 override）。每个 variant 可声明：

| 字段 | 作用 | 合并方式 |
|---|---|---|
| `temperature` / `top_p` / `max_tokens` | 采样参数 override（不写 = 不 override） | 仅当当前轮的 `ModelRequest` 未显式设置时才套用（请求级优先） |
| `headers` | 合并到每次 HTTP 请求的 header | 逐键合并到 HTTP 请求 |
| `body` | 合并到 JSON 请求体的任意键 | 逐键合并到 JSON payload |

`profiles.<name>.variant` 指定启动时默认套用的 variant 名（空 = "Default"）。`/model` 切换会携带当前 variant 名到新 profile（目标 profile 无该 variant 时回退到 Default 并告警）。

## MCP server（mcp.json）

MCP server 用独立的 **JSON** 文件配置，沿用业界通用约定 `{"mcpServers": {...}}`（与 Claude Desktop / Cursor 等一致，可直接复制上游文档的片段）。两层文件，均可选，按 **server 名深合并**（项目覆盖/扩展全局）：

- 全局：`$XDG_CONFIG_HOME/creator/mcp.json`（或 `~/.creator/mcp.json`）
- 项目级：`./.creator/mcp.json`

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
      "env": { "FOO": "bar" },
      "disabled": false,
      "tools": {
        "read_file": { "readOnly": true, "concurrencySafe": true, "maxResultChars": 0 }
      }
    }
  }
}
```

- `type`：`stdio`（默认）/ `http` / `sse`；`url` 用于 http/sse。
- `disabled: true`：关停该 server。项目文件可借此**关停全局定义的 server**（按名深合并）。
- `tools`：按工具名声明 capability override（MCP 协议不携带 readOnly/concurrencySafe，默认 fail-closed）。
- 深合并：同名 server 项目逐字段覆盖全局（如项目只改 `command`，仍继承全局 `type`/`args`）；未知键被宽容忽略（为未来扩展预留）。
- 缺失文件不报错；JSON 语法错误会告警并跳过。

## 环境变量

| 变量 | 作用 | 覆盖 |
|---|---|---|
| `XDG_CONFIG_HOME` | 全局配置目录根（设置时全局配置走 `$XDG_CONFIG_HOME/creator/config.toml` 与 `mcp.json`） | 全局配置路径 |
| `OPENAI_API_KEY` | API key | profile 的 api_key |
| `OPENAI_BASE_URL` | 端点 URL | profile 的 base_url |
| `OPENAI_MODEL` | 模型名 | profile 的 model |
| `CREATOR_AGENT_TYPE` | provider 类型（openai/anthropic） | profile 的 type |
| `CREATOR_AGENT_REQUEST_TIMEOUT` | 单次流式总超时 | profile 的 request_timeout |
| `CREATOR_AGENT_DEBUG` | =1 时打印每轮发给模型的消息 + 工具调用到 stderr（诊断用） | — |

## Flag

| Flag | 作用 |
|---|---|
| `-profile <name>` | 指定 profile |
| `-type <type>` | 覆盖 provider 类型（openai/anthropic） |
| `-base-url <url>` | 覆盖 base_url |
| `-api-key <key>` | 覆盖 api_key |
| `-model <name>` | 覆盖 model |
| `-request-timeout <dur>` | 覆盖单次流式总超时（如 10m/600s） |
| `-c` / `--continue` | 恢复最近一个会话（仅 TUI） |
| `-r` / `--resume` | 启动后弹出会话选择器，手动选要恢复的会话（仅 TUI） |

## 权限模式（Permission Modes）

模式决定 agent 在多大范围内可以**不经询问**就执行写/修改操作——一套从保守到激进的信任梯度。运行时用 `Shift+Tab`（循环）、`/mode [name]`、`/modes`（picker）或 `Ctrl+X o` 切换，立即生效、无需重建 agent。启动模式由 `permissions.mode` 指定（默认 `default`）。

| 模式 | 自动放行 | 仍询问 | 拒绝 |
|---|---|---|---|
| `default` | 只读工具（read/grep/glob/todo/skill/task） | 每个写/修改操作 | — |
| `trust` | 工作区内常规写（文件编辑）+ 良性命令（`ls`/`git status`/`make test`…） | 危险操作：删除、网络写入、`sudo`、装包、工作区外写入、`git push`/`reset --hard` | — |
| `auto` | 几乎全部操作 | — | 仅灾难性操作（`rm -rf /`、`mkfs`、写裸设备、fork bomb） |
| `readonly` | 只读工具 | — | 一切写/修改（硬阻断，覆盖 `allow`） |

**决策优先级**（高 → 低）：

1. `readonly` 模式下的写操作 → **拒绝**（覆盖 `allow`，用户明确的"禁止写入"意图）
2. `deny` 规则命中 → 拒绝（任何模式）
3. 灾难性操作 → 拒绝（任何模式，如 `rm -rf /`）
4. `allow` 规则命中 → 放行（白名单提升）
5. 模式阈值决策（按上表）

**风险分级**：每个工具调用按副作用分为 `safe`（只读）/ `normal`（可逆本地写、良性命令）/ `risky`（删除、网络、sudo、装包、工作区外写、远端 git 操作）/ `catastrophic`（灾难性）。模式即一个风险阈值——低于阈值放行、高于阈值询问，而 `catastrophic` 在任何模式下都被拒绝。

**模型可见**：当前模式每轮注入系统提示词，模型知道边界且**不能自己切换模式**（无切换工具，只有用户能切）。在 `auto` 模式下模型也不会建议/询问切换（避免干扰）；其他模式下可建议用户切换并等待确认。

> 应用层规则是尽力而为的过滤，无法防御蓄意绕过（如 `$(rm -rf /)` 命令替换、引用、eval）。强隔离需启用 `sandbox`。

## 规则格式（permissions / hooks matcher）

- `"tool"` — 匹配该工具任意调用
- `"tool:spec"` — spec 是 glob（`*` 跨任意字符含 `/`）
- `"*"` / `"read:*"` — 通配

**bash 复合命令拆分**：`cmd1 && cmd2` 会拆成两条独立匹配，任一 deny → 整体 deny（防 `&&` 绕过）。
