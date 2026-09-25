# 基础架构设计（v3 终版）

> 新基调：**开源 Agent 核心项目**。核心是 agent 运行时内核；CLI 是产品交互外壳，与内核
> **一体单二进制**。初期产品形态 = **本机调用**（进程内直调，不走网络）。桌面端 / 编辑器走
> **自研协议**（stdio JSON-RPC）；远程 / webui 走 **gRPC（HTTP/2，初期 h2c 免证书）**。
> Lua 插件扩展；本地配置管理不用 sqlite。HTTP/3 不走。

设计三原则（贯穿全文）：

1. **工程化**：每模块职责、输入输出、可单测；正确性靠结构与测试保证，不靠调参试错。
2. **不过度封装**：接口只放真正的变化边界（模型厂商、Lua VM、存储、传输）；其余用具体类型。
3. **可扩展**：扩展点只有三个——Lua 插件、模型适配层、传输通道；都是"窄接口 + 具体实现"。

**依赖纪律（锁定）**：服务端流量底座只有官方 `google.golang.org/grpc`（grpc-go）+ 标准库。
**不引入 Hertz / Gin / Echo / Fiber 等通用 HTTP 与服务框架，也不引入任何 RPC 包装库**；
缺什么能力就在 `server/` 里写薄层（token 拦截器、限额都是这么来的）。

---

## 0. 决策记录（本轮定案）

| 决策 | 内容 | 备注 |
|---|---|---|
| 产品形态 | 单二进制一体：CLI 即产品，运行时内核同进程；默认本机直调 | 网络能力一起打包，但 CLI 正常使用**永不监听**，仅 `serve` 开启 |
| 桌面/编辑器通道 | **自研协议**：JSON-RPC 2.0 信封 + 自家词汇 + stdio + schema 版本化 | 不用 ACP——标准功能面固定、演进节奏不可控；自研换完全控制权 |
| 网络面 | **gRPC over HTTP/2**（grpc-go），初期 **h2c 免证书** | HTTP/3 搁置（gRPC 主场是 HTTP/2）；Hertz 不引入（gRPC 场景不需要 HTTP 框架） |
| 安全 | **授权天花板在 agent 侧** + 网络面三道闸（不暴露 / 验身份 / 限权限） | 见 §8 |
| 插件 | Lua，用 **lunar**（锁 commit + VM 窄接口隔离） | lunar 是纯 Go Lua 5.1 VM，非插件框架；插件宿主自建 |
| 配置 | 分层 TOML + 版本迁移 + 秘密分离；**不用 sqlite** | 会话数据 JSONL |
| 契约 | **.proto 单一事实来源**（buf + 代码生成） | 生成 Go 类型；stdio 通道用 protojson，gRPC 通道用 protobuf |
| 生命周期 | `kernel/` 保留原样，当内核骨架 | 注册 / 依赖序启动 / 逆序销毁 / 失败回滚 |
| Go SDK | ACP 已不用；gRPC 官方有 Go 支持（grpc-go），无缺口 | 自研 stdio 协议的 Go 实现全部自写 |

---

## 1. 总体结构

```
                ┌───────────────────── 使用面 ─────────────────────┐
                │ CLI/TUI(产品本体)   桌面端/编辑器   webui(未来)   │
                └───────┬──────────────────┬────────────────┬─────┘
                  进程内直调(默认)    自研协议(stdio)      gRPC(HTTP/2)
                        ▼                  ▼                  ▼
┌──────────────────────────────────────────────────────────────────────────┐
│ core/      agent 运行时：会话、agent loop、工具执行、权限、配置、Lua 插件   │
├──────────────────────────────────────────────────────────────────────────┤
│ adapters/  模型厂商适配层（唯一允许 import provider SDK 的地方）            │
├──────────────────────────────────────────────────────────────────────────┤
│ contract/  .proto 生成的领域模型 + 线上协议形状（无逻辑，所有端共享）        │
│ kernel/    组件生命周期：注册 / 依赖序启动 / 逆序销毁 / 失败回滚（保留）      │
└──────────────────────────────────────────────────────────────────────────┘
```

依赖方向单向：通道实现（stdio 协议 / gRPC）→ core → (contract, kernel)；
`adapters` 实现 core 定义的窄接口；`contract` / `kernel` 不依赖内部包；kernel 只用标准库。
装配只在 `cmd/`。

### 包布局

```
cmd/
  creator-agent/          一体入口（单二进制）：默认本机交互（TUI/REPL，进程内直调）；
                          serve 开 gRPC 监听；rpc 子命令 = 自研 stdio 协议模式（被桌面/编辑器拉起）
server/
  ├── grpc/               gRPC 服务实现（薄：解析 → 调 core → 流回事件）
  ├── rpc/                自研 stdio 协议：JSON-RPC 分发 + 会话桥
  ├── auth/               token 生成/校验、拦截器、（将来）TLS
  └── limits/             消息/并发/长流/限速等加固配置
core/
  ├── runtime/            agent loop（真正的运行时核心）
  ├── session/            会话与消息持久化（JSONL）
  ├── tools/              工具系统：内置工具 + 注册表 + 沙箱执行
  ├── perms/              权限与授权流（天花板策略：Mode / AllowSet / Ask）
  ├── config/             分层配置管理（见 §6）
  └── plugins/            Lua 插件宿主
      └── luavm/          VM 窄接口（lunar 实现，可整体替换）
adapters/                 模型适配（协议分包：openaichat / …；唯一允许 import 厂商 SDK 的地方）
contract/                 领域模型 + 控制面 + 叶子助手（Model 配置对象、事件、会话、路径等）
kernel/                   生命周期框架（保留，原样）
```

---

## 2. 三个通道（功能一份，入口三种）

core 能力以 **Go 方法**暴露（`contract` 类型 + `Agent`/`Session`/`Config`/`Plugins` 服务面），
**唯一功能面**——三个通道都只是它的消费者，谁都不许在自己身上重写逻辑。

| 通道 | 谁用 | 机制 | 安全面 |
|---|---|---|---|
| **进程内直调**（默认，产品形态） | 本机 CLI / TUI / 未来桌面嵌入内核 | 直接调 core 方法，事件走 Go channel | 无网络，无攻击面 |
| **自研 stdio 协议** | 桌面端 / 编辑器插件拉起 `creator-agent rpc` 子进程 | JSON-RPC 2.0 over stdio，protojson 编码 | 父子进程信任，无端口无暴露 |
| **gRPC 网络面**（`serve` 按需开启） | webui / 远程 / 跨机器 | gRPC over HTTP/2（初期 h2c） | 三道闸，见 §8 |

三通道共用同一份 `contract` 类型与事件流（channel / stdio 通知 / gRPC stream 只是搬运方式不同）。

---

## 3. 自研协议（桌面 / 编辑器通道）

**信封**：JSON-RPC 2.0（request / response / notification）。**词汇自家设计**，proto schema 定义、
版本化（v1 起），JSON 编码走 protojson。

### 方法面（v1）

| 方法 | 方向 | 用途 |
|---|---|---|
| `initialize` | 握手 | 协商协议版本 + 能力声明 |
| `session/new` · `session/resume` | 请求 | 建会话 / 恢复（可回放历史） |
| `session/prompt` | 请求 | 发一条消息，驱动一个 turn |
| `session/cancel` | 通知 | 打断当前 turn |
| `session/close` · `session/list` | 请求 | 关会话 / 列会话 |
| `session/update` | 通知（下行流） | 文本增量 / 工具调用状态 / 计划 / 用量 |
| `session/request_permission` | 请求（上行） | 授权询问，选项：allow_once / allow_always / reject_once / reject_always |
| `rpc/elicitation` | 请求（上行） | 向用户要结构化输入（如填分支名） |

### 兼容与扩展规则

- 版本协商在 `initialize`；**未知通知安全忽略，未知请求回 `-32601 Method not found`**；
- 自有扩展走 `_creator.dev/*` 命名空间方法 + `_meta` 字段，不与标准词汇冲突；
- schema 变更走 buf 版本化，wire 兼容靠握手协商，不靠猜。

### 生命周期

子进程随宿主 UI 生死（初期）。后续可加守护进程模式（UI 重启不丢会话、多 UI 接同一内核），
届时 stdio 由守护进程接管或换 Unix 套接字——协议不变。

---

## 4. Agent 运行时（core/runtime）

- **并发模型**：每会话一个 goroutine 串行执行 turn，多会话天然并发。不做全局调度器——
  可推理、可复现比吞吐重要。
- **turn 管线**（薄，不做中间件框架）：

  ```
  输入 → 少量固定步骤(AgentsMd 注入 / 上下文预算) → model adapter
       → 事件流出 → 工具调用(权限检查 → 沙箱 → 执行) → 回填模型 → 循环
  ```

- **工具系统**：fail-closed 能力声明（不声明 ReadOnly 视为写、不声明 ConcurrencySafe 视为
  不可并发、不声明 MaxResultChars 视为不落盘）。内置工具与插件注册工具进同一注册表，
  权限系统一视同仁。
- **权限流**：`Mode`（auto / ask / deny）+ `AllowSet` + `AskResolver`；ask 经三个通道任意一个
  问到用户，答案回填。**授权天花板在 agent 侧**（§8 S1）。
- **model adapter 窄接口**（变化边界）：

  ```go
  type ModelClient interface {
      Stream(ctx context.Context, req *contract.ModelRequest) (<-chan contract.Event, error)
      Name() string
  }
  ```

  请求用 `contract.ModelRequest`（messages + tools），不是 `StreamInput`：一次 agent 执行
  含多次模型调用，每次的历史与工具面都不同——执行输入（TUI→agent）与模型调用输入
  （loop→adapter）是两个生命周期。provider SDK 类型不得越过 adapters/ 边界。

  配置对象是 **`contract.Model`**（Name / Protocol / BaseURL / ModelID / APIKey / Params），
  中立领域模型而非适配层内部类型——TUI 只认 contract。TUI 用 `/model` 面板（二级菜单）创建 / 删除模型对象
  对象（字段级校验 + `Model.Validate()`），落盘 `~/.creator/models.json`（0600；含密钥的
  本地文件，绝不入 git、绝不打印——`Model.String()` 恒打码）；`adapters.New(contract.Model)`
  按 Protocol 分发到协议子包。以协议为键，不以厂商为键。

- **思维链回传（事实规范）**：OpenAI Chat Completions 生态**从未标准化**思维链字段，
  圈内流通四个名字（调研自三个开源实现 + vLLM 变更记录）：

  | 字段 | 谁在用 | 备注 |
  |---|---|---|
  | `reasoning_content` | DeepSeek 发明；Kimi API、旧 vLLM、绝大多数兼容网关 | **事实默认** |
  | `reasoning_details` | OpenRouter | 有时是数组形状，不是字符串 |
  | `reasoning` | OpenAI GPT-OSS 指引；新 vLLM（vllm#27752，请求侧只认它 #38488） | |
  | `reasoning_text` | minimax-code 实测扫描的变体 | |

  我们的规范（`adapters/openaichat` 已实施）：
  1. **入站**按 `reasoning_content > reasoning_details > reasoning > reasoning_text` 优先级
     扫描，只认字符串值（跳过 vLLM 的 `null` 占位、OpenRouter 的数组），只取第一个命中。
     **解析永远开**——数据层不丢数据。
  2. **可见性是 UI 的事**：用户看不看思维链由 TUI 显示配置决定（`thinkingMode` 三档：
     compact 摘要 / expanded 全文 / hidden，Ctrl+T 循环，**默认可见**），不进 Model 对象。
  3. **收/发配置互相隔离**（2026-09 拆分：一个字段名同时管收发太隐晦）：
     - **接收**（`Params.ReasoningKeyIn`，默认空 = **智能适配**）：入站按优先级扫已知名字；
       钉死则只认该键（接收面收窄成一个名字，只给非标准网关用）。
     - **回传开关**（`Params.ThinkingEcho: "on"|"off"`，**默认 `on`**）：`off` = 思维链完全
       不上出站请求（端点拒收未知字段时用），且"发"侧配置在 UI 上不可配置（发不发都
       没意义的字段不给配）。
     - **发送**（`Params.ReasoningKeyOut`，默认空 = **原样返回**）：回传字段名取端点自己
       说的那个——`reasoningDialect` 观察入站命中键并记住，出站按记住的键回显（"对方说
       什么方言就回什么方言"，检测永不遗忘，端点中途换方言下次观察即跟随）；钉死则按
       钉死名回显（"宽收窄发"）。回退链：钉发 > 观察到的键 > 钉收 > `reasoning_content`。
       不认该字段的服务器会忽略它，需要思考进历史的网关则依赖它。
  4. **请求侧"思考深度"控制（2026-09 调研，已实施到 `contract.Reasoning`）**：形状按能力
     分三类，绝不混用一个字段：

     | 类别 | 谁在用 | 传参形状 |
     |---|---|---|
     | 等级型 | OpenAI Chat Completions 顶层 `reasoning_effort`；OpenRouter `reasoning.effort`；Gemini `thinking_level`；Anthropic 新式 `output_config.effort` | 枚举，**每个模型自述支持子集** |
     | 开关型 | DashScope/Qwen `enable_thinking`（布尔）；Ollama `think`（布尔）；GLM/DeepSeek `thinking:{type:"enabled"/"disabled"}`（对象）；vLLM/SGLang `chat_template_kwargs:{enable_thinking\|thinking}`（模板布尔）；OpenRouter `reasoning:{enabled}`（对象布尔臂） | 二值开关，深度不可调 |
     | 预算型 | Anthropic 旧式 `thinking.budget_tokens`；DashScope `thinking_budget`；Gemini `thinkingBudget` | 整数预算（`budget` kind 预留未实施） |

     等级枚举并集 = `none / minimal / low / medium / high / xhigh / max`（OpenAI 官方文档
     明确取值"依赖模型"，SDK 已定义 none..xhigh 常量；OpenRouter 同集合并按 max_tokens
     百分比换算档位；Gemini 只有 minimal..high；强制思考的模型拒收 `none`——OpenRouter
     models 端点以 `supported_efforts` + `mandatory` 自述能力）。

     我们的规范：
     1. 模型对象声明能力（`Params.Reasoning`：Kind + 等级子集 + 默认档）。TUI 把推理配置
        收进**二级设置页**：一个独立**思考开关**（纯开/关，不承担种类切换），打开后出现
        "允许等级"多选（7 档预设）与"默认推理等级"；"开关方言"行给仅开关型网关，与等级
        **互斥**（选方言即清空等级，勾等级即把方言复位为"不使用"），声明不会自相矛盾。
     2. 开关到落线的映射原则是"**按端点方言明确关，无法表达就沉默**"：
        开 + 等级 → `Default=默认档`；关 + 等级含 `none` → `Default=none`（端点自述接受
        明确关闭）；关 + 等级不含 `none` → 什么都不发（强制思考模型无法表达关，发任何档
        都会把思考打开）；开关方言已选 → 开关直接映射 `Default=on/off`（Qwen3 这类默认开
        思考的网关必须显式发 false 才算关）。
     3. `kind=effort` → `adapters/openaichat` 发顶层 `reasoning_effort: <默认档>`（"max"
        这类 SDK 无常量的值原样落线）；`kind=none` 什么都不发——安全默认，乱发参数会被
        不支持的端点 400 拒收。
     4. `kind=toggle` 按方言映射（`Params.Reasoning.ToggleDialect` 钉死方言，模型对象里选）。
        **布尔开关没有事实标准**（不像思维链字段有 `reasoning_content` 事实默认），各家字段名和
        形状都不同，所以方言池尽量铺满实测形状 + 自定义口，UI 循环第一项是**不传（推荐）**
        （乱发开关会被不认识的端点 400 拒收）：

        | 方言 | 线上形状 | 谁在用 |
        |---|---|---|
        | `enable_thinking` | 顶层 `"enable_thinking": true\|false` | Qwen/百炼系 |
        | `think` | 顶层 `"think": true\|false` | Ollama 系 |
        | `thinking-type` | `"thinking": {"type":"enabled"\|"disabled"}` | GLM / DeepSeek |
        | `chat-template-enable-thinking` | `"chat_template_kwargs": {"enable_thinking": true\|false}` | vLLM/SGLang 跑 Qwen3、Gemma |
        | `chat-template-thinking` | `"chat_template_kwargs": {"thinking": true\|false}` | vLLM 跑 Granite、DeepSeek-V3.1 |
        | `reasoning-enabled` | `"reasoning": {"enabled": true\|false}` | OpenRouter |
        | 自定义 | `ToggleField` 点号路径（`a.b` → `{"a":{"b":…}}`），值取 `ToggleOnValue`/`ToggleOffValue` | 上表之外的新网关 |

        自定义把**值**也开放了（不止真/假）：值按 **JSON 字面量**解析（`true`/`false`/数字/
        `"带引号"`/裸文本当字符串），于是字符串枚举（如 `{"type":"adaptive"/"disabled"}`——
        MiniMax 实测形态）和数字（`1/0`）全能表达；**留空 = 该状态不发字段**（覆盖"只在开时
        传"的网关）。UI 首次选自定义预填 `true/false`（只种子一次，不与用户编辑打架）；字段名
        留空或两个值都留空时提交直接校验失败，不静默丢开关。实测断言见
        `adapters/openaichat/client_test.go` 的 wire 测试（含嵌套、字符串/数字值、留空省略）。
        至此单字段形状全覆盖；唯一剩的缺口是**多字段联动**（如 Anthropic 开思考必须同时给
        预算数），归"预算型"kind 的活。
     5. 每请求改档位留给 loop→adapter 的 `ModelRequest` 扩展，暂不做。

  证据：MoonshotAI/kimi-code `packages/kosong/src/providers/reasoning-key.ts`
  （KNOWN_REASONING_KEYS 优先级扫描 + `ReasoningKeyDialect` **按端点学习出站方言**、显式
  键停用检测——"各家软件很少设置这个字段"的真相就是智能适配：入站扫描 + 出站回说对方的
  方言，只有非标准网关才钉死字段名）；BerriAI/litellm（每厂商一份 transformation 硬编码
  `reasoning_content`，流式层直查字段，另提供 `merge_reasoning_content_in_choices` 打包
  成 `<think>` 块）；zai-org/ZCode `adapters/src/model/reasoning-history-normalization.ts`
  （历史卫生：跨模型思维链剔除、签名拒绝后修复——回传的配套问题）；
  MiniMax-AI/minimax-code `model-provider/thinking.ts`（on/off 模型映射
  `thinking:{type:adaptive|disabled}`，与我们 `thinking-type` 方言同形）；
  vLLM `docs/features/reasoning_outputs.md`（`--reasoning-parser` + `chat_template_kwargs`
  模板布尔，键名随所跑模型变：Qwen3/Gemma 用 `enable_thinking`，Granite/DeepSeek-V3.1 用
  `thinking`）；OpenRouter reasoning 文档（`reasoning:{effort|max_tokens|enabled|exclude}`，
  `effort` 与 `max_tokens` 二选一，`effort:"none"` 即彻底关思考）。

---

## 5. Lua 插件（core/plugins）

lunar 是**纯 Go Lua 5.1 虚拟机**（非插件框架）；插件宿主自建，VM 可替换。

### 插件形态

```
my-plugin/
  plugin.toml        # name / version / permissions / entry / hooks
  main.lua           # 入口
```

宿主暴露（按 manifest permissions 收窄）：`agent.register_tool` / `agent.on(hook, fn)` /
`agent.register_command` / `agent.log` / `agent.config.get` 等。钩子：session_start / message /
tool_call / tool_result / session_end。

### 沙箱与生命周期

- 默认只给 `CoreLibraries()`（不能碰 io/os/文件系统）；文件访问走宿主按 permissions 放行的 API。
- 每插件独立 lunar State（单 goroutine 约束），调用串行化，每次带 context 超时；
  `os.exit` 变 ExitRequest，杀不死内核。
- 插件宿主是 kernel 组件：启动加载、停止逆序关闭全部 State；热重载 = 关旧 State 起新 State。
- VM 窄接口（`DoString / NewFunction / SetGlobal / Close`）隔离在 `core/plugins/luavm/`；
  lunar 锁 commit（无 release、API 仍在稳定），不行就换 gopher-lua，只动一个包。
- 已知限制：lunar 无指令级预算（防死循环靠 context 取消 + 调用超时）。

---

## 6. 配置管理（不用 sqlite）

### 分层合并（低 → 高）

```
内置 defaults → ~/.creator/config.toml（全局）→ <项目>/.creator/config.toml
              → 环境变量 CREATOR_AGENT_* → 运行时 API 覆盖
```

- **TOML** 格式 + 类型化结构 + 校验（报错指明哪层哪个键为什么错）。
- **`configVersion` + 迁移函数链**：旧配置自动逐版本升级。
- **秘密分离**：主配置只写引用（`apiKeyEnv = "ANTHROPIC_API_KEY"`），密钥在环境变量或
  `credentials.toml`（0600，不进 git）。**模板 / 文档 / 测试永不出现真密钥**。
- **单一写者**：只有内核写配置（原子写：临时文件 + rename）；三个通道的"改配置"都调同一方法。
- **profile**：命名的模型+参数预设，切换只改一个键。
- 会话数据：每会话一目录（`messages.jsonl` 追加 + `meta.json`），抗崩溃、可 diff、可抢救。

---

## 7. 生命周期（kernel 保留）

内核由 kernel 组件拼装，拿到"注册 / 依赖序启动 / 逆序销毁 / 启动失败回滚 / 优雅退出"：

```
config → session store → plugin host → adapters → runtime → 通道(grpc / stdio)
```

信号（SIGINT/SIGTERM）只做 `app.Shutdown()` → 逆序销毁。kernel 七条不变量（G1–G7）不变，
见 `kernel/doc.go`。

---

## 8. 安全设计

### 不变量

- **S1 授权天花板在 agent 侧**：客户端（桌面/编辑器/web）的"允许"只能在 agent 侧策略以内生效；
  `deny` / 沙箱 / 写隔离由 agent 侧强制，**客户端批准永远越不过 deny 规则**。
- **S2 展示与执行分离**：工具展示名、diff 渲染都是"给人看的"，不授予任何权限；真实能力以
  注册表声明为准（fail-closed）。
- **S3 提示词注入防线** = S1 + S2 + 审计日志；仓库内容永远视为不可信输入。

### 网络面（gRPC）三道闸

| 闸 | 手段 | 要点 |
|---|---|---|
| **1 不暴露** | 默认绑 `127.0.0.1`；远程优先隧道（SSH / Tailscale / WireGuard）；本机可选 Unix 套接字（0600） | 外部包到不了，最有效 |
| **2 验身份** | 首启生成 256-bit token（0600）；gRPC 拦截器验 `Bearer`；常数时间比较；可轮换 | **unary + stream 双拦截器必须都挂**（只挂一个 = 流式裸奔） |
| **3 限权限与加固** | 方法级授权；**关闭服务反射**；消息大小上限；并发流上限；长流最大时长；限速；审计日志 | 反射关闭防接口清单外泄 |

### h2c（免证书）残余风险与档位

明文信道：token 可被同网段嗅探重放；无服务端身份可被冒充。

| 档位 | 场景 | 风险 | 措施 |
|---|---|---|---|
| 回环（默认） | 本机 | ≈ 零（流量不出机器） | 闸 2/3 即够 |
| 内网 | 局域网 / 自用 | 同网段嗅探 | 隧道优先；直连则知悉风险 |
| 公网 | 互联网 | 真实 | **必须 TLS（或 mTLS）** + 全闸（口子留在 auth/，届时启用） |

---

## 9. 技术选型与风险

| 选型 | 成色 | 结论 |
|---|---|---|
| **grpc-go** | gRPC 官方 Go 实现，Google 维护，成熟 | **采用**（网络面）；鉴权/限流自建拦截器（官方明确只给管道不给策略） |
| **buf + protoc-gen-go** | proto 工具链标准 | **采用**（契约单一事实来源） |
| **lunar** | 49★ 纯 Go Lua 5.1 VM，MIT，API 仍在稳定 | **采用**：锁 commit + VM 窄接口隔离 |
| **kernel/** | 自研，22 测试 + 七不变量 | **保留**（生命周期地基） |
| Hertz | 7.4k★ HTTP 框架 | **不引入**：gRPC 场景用 grpc-go 自己的 HTTP/2 栈；HTTP 框架没有出场位 |
| ACP | 4.3k★，JetBrains/Zed 等接入 | **评估后不用**：标准功能面固定、演进不可控；自研协议换完全控制权，生态自攒 |
| HTTP/3 / quic-go | — | **搁置**：gRPC 主场是 HTTP/2；未来有需求再议 |
| 配置 / 会话 | 自研分层 TOML + JSONL | 不引 viper / sqlite / ORM |

已知限制：① lunar 无指令预算（超时兜底）；② h2c 明文残余风险见 §8 档位；③ 浏览器 webui 要接
gRPC 需 grpc-web/Connect 桥——等 webui 立项再定；④ proto 工具链进构建流程（CI 加 buf lint）。

---

## 10. 改动清单（相对现状）

### 保留（资产）

| 项 | 处置 |
|---|---|
| `kernel/` | **原样保留**，当内核骨架 |
| `cmd/creator-agent/tui/` | **保留为产品交互前端**（已砍到"输入框 + `/model`"极简外壳，见 [docs/tui-cut.md](docs/tui-cut.md)）：注入的 `contract.Agent` 换本地直调实现（进程内 channel）；桌面场景走 stdio 协议 |
| `cmd/creator-agent/diag/` | 保留给 CLI/TUI；内核侧用 `log/slog` |
| `docs/tui.md` | 保留（渲染不变量对保留代码继续有效） |
| 测试三层纪律（mock 单测 / 集成 opt-in / 沙箱冒烟） | 保留，适用于新模块 |
| 密钥只进环境变量/本地文件 | **保留**（安全底线，见 §6） |

### 新增

`server/`（grpc + rpc + auth + limits）、`core/`（runtime + session + tools + perms + config + plugins）、
`adapters/`、一体入口 `cmd/creator-agent`（子命令：默认交互 / `serve` / `rpc`）、contract 的 proto 定义与生成流程。

### 重写

README / CONTRIBUTING / docs（按新定位）、CI（buf lint + 生成检查 + 真二进制构建 + 多 OS `-race`）、
Makefile（build 出真东西 + proto 生成 target）。

### 清理

根目录旧二进制构建产物、空 `logs/` 目录。CI 已下线的 sandbox-smoke 恢复时机挂到 core/tools 重建后。

---

## 11. 实施阶段

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| **P0 骨架** | kernel 装配 + config 分层 + core 服务方法面 + 一体入口直调 + gRPC 最小服务 + token 双拦截器 | 本机一条命令直调跑通（零网络）；`serve` 后 gRPC 健康检查通、无 token 被拒 |
| **P1 会话与流式** | session JSONL + 事件流（channel / gRPC stream）+ proto 契约落线 + mock 模型 | TUI 经直调通道真终端跑起来（假模型回显）；流式事件两端一致 |
| **P2 真运行时** | agent loop + 工具系统 + 权限流（天花板）+ 第一家 adapter | 真实模型对话 + 工具调用 + 授权流走通；deny 不可被客户端越过（S1 用例） |
| **P3 插件** | lunar 宿主 + manifest + 工具注册 + 钩子 + 热重载 | 一个 Lua 插件注册的工具被 agent 真实调用 |
| **P4 桌面通道** | `rpc` 子命令 + 自研协议 v1（schema 发布） | 模拟桌面宿主拉起子进程全会话走通（建会话/流式/授权/打断/恢复） |
| **P5 网络硬化** | 三道闸全量落地 + 隧道接入文档 + TLS 可选件（口子） | §8 三道闸逐项验收；反射关闭、限额生效、审计可查 |

每阶段结束跑全量门禁（`gofmt -s` / `go vet` / `go build` / `go test -race`，多 OS + buf lint），
功能与设计各复核一轮再进下一阶段。
