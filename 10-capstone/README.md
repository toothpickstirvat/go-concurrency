# 第 10 节：综合项目

## 目标

把前 9 节学的内容组合进一个完整程序。这个项目模拟一个简化的"任务调度引擎"，包含共享状态、取消传播、限流、定时任务、错误处理。

---

## 场景

一个任务调度器，管理多个"worker"。每个 worker 周期性地从外部"数据源"拉数据，处理后写进一个共享的 result map。有一个监控
goroutine 周期性打印状态。任何 worker 失败都会取消整个调度器。

---

## 要求

实现一个 `Scheduler` 类型，main 启动它，运行 5 秒后正常退出。

### 必须用到的知识点

1. **Mutex / RWMutex**（第 1 节）：保护 result map，写用 `Lock`，监控读用 `RLock`
2. **atomic**（第 2 节）：一个 `atomic.Int64` 计数器，统计总共处理了多少条数据
3. **sync.Once**（第 3 节）：result map 只初始化一次
4. **context**（第 4 节）：根 ctx，`WithTimeout(5s)`，所有 goroutine 派生于它，到点自动取消
5. **select**（第 5 节）：每个 worker 在 select 里同时等 ctx.Done、ticker.C、数据 channel
6. **pipeline**（第 6 节）：数据流 `数据源 → 处理 → 写入 map`，每个环节一个 goroutine
7. **errgroup**（第 7 节）：用 `errgroup.WithContext` 管理 worker，任意一个失败就取消所有
8. **信号量**（第 8 节）：限制同时拉取数据的 worker 数为 3
9. **ticker**（第 9 节）：监控 goroutine 每 500ms 打印一次 result map 的大小和计数器值，用 `NewTicker` 并 `Stop`

### 结构建议

```go
type Scheduler struct {
    mu       sync.RWMutex
    results  map[string]int
    counter  atomic.Int64
    initOnce sync.Once
    sem      chan struct{} // 信号量
}
```

### 加分项

1. 加一个"慢 worker"，它处理时间特别长，观察背压：信号量被占满时，其他 worker 是否等待。
2. 加一个"会失败的 worker"，让它 2 秒后返回错误，确认 errgroup 取消所有 worker，监控打印出退出前的状态。
3. 用 `runtime.NumGoroutine()` 在退出前打印，确认没有 goroutine 泄漏。

---

## 自查方法

```bash
go run -race ./10-capstone
```

- 运行 5 秒自动退出，不是因为 deadlock 退出
- 监控打印显示 result map 逐渐增长，计数器递增
- 没有 DATA RACE
- 退出前 goroutine 数应该回到接近初始值
- 失败 worker 触发时，其他 worker 能在 1 秒内全部退出

---

## 运行方式

```bash
go run -race ./10-capstone           # 正常跑 5 秒
go run ./10-capstone -limit 5        # 信号量容量改成 5
go run ./10-capstone -fail 3         # worker 3 在第 2 秒失败
go run ./10-capstone -slow 2         # worker 2 每次拉取 500ms
```

---

## 最终结构

```
main
 └─ ctx: WithTimeout(5s)
     └─ errgroup.WithContext(ctx) → gctx
         ├─ monitor：Ticker 每 500ms 打印 map 大小和计数器，gctx 结束时打印最终状态
         └─ worker 1..5：每个 worker 派生 wctx，内部是两级 pipeline
               source(wctx) ──ch──► worker 自己处理：s.Set(key, val)
               source 每次拉取前拿信号量（容量 3），拉完立刻释放
```

| 知识点 | 用在哪里 |
|--------|----------|
| RWMutex（第 1 节） | `Set` 写 map 用 Lock，`Stats` 读用 RLock |
| atomic（第 2 节） | `counter` 统计总条数，放在锁外 |
| sync.Once（第 3 节） | `init()` 懒创建 results，`Set`/`Stats` 开头都调用 |
| context（第 4 节） | 根 ctx 5 秒超时；worker 派生 wctx 控制自己的 source；`context.Cause` 打印取消原因 |
| select（第 5 节） | 拿信号量、拉数据、发给下游，三处都同时等 ctx.Done；失败计时器不用时是 nil channel |
| pipeline（第 6 节） | source 是 ch 唯一写入方，`defer close(ch)`；worker `range` 结束说明 source 已退出 |
| errgroup（第 7 节） | 管理 monitor 和 5 个 worker，任意一个返回错误就取消 gctx |
| 信号量（第 8 节） | `sem chan struct{}`，循环里拿、拉完立刻放 |
| Timer/Ticker（第 9 节） | monitor 用 Ticker；`fetch` 用 Timer + select 代替 Sleep，可以被 ctx 打断 |

---

## 实验结果

不带 `-race`，4 个逻辑 CPU。

### 正常跑 5 秒，信号量容量 3 和 5

| 信号量容量 | counter | 理论值 |
|---|---|---|
| 3 | 294 | 3 × (5s ÷ 50ms) = 300 |
| 5 | 490 | 5 × 100 = 500 |

**吞吐量由信号量容量决定，和 worker 数量无关。** 5 个 worker 抢 3 个名额，任何时刻只有 3 个在拉数据。
比理论值少几条，是每次拉取除了 50ms 还有调度和写 map 的开销。

### 会失败的 worker（`-fail 3`）

```
  worker 3 失败：处理 25 条
  worker 2 退出：处理 22 条（worker 3 失败）
[最终] size=117 counter=117（原因：worker 3 失败）
  worker 1 退出：处理 22 条（worker 3 失败）
  worker 5 退出：处理 22 条（worker 3 失败）
  worker 4 退出：处理 26 条（worker 3 失败）
调度器出错: worker 3 失败
退出，耗时 2.001s，goroutine 数：启动前 1，退出后 1
```

- worker 3 在第 2 秒返回错误，errgroup 取消 gctx，其他 worker 和 monitor 在同一时刻退出，总耗时 2.001s。
- `context.Cause(ctx)` 拿到的是"worker 3 失败"，`ctx.Err()` 只会是笼统的 `context canceled`。

### 慢 worker（`-slow 2`）

| | 正常 | worker 2 每次拉取 500ms |
|---|---|---|
| 总 counter | 294 | 207 |
| worker 2 | 60 条 | 9 条 |
| 其他每个 worker | 56–61 条 | 49–50 条 |

慢 worker 拖慢的不只是它自己。它每拿到一个名额就占 500ms，3 个名额里大部分时间被它占掉一个，其他 4 个 worker 只能抢剩下的 2 个：
2 × 5s ÷ 50ms ≈ 200 条，和其他 4 个 worker 合计 198 条对得上。**共享配额下，一个慢任务会占用所有人的吞吐。**

### goroutine 泄漏检查

三种场景退出后的 goroutine 数都和启动前一样（1 个，就是 main）。

---

## 设计要点

### 信号量在循环里拿、在循环里放，不能 defer

source 是一个循环。如果写 `defer func() { <-s.sem }()`，第一次拿到的名额要等函数返回才还，等于每个 source 永久占一个名额，
第 4 个 worker 永远拿不到。所以拉完立刻 `<-s.sem`，然后再发给下游；下游慢时 source 卡在发送上，但不占名额。

### 三处阻塞都要能被 ctx 打断

拿信号量、拉数据、发给下游，都写成 select 同时等 `ctx.Done()`。拉数据用 Timer + select（`fetch`），不用 `time.Sleep`：
Sleep 打断不了，退出时每个 source 都要多等一次拉取的时间。

### worker 返回前要确认它的 source 已经退出

只靠 errgroup 也能让 source 最终退出（worker 返回错误后 gctx 被取消），但 errgroup 只等 `g.Go` 启动的 goroutine，source 不在里面。
`g.Wait()` 返回时 source 可能还在跑，退出时的 goroutine 数就会多一个。所以 worker 自己派生 wctx 给 source，失败时：

```go
cancel()          // 通知 source 退出
for range ch {}   // 一直读到 source close(ch)，确认它已经结束
return err
```

正常退出时不需要额外处理：`range ch` / `<-ch` 读到 `ok=false`，说明 source 已经 close 并返回。

### 到点退出不当作错误

monitor 和 worker 在 ctx 结束时都返回 `nil`，所以正常到点 `g.Wait()` 返回 nil。如果某个 goroutine 返回 `ctx.Err()`，
`Wait` 会拿到 `context.DeadlineExceeded`，main 要用 `errors.Is` 把它和真正的失败区分开。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| monitor 用 Timer 做周期打印 | 只打印一次，之后永远等不到 | 用 Ticker（第 9 节同一个坑） |
| `ctx.Done()` 分支里只打印不 return | Done 一直就绪，for 循环疯狂空转 | 打印最终状态后 return |
| 只写了 `Init` 却没人调用 | 往 nil map 写入 panic | `Set`/`Stats` 开头调用 `init()`，靠 Once 保证只执行一次 |
| `Stats` 遍历 map 数个数 | O(n)，持读锁的时间变长 | `len(map)` 是 O(1) |
| 循环里用 defer 释放信号量 | 名额要等函数返回才还，其他 worker 永远拿不到 | 拉完立刻释放 |
| 拉数据用 `time.Sleep` | ctx 取消后还要多等一次 | Timer + select |
| worker 失败后不管自己的 source | `g.Wait()` 返回时 source 还在跑 | 先 cancel，再读空 ch，确认 source 退出 |
| 到点退出返回 `ctx.Err()` 又不区分 | 正常结束被当成出错 | 返回 nil，或用 `errors.Is(err, context.DeadlineExceeded)` 区分 |
| 以为 worker 多吞吐就高 | 吞吐量被信号量容量卡住 | 先找瓶颈（这里是信号量），再调参数 |

---

## 最佳实践

- **每个 goroutine 都有明确的退出路径**：ctx 结束、上游 close、自己出错，三种都要想到。
- **谁启动 goroutine，谁负责等它退出**：worker 启动了 source，就由 worker 确认 source 结束；errgroup 只管 `g.Go` 启动的。
- **共享状态封装在方法里**：调用方只用 `Set`/`Stats`，看不到锁和 map，不会忘记加锁。
- **单个计数用 atomic，复合数据用锁**，atomic 操作放在锁外让临界区更短。
- **用 `context.Cause` 记录取消原因**，日志里能直接看到是谁、为什么失败。
- **退出前检查 `runtime.NumGoroutine()`**，是发现 goroutine 泄漏最简单的办法；真实服务里用 pprof 的 goroutine profile。

---


## 这节做完之后

你已经覆盖了 Go 并发编程的核心。可以回头读 gmaker 的 `internal/engine/` 代码，会发现大部分模式你都亲手实现过了。剩下的是工程细节：锁的顺序、goroutine
泄漏排查、race detector 的解读、性能调优——这些在真实代码里练。

可选的进阶方向：

- Go memory model（happens-before 关系）
- `runtime.GOMAXPROCS` 和调度器行为
- pprof 分析 goroutine 数和锁竞争
- channel 的底层实现（hchan 结构）
