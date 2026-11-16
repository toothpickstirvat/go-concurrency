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

1. main 创建一个 `WithTimeout` 的 ctx，超时 2 秒。
2. 启动 3 个 worker goroutine，每个 worker 模拟做一件耗时的事（用 `select` 同时等 `ctx.Done()` 和一个 `time.After`）：
    - worker 1：1 秒完成
    - worker 2：3 秒完成
    - worker 3：5 秒完成
3. 每个 worker 退出时打印"完成"或"被取消"和原因（用 `ctx.Err()`）。
4. main 等所有 worker 退出后打印总耗时。

### 加分项

1. 把超时从 2 秒改成 4 秒，观察哪些 worker 能完成、哪些被取消。
2. 在 worker 内部再派生一个子 goroutine，确认父 ctx 取消时子也能退出（取消是传播的）。
3. 用 `WithValue` 传一个 `requestID`，每个 worker 打印时带上它。思考：为什么 `WithValue` 不应该用来传普通参数？

---

## 写之前先想清楚

1. `ctx.Done()` 返回的 channel 被 close 时，`ctx.Err()` 会返回什么？是 `context.Canceled` 还是 `context.DeadlineExceeded`
   ？取决于什么？
2. 在 worker 里 `select { case <-time.After(3*time.Second): ... }`，如果 ctx 先取消了，这个 `time.After` 的 goroutine
   会不会泄漏？怎么避免？（提示：把 `time.After` 换成 `time.NewTimer` 并 `defer t.Stop()`）
3. 为什么 `ctx` 应该作为函数第一个参数，而不是放进结构体？

---

## 自查方法

```bash
go run -race ./04-context
```

超时 2 秒时：worker 1 完成，worker 2、3 被取消，总耗时约 2 秒。
超时 4 秒时：worker 1、2 完成，worker 3 被取消，总耗时约 4 秒。

---

## 对应 gmaker 的代码

- `internal/engine/engine.go` 里 `loadRunCtx()` 返回引擎的根 ctx，所有 goroutine 派生于它
- `internal/engine/engine_dispatch.go:25` `context.WithCancel(engine.loadRunCtx())` 给每个 dispatcher 一个可独立取消的子
  ctx
- `engine_hotadd.go` 里用 ctx 取消旧 dispatcher 防止 goroutine 泄漏
