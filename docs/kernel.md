# kernel — 组件生命周期框架

`kernel/` 是项目的底层框架：**组件注册、按名查找（依赖注入）、依赖序启动、逆序销毁、全局生命周期状态机**。它相当于 [cordis](https://github.com/cordiverse/cordis) 的极简版——只取"注册表 + 作用域 + 销毁"这条主线，砍掉动态装载与热重载。零外部依赖（只用标准库），全部行为由测试机检（见文末"正确性怎么保证"）。

## 它解决什么

从零重建的 core 会由十几个模块组成（config、profile、adapters、tools、mcp、permission、session、tui 桥接……）。没有框架时每个模块都要手写"我先起、谁后起、退出时怎么收场"，顺序错了就是 use-after-shutdown 这类难查的 bug。kernel 把这些规则**集中到一处并用测试钉死**：

- 模块只声明 `Deps()`（我依赖谁），启动顺序由内核算（拓扑排序），人不用记；
- 退出时按启动顺序的**严格逆序**销毁，资源释放顺序天然正确；
- 任何一个模块启动失败，已启动的模块自动回滚销毁，不会留半截现场；
- panic、销毁报错、销毁卡死都有确定行为（收编成错误 / 继续销毁 / 超时上界）。

## 为什么自研而不是用现成库（选型记录）

| 候选 | 结论 | 原因 |
|---|---|---|
| cordis（JS） | 不可用 | 只有 Node.js 实现；其 README 明确 API 未稳定。我们要的只是它的模型，不是它的运行时 |
| [tr1v3r/cordis-go](https://github.com/tr1v3r/cordis-go) | 观察名单 | cordis v4 的 Go 移植（Koishi / DeepSeek Harness 插件系统同源），形态最贴：按名 `Get`、`OnDispose` LIFO、Effect/Disposer、服务注入、事件总线。但 2026-09 才出现，**无正式发版、0 star、单人维护**，README 自带未完成清单——不满足"非常成熟"，暂不押注 |
| [uber-go/fx](https://github.com/uber-go/fx) | 备胎（唯一够格的三方） | 最成熟的 Go 生命周期框架（Uber 生产级、约万级引用）。生命周期语义与 G2/G4/G5 高度重合：按依赖序启动、启动失败逆序回滚、逆序销毁、15s 默认超时、`fx.Module` 分组、`fx.ValidateApp` 不启动验图。真正不合的是**模型**：纯类型构造器注入 + 反射（dig），**没有运行时按名查找**；并引入 dig / multierr 等新依赖，图错误要到启动才暴露且报错冗长 |
| [sarulabs/di](https://github.com/sarulabs/di) | 中庸 | 三方里少数"名字即键"的容器（按名注册/查找，`Close` 按依赖逆序执行）；但无显式启动阶段与失败回滚语义，反射实现，社区规模小（约百级引用） |
| [google/wire](https://github.com/google/wire) | 出局 | 编译期生成装配代码（无反射），但**官方 README 已声明不再维护**，且无运行时生命周期管理 |
| oklog/run | 太薄 | 只做 goroutine 监督（actor 模式），无注册、无查找、无依赖序 |
| thejerf/suture | 不合形状 | 监督树 + 崩溃重启语义，无组件注册表 |
| **自研 `kernel/`** | **采用** | 需求是一个可精确规定的小内核，正确性靠性质测试机检而非调参；且保持项目"依赖极少"的既定路线 |

（其余小众容器——golobby/container 等——无生命周期管理且停更，未列入。oklog/run 与 thejerf/suture 的定位见下。）

如果未来需要服务端风格的构造器 DI（数百个构造函数的图）或 fx 的广度功能（装饰器、日志钩子），迁移到 fx 也不迟——kernel 的 Component 转成 fx 构造器是平滑演进。（kernel 本体是小几百行 + 全量测试的小内核。）

## 七条不变量（G1–G7）

完整表述在 `kernel/doc.go`，这里是速查：

| # | 不变量 | 一句话 |
|---|---|---|
| G1 | 注册唯一性 | 名字非空且唯一，违规在注册时就报错 |
| G2 | 依赖序 | 启动顺序是依赖图的拓扑序；缺依赖 / 成环在**任何组件启动前**报错 |
| G3 | 确定性 | 同样的注册序列永远得到同样的启动顺序（不受 map 遍历或调度影响） |
| G4 | 回滚原子性 | 第 k 个组件启动失败 → 前 k-1 个按逆序销毁，失败者只跑销毁器不跑 Stop，之后的组件根本不会被启动 |
| G5 | 销毁保证 | 销毁顺序 = 成功启动顺序的严格逆序；Stop 至多一次；单个 Stop 报错或 panic 不阻断后续销毁；每个 Stop 超时上界 `StopTimeout`（默认 15s） |
| G6 | 销毁器 | `Scope.Defer` 注册的销毁器恰好执行一次、后进先出；在启动失败时立即执行；用不被取消的 context 执行（关停信号不能中断资源释放） |
| G7 | 可见性与状态 | 按名查找只看得见**已成功启动**的组件（在自己 Start 里只保证看得见声明的依赖）；状态机只允许 `New → Starting → Running → Stopping → Stopped`、`New → Stopped`、`Starting → Stopped`；一次性，停了不重启 |

## 状态机

```
New ──Start──▶ Starting ──全成──▶ Running ──Stop/Shutdown──▶ Stopping ──▶ Stopped
 │                │ 失败/取消                                    （终态）
 └──Stop──────────┴───────────── 回滚销毁 ──────────────────────▶ Stopped
```

## 用法

```go
app := kernel.New()

// 函数式组件（简单模块）
_ = app.Add(kernel.Funcs{CompName: "config", OnStart: loadConfig})

// 结构体组件（带依赖、带销毁）：实现 kernel.Component
_ = app.Add(&MCPHost{name: "mcp", deps: []string{"config", "tools"}})

// 启动 → 阻塞等 ctx 取消或 app.Shutdown() → 逆序销毁
if err := app.Run(ctx); err != nil {
    log.Fatal(err)
}
```

组件在自己的 `Start` 里按名取依赖（泛型断言到接口）：

```go
func (m *MCPHost) Start(s *Scope) error {
    agent, ok := kernel.Get[contract.Agent](s.App(), "agent") // 依赖已启动，必可见
    if !ok {
        return fmt.Errorf("agent component missing")
    }
    cli, err := dial(m.url)
    if err != nil {
        return err
    }
    s.Defer(func(context.Context) { cli.Close() }) // 成功后 Stop 阶段 LIFO 执行
    m.cli = cli
    return nil
}
```

装配约定：`kernel/` 只用标准库；未来的 `core/` 依赖 `kernel` + `contract`；`cmd/` 只做装配（把组件注册进 `kernel.App`、接信号、跑 `app.Run`）。

## 非目标（明确不做）

- **动态装载 / 热重载**（cordis 的 registry/effects 运行时）：单二进制 CLI 不需要运行时换插件。
- **崩溃自动重启**（suture 式监督）：agent 进程内重启会放大状态不一致，失败就干净退出。

## 未来扩展点（形状已预留）

- `Provide` 服务值（cordis `ctx.provide`）：组件暴露与自身解耦的服务对象。
- 生命周期事件订阅（ready / dispose 通知）：给装配层打横幅、打点用。

## 正确性怎么保证

不靠"多跑几次看看"，靠可机检的断言（`kernel/*_test.go`）：

1. **性质测试**：300 组随机依赖图（1–40 节点），断言启动顺序满足拓扑约束且重复调用结果逐位相同（G2/G3）。
2. **逐条不变量对应用例**：回滚顺序、销毁逆序、Stop 至多一次、销毁器 LIFO 且恰好一次、panic 收编、超时上界、状态机全非法迁移、启动期 Shutdown 取消、并发查找与销毁并行（race 检测）。
3. CI 跑 `go test -race ./...`，上述全部在内。
