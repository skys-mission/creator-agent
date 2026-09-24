# 贡献指南

感谢你对 creator-agent 的兴趣。**当前处于从零重建阶段**：只有 TUI 渲染层被完整保留，core 等待重建。本指南帮你快速上手。

## 开发环境

- **Go 1.25+**（构建时）。运行时零依赖（单二进制）。
- 无需 Node / Python / 其他运行时。

## 常用命令

```bash
make test        # 全部测试（含 race 检测，CI 跑这个）
make test-fast   # 快速测试（无 race，本地迭代用）
make test-cover  # 测试 + 覆盖率
make build       # 编译检查（暂无 main 包）
make bench       # 基准测试（纯函数热路径）
make vet         # 静态检查
make fmt         # 格式化（gofmt -s）
make help        # 查看所有命令
```

提交前请确保 `make test`、`make vet`、`make fmt` 全绿。

## 项目结构

```
contract/               TUI 消费的全部 API（领域模型 + 控制面 + 叶子助手）
cmd/creator-agent/
  ├── tui/              自研全屏 TUI 渲染层（保留）+ tui/terminal/ + tui/i18n/
  └── diag/             不可 recover 崩溃的落盘诊断（fatal.log / trace.log）
docs/tui.md             渲染不变量与踩坑经验
scripts/                dev-sandbox.sh 隔离测试沙箱（待 CLI 重建后恢复 make target）
```

## 边界规则

- **`contract/` 不得包含任何 agent 逻辑。** 没有 run loop、没有工具执行、没有模型 adapter、没有中间件管线、没有配置加载。往这里加逻辑就毁掉了它的作用——那些属于新 core，由它来**实现**这个契约。
- `contract/` 允许的只有三类：完整未删减的领域模型（新 core 要产出/消费的形状）、TUI 驱动的运行时控制面（`ModeController` / `AllowSet` / `SandboxController` / `AskResolver`）、TUI 直接调用的叶子助手（会话 ID 与标题派生、`ApproveKey` 分组、错误提示、磁盘路径）。
- **TUI 只认 `contract/` 的类型**，不认任何 provider SDK 类型。SDK 只能活在未来 core 的 adapter 层里，且不得越过 adapter 边界外泄。
- **工具能力声明 fail-closed**：`ToolInfo` 不声明 `ReadOnly` 视为写、不声明 `ConcurrencySafe` 视为不可并发、不声明 `MaxResultChars` 视为不落盘。加工具时保持这个语义。

## 测试策略

改 TUI：补 `cmd/creator-agent/tui` 的纯单元测试。**渲染相关用自建 `Screen` 的 `GetContents()` / `GetCursor()` 断言 back buffer，不需要真终端**——加新渲染分支时补对应用例，而不是"跑起来看看"。

三层测试纪律（重建时沿用），详见 [docs/tui.md](docs/tui.md) 第 7 节：

1. **单元测试**（默认，零网络）：全 mock，CI 跑的就是这层。
2. **真实 API 集成测试**（opt-in）：首行 `//go:build integration` + 环境变量 `CREATOR_AGENT_TEST_API_KEY` 双重门控，为空 `t.Skip`。断言用哨兵串 / 确定性数学 / 跨轮记忆这类**智能断言**，不用开放式判断。
3. **沙箱写隔离冒烟**（opt-in）：`sandboxsmoke` tag，真正执行 `sandbox-exec` / `bwrap` 验证写隔离。

## 改 TUI 渲染层

**必读 [docs/tui.md](docs/tui.md) 第 4 节的三条渲染不变量**，每条都来自一次真实 bug：

1. 每个 `drawXXX` 必须完全填充其矩形区域（否则上一帧残留字形从短行渗透）
2. 授权块按钮必须锚定到块底部可见（否则大文件 diff 把 `Allow/Deny` 挤出屏幕，表现为"卡在授权"）
3. panic 恢复时禁用鼠标捕获必须先于退出 alt screen（否则父 shell 被 SGR 鼠标报告喷成乱码）

另外：宽字符（CJK/emoji）的 follow cell 在 buffer 里填空格但输出时跳过；主题切换走原子 `palettePtr`，任何"渲染结果依赖主题"的缓存**必须带 theme 维度**；`App` 状态由单 goroutine 独占，外部事件一律走 channel。

## 平台兼容

平台文件按后缀切分（`terminal_{darwin,linux,unix,windows}.go` 等）。**Windows 走 REPL 降级**（无全屏 TUI）。CI 在 ubuntu 与 macos 都跑 `-race`。改平台相关代码时确认 `GOOS=windows go build ./...` 与 `GOOS=linux go build ./...` 都能过。

## 安全

- **永远不要**把 API key 写进代码 / 文档 / config 模板 / 测试 / CI。key 只在环境变量和不进 git 的本地文件。
- 权限规则是 best-effort 启发式（bash 复合命令拆分对命令替换 `$(...)` / 反引号不可见）；强隔离靠 OS 沙箱。

## 提交

- 遵循 `make test` + `make vet` + `make fmt` 全绿。
- commit message 用约定式格式（`feat:` / `fix:` / `test:` / `docs:` / `perf:` / `refactor:` / `chore:`）。
- 中文 commit message 可接受（项目文档中英混用）。

## License

贡献内容遵循 [Apache 2.0](LICENSE)。
