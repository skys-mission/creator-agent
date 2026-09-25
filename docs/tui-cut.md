# TUI 大砍记录（2026-09）

完全重构背景下的一次功能清场：**只保留模型对象管理（TUI 里的 `/model`：二级菜单新建 / 删除）**，其余全部砍掉。
目标是一个非常干净的页面——只有输入框和核心终端兼容能力。本文记录砍了什么、为什么砍、怎么找回、重建时按什么顺序接回来。

## 现在还剩什么（活代码）

```
cmd/creator-agent/tui/
  ├── state.go           App 状态（输入框 + 一行提示 + /model 面板 + 创建表单）
  ├── loop.go            事件循环 + stdin 输入泵 + panic 落盘诊断
  ├── keys.go            按键路由（编辑 / 提交 / 退出 / 弹窗优先）
  ├── commands.go        命令注册表（只有 /model）
  ├── completions.go     命令行提示：输入 `/` 弹候选菜单 + Tab 补全（保留项）
  ├── render.go          极简渲染：提示行 + Claude 式输入框 + 绘制原语
  ├── input.go           输入缓冲（光标 / 换行 / 宽字符折行）
  ├── screen.go          自建双缓冲 Screen + Surface（宽字符 follow cell）
  ├── style.go/theme.go  调色板与样式（主题切换基础设施保留，命令砍掉）
  ├── model_menu*.go     /model 二级菜单（新建 / 管理列表 / 删除确认）
  ├── model_form*.go     创建模型表单 + 确认页
  ├── model_store.go     模型落盘 ~/.creator/models.json（0600，含密钥）
  ├── terminal_bridge.go terminal/ 类型别名
  ├── util.go            小工具（mini/maxi/maskKey/truncatePlain/orDash）
  ├── terminal/ i18n/    原样保留
  └── diag/              原样保留（崩不掉的诊断落盘）
```

页面形态：**输入框（顶/底横线 + `❯` 提示符 + 折行编辑区 + 底部一行快捷键提示）+ 其上一行 notice 提示**。
退出方式：`Ctrl+D`，或两秒内连按两次 `Ctrl+C`（单按 `Ctrl+C` 只提示"再按一次退出"）。
`Esc` 只清空草稿、永不退出。

行为约定（与砍掉前的差别）：

- **命令行提示保留**：输入 `/` 会弹出命令候选菜单（模糊过滤、↑↓ 选用、Tab 选用、PgUp/PgDn 翻页），
  Tab 也能直接补全唯一候选。这是输入框体验的一部分，不砍。
  回车语义比砍掉前更顺：输入**正好是完整命令**时回车直接执行（不用按两次）；输入是前缀时
  回车选用高亮候选，再按一次回车执行。
- 输入普通文字：提示"agent 运行时还没接入（P2 重建中）"，**草稿保留在输入框**，不吞用户输入。
- 输入未知命令（如 `/help`）：提示未知命令并清空输入；`/etc/hosts` 这类路径样文本按普通文字处理。
- 鼠标事件直接丢弃（没有可滚动区域）。

## 砍了什么

按文件（全部在 git 历史 + 备份里，一个没丢）：

| 文件 | 原功能 |
|---|---|
| `layout_claude.go` | 消息区/工具卡/授权块布局（输入框绘制已并入 `render.go`） |
| `markdown.go` | markdown 解析 + styledLine 折行缓存 |
| `picker.go` `pickers.go` `pickers_mcp.go` `pickers_session.go` `mode_picker.go` | 各类选择器（模型/MCP/会话/人格/模式/主题） |
| `keybindings.go` | `Ctrl+X` leader 键绑定 |
| `approve.go` `approval_view.go` | 工具授权块（Allow/Deny） |
| `mentions.go` | `@文件` 引用展开 |
| `history.go` | 输入历史（↑↓ 翻历史） |
| `wizard.go` `wizard_input.go` | 旧的 `/model` 交互向导（被 `/model` 面板 + 创建表单取代） |
| `commands.go`（旧） | 全部 24 个 slash 命令（/help /clear /copy /compact /cost /model /models /mcps /agents /variants /mode /sandbox /changes /diff /resume /tools /themes /rename /pin /new /sessions /exit /quit …） |
| `util.go`（旧） | 成本估算/标题生成/OSC 52 复制/首页 tips 等助手 |

对应测试文件一并删除（30+ 个）；保留并改造的测试：

- `input_render_test.go`（输入→渲染管线、光标位置）
- `model_form_test.go` `model_store_test.go`（表单全走查 + 落盘 0600 + 校验/取消/掩码）
- `commands_test.go`（新写：注册表漂移守卫 + 提交分流）
- `completions_test.go`（命令行提示：弹出 / 过滤 / Tab 选用 / 回车语义 / Esc 关闭 / 渲染 / 退出键不被吞）
- `keyparse_test.go` `self_render_test.go` `present_bytes_test.go` `followcell_test.go` `surface_test.go`（终端解码/双缓冲/宽字符等核心兼容能力）
- `style_test.go` `theme_test.go`（调色板/主题基础设施；`/themes` 命令用例已删）

i18n 字典保留全部旧词条（未删除），只新增 `footer.hint`、`msg.runtime_not_ready`，并改掉 `msg.unknown_command` 里的 `/help` 提示。
旧词条现在无人引用，属于安全的死数据；哪块功能重建时直接复用。

## 怎么找回

两条路，任选：

1. **git 历史**：`git show <砍掉那次commit>^:cmd/creator-agent/tui/pickers.go`（commit 前的版本全在）。
2. **本地备份**：`attic/tui-legacy/` 是砍掉前整个 `cmd/creator-agent/tui/` 的完整拷贝（含全部测试）。
   里面放了一个嵌套 `go.mod`，让它**不参与主模块的 `go build ./...` / `go test ./...`**（否则备份包会跟着主构建走、迟早编译失败）。
   想复活某个文件：拷回 `cmd/creator-agent/tui/` 后按现有类型（`App` 只剩输入框/notice/表单三个状态面）补回落点即可。

`attic/` 已加入 `.gitignore`，是纯本地备份，不进 git。

## 重建路线（建议顺序）

1. **P2 运行时接线**：`submitInput` 里普通文字分支换成真正的 agent 调用（`contract.Agent` / `contract.StreamInput` 已就绪）；消息区（`msgBlock` + viewport + 滚动）随之回来。
2. **授权块**：`approve.go` / `approval_view.go` 的锚底不变量（AGENTS.md 渲染不变量第 2 条）直接照搬。
3. **斜杠命令逐个回来**：命令行提示（补全菜单）已经就位，`commands_test.go` 的注册表守卫从"只有 /model"放宽即可。
4. **选择器 / 会话 / MCP / 主题命令**：`picker[T]` 泛型在备份里，UI 状态挂在 `App` 上重新接。
5. markdown / 工具卡 / 成本估算等富渲染最后回。

每回一块，就把 `docs/tui.md` 对应节的不变量重新核对一遍（三条渲染不变量始终有效）。
