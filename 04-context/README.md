# 第 4 节：Context

## 为什么学 Context

fan-out 项目里我们靠 close channel 通知 goroutine 退出。但真实程序里 goroutine 会层层派生：一个请求开 3 个 worker，每个
worker 又开 2 个子任务。要取消时，需要一次取消整棵树，而且要传超时、传请求级别的值。`context.Context` 就是干这个的。

---

## 要学的内容

1. `context.Background()` 和 `context.TODO()` 的区别
2. `WithCancel(parent)` —— 父取消时所有子也取消
3. `WithTimeout(parent, dur)` —— 到时间自动取消
4. `WithValue(parent, key, val)` —— 传请求级别的值（要慎用）
5. `ctx.Done()` channel、`ctx.Err()` 错误
6. 传播规则：函数第一个参数是 `ctx context.Context`，调用下游时传 `ctx` 而不是 `Background()`

---

## 任务

在 `04-context/main.go` 里写一个**模拟请求处理**。

### 要求

1. main 创建一个 `WithTimeout` 的 ctx，超时 4 秒。
2. 启动 3 个 worker goroutine，每个 worker 模拟做一件耗时的事（用 `select` 同时等 `ctx.Done()` 和一个 `time.After`）：
   - worker 1：1 秒完成
   - worker 2：3 秒完成
   - worker 3：5 秒完成
3. 每个 worker 退出时打印"完成"或"被取消"和原因（用 `ctx.Err()`）。
4. main 等所有 worker 退出后打印总耗时。

### 加分项

1. 在 worker 内部再派生一个子 goroutine，确认父 ctx 取消时子也能退出（取消是传播的）。
2. 用 `WithValue` 传一个 `requestID`，每个 worker 和子 goroutine 打印时带上它。

---

## 写之前先想清楚

1. `ctx.Done()` 返回的 channel 被 close 时，`ctx.Err()` 会返回什么？是 `context.Canceled` 还是 `context.DeadlineExceeded`？取决于什么？
2. 在 worker 里 `select { case <-time.After(3*time.Second): ... }`，如果 ctx 先取消了，这个 `time.After` 创建的定时器
   会不会泄漏？（提示：Go 1.23 前后答案不同。本机是 Go 1.27。对比 `time.NewTimer` + `defer t.Stop()` 的写法）
3. 为什么 `ctx` 应该作为函数第一个参数，而不是放进结构体？

答案在文末。

---

## 自查方法

```bash
go run -race ./04-context
```

超时 4 秒时：worker 1、2 完成，worker 3 被取消，总耗时约 4 秒。

---

## 实验结果

超时 4 秒，worker 耗时分别是 1s / 3s / 5s，每个 worker 还派生了一个 10 秒的子 goroutine，requestID = "req-42"：

```
[req-42] worker 1 完成
[req-42] child 1 子任务被取消, err: context canceled
[req-42] worker 2 完成
[req-42] child 2 子任务被取消, err: context canceled
[req-42] worker 3 被取消, err: context deadline exceeded
[req-42] child 3 子任务被取消, err: context deadline exceeded
elapsed: 4.000878676s
```

各 goroutine 的退出时间：

| goroutine | 退出时刻 | Err() | 原因 |
|-----------|----------|-------|------|
| worker 1  | 第 1 秒  | —     | 正常完成 |
| child 1   | 第 1 秒  | `context.Canceled` | worker 1 完成后主动 `cancel()` |
| worker 2  | 第 3 秒  | —     | 正常完成 |
| child 2   | 第 3 秒  | `context.Canceled` | worker 2 完成后主动 `cancel()` |
| worker 3  | 第 4 秒  | `context.DeadlineExceeded` | 根 ctx 超时 |
| child 3   | 第 4 秒  | `context.DeadlineExceeded` | 取消从父 ctx 传下来，原因继承 |

---

## 核心机制

### 取消树：父取消，所有子孙跟着取消

`WithCancel` / `WithTimeout` / `WithDeadline` 都会把新 ctx 注册为父 ctx 的子节点。父 ctx 取消时，
运行时遍历所有子节点一次性关闭。子 ctx 取消**不会向上传播**，只往下。

```
Background
  └── ctx (WithTimeout 4s)         ← 超时时关闭整棵树
        ├── worker1Ctx (WithCancel) ← worker 1 完成后主动关闭
        │     └── child1Ctx (WithCancel)
        ├── worker2Ctx (WithCancel)
        │     └── child2Ctx (WithCancel)
        └── worker3Ctx (WithCancel)
              └── child3Ctx (WithCancel)
```

### ctx.Err() 返回第一个取消原因

- 有人调用了 `cancel()`：`context.Canceled`
- 某一层的 deadline 到了：`context.DeadlineExceeded`
- 子 ctx 因为父 ctx 取消而跟着取消时，**继承父 ctx 的原因**。第 4 秒根 ctx 超时，child 3 拿到的是 `DeadlineExceeded`，不是 `Canceled`。
- 第一个原因生效后不会再变：worker 3 返回时调用了 `cancel()`，但 child 3 的 Err() 已经是 `DeadlineExceeded`，不会变成 `Canceled`。

### 等子 goroutine 退出之前必须先通知它退出

```go
// 错误写法：死锁
childCtx, cancel := context.WithCancel(ctx)
defer cancel()          // ← 要等函数返回才执行
go child(childCtx, ch)
select { ... }
<-ch                    // ← 等 child 退出；child 在等 childCtx.Done()；Done 要等 cancel()；cancel() 要等这里返回
                        // 两边互相等，靠父 ctx 超时兜底，提前完成的 worker 会卡到超时才退出

// 正确写法
childCtx, cancel := context.WithCancel(ctx)
defer cancel()
go child(childCtx, ch)
select { ... }
cancel() // 先通知
<-ch     // 再等
```

### WithValue：用未导出类型当 key

```go
type ctxKey struct{}            // 未导出，包外构造不出相同的 key
ctx = context.WithValue(ctx, ctxKey{}, "req-42")

v, ok := ctx.Value(ctxKey{}).(string)  // 取值需要类型断言；不存在时 v 是 ""
```

Value 查找沿着 ctx 树往上找：子 ctx 自己没有这个 key，就去父 ctx 找。所以 child 传进去的 childCtx 也能拿到根 ctx 里的 requestID。

---

## 结论

1. **`ctx` 是取消信号 + 超时 + 值的统一载体**，层层往下传，不要放进结构体字段里。
2. **`WithCancel` / `WithTimeout` 返回的 cancel 一定要调用**，`defer cancel()` 是标准写法。多调用一次没有影响。
3. **等子 goroutine 之前先 cancel()**，不要只依赖 defer，否则工作提前完成时仍然卡到超时。
4. **`WithValue` 只传请求级别的横切元数据**（链路追踪 ID、认证 token），不传业务参数或控制流。
5. **`ctx.Err()` 是第一个原因，不会变**：判断是超时还是主动取消用 `errors.Is(err, context.DeadlineExceeded)`。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| `_, cancel := WithTimeout(...)` 丢掉 cancel | ctx 和定时器直到超时才释放，`go vet` 报警告 | `defer cancel()` |
| 等子 goroutine 退出前没有先 cancel | 子 goroutine 卡到父 ctx 超时才退出 | 先 `cancel()`，再 `<-done` |
| 用字符串或内置类型当 WithValue 的 key | 不同包的 key 相等，互相覆盖；`go vet` 报警告 | 用未导出的自定义类型 |
| 直接断言 `ctx.Value(key).(string)` | key 不存在时 value 是 nil，panic | 先判断 ok：`v, ok := ctx.Value(key).(string)` |
| ctx 放进结构体 | goroutine 生命周期和 ctx 生命周期脱钩，难以取消 | ctx 作为函数第一个参数 |
| 用 WithValue 传业务参数 | 类型不安全，函数签名看不出依赖，难以测试 | 显式参数 |
| 子 ctx 被取消，以为 Err() 是 Canceled | 实际继承的是父 ctx 的原因，可能是 DeadlineExceeded | 用 `errors.Is` 判断，不要直接比较字符串 |

---

## 最佳实践

- 函数签名：`func foo(ctx context.Context, ...)`, ctx 永远是第一个参数。
- 不调用下游就不需要 ctx；需要时从参数取，不要从结构体字段取。
- 每一层派生时考虑是否需要**独立取消**：需要就 `WithCancel`，需要超时就 `WithTimeout`，只是透传就直接传 ctx。
- 收到 ctx 的函数，做任何阻塞操作时都要在 select 里同时监听 `ctx.Done()`，不能只睡眠或只等 channel。
- `context.Background()` 用于进程顶层（main、测试、全局初始化）；`context.TODO()` 用于还不知道该传哪个 ctx 的临时占位。

---

## 思考题和答案

1. **`ctx.Err()` 是 Canceled 还是 DeadlineExceeded？**
   取决于谁先触发：有人调用 cancel 返回 `Canceled`；deadline/timeout 到期返回 `DeadlineExceeded`。子 ctx 继承第一个触发的原因。用 `errors.Is(ctx.Err(), context.DeadlineExceeded)` 判断。

2. **`time.After` 会泄漏吗？**
   Go 1.23 之前：会。`time.After` 内部创建一个 Timer，如果 ctx 先取消，select 走 Done 分支，这个 Timer 在超时前不会被 GC 回收，goroutine 一直存在直到 Timer 到期。Go 1.23 起：不会。运行时改为当 Timer 不再被引用时就可以被 GC，不需要 Stop。本机 Go 1.27 不会泄漏。但为了兼容旧版本、也为了显式表达意图，可以用 `time.NewTimer(d)` + `defer t.Stop()`。

3. **为什么 ctx 不放进结构体？**
   ctx 表示"这次调用"的生命周期，结构体表示一个对象的生命周期。两者不一样长。把 ctx 放进结构体，调用方就无法控制单次操作的超时和取消，只能控制整个对象的生命周期。gRPC、database/sql 等标准库都把 ctx 作为每个方法的第一个参数。

---

## 对应 gmaker 的代码

- `internal/engine/engine.go:292` `loadRunCtx()` 返回引擎的根 ctx（存在 `atomic.Value` 里），所有 goroutine 派生于它
- `internal/engine/engine_dispatch.go:25` `context.WithCancel(engine.loadRunCtx())` 给每个 dispatcher 一个可独立取消的子 ctx
- `internal/engine/engine_dispatch.go:20` 同一个 marketID 重新注册时，先调用旧的 cancel，让旧 dispatcher 退出，防止 goroutine 泄漏
- `internal/engine/engine_series.go:33` 每个系列的 poller 也有自己的 cancel，存在 `seriesCancels` 里，可以单独停掉
- `internal/engine/engine_hotadd.go:202` 热添加前先检查 `runCtx.Err() != nil`：根 ctx 已取消就不再启动新 goroutine
