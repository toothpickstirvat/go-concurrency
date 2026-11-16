# 第 9 节：定时器 Timer 和 Ticker

## 为什么单独学

Go 的 `time.Timer` 和 `time.Ticker` 看起来简单，但有几个经典陷阱：忘记 `Stop` 会导致 goroutine 泄漏、`Reset` 的时机不对会丢失
tick、在循环里用 `time.After` 会创建大量 timer。这一节把这些坑都踩一遍。

---

## 要学的内容

1. `time.After(dur)` 返回一个 channel，到点后往里发一个值。适合一次性等待，不适合放循环里。
2. `time.NewTimer(dur)` 可以 `Stop()` 和 `Reset()`，适合循环里复用。
3. `time.Tick(dur)` 返回一个 channel，周期性发值。但没法 Stop，会泄漏。`time.NewTicker` 才是正确的。
4. 在 select 里用 timer 的正确写法

---

## 任务

在 `09-timer-ticker/main.go` 里写三个小程序。

### 任务 A：错误的循环超时

故意写一个错误版本：在 `for` 循环里用 `select { case <-time.After(1*time.Second): ... }`，循环 100 万次。观察内存或
goroutine 数增长（用 `runtime.NumGoroutine()` 打印）。

然后改正：用 `time.NewTimer` 在循环外创建，循环里 `Reset`。

### 任务 B：Ticker 周期任务

用 `time.NewTicker(100ms)` 每 100ms 打印一次时间。10 次后停止。确认 `ticker.Stop()` 被调用。

### 任务 C：超时 + 取消

一个 worker 做耗时操作，同时有两个退出条件：ctx 取消、超时 500ms。用 `select` 同时等 `ctx.Done()` 和
`time.NewTimer(500ms).C`。哪个先到就退出，打印是哪个触发的。

---

## 写之前先想清楚

1. `time.After` 在循环里为什么泄漏？它创建的 timer 什么时候被 GC？
2. `time.NewTimer` 的 `Reset` 为什么要先 `Stop`？如果不 Stop 直接 Reset 会怎样？（提示：看文档关于"drain the channel"的部分）
3. `time.Ticker` 和 `time.NewTicker` 的区别是什么？前者为什么危险？

---

## 自查方法

```bash
go run -race ./09-timer-ticker
```

任务 A：错误版本 goroutine 数会涨到几十万；改正后保持稳定。
任务 B：打印 10 次后退出，没有泄漏。
任务 C：根据超时设置，打印"超时"或"被取消"。

---

## 对应 gmaker 的代码

- `internal/engine/engine_series.go` 里有大量 `time.After` 用于结算等待和超时，但它们都在 select 里一次性使用，不在循环里复用
- `internal/engine/engine.go` 里用 `time.NewTicker` 做周期性余额刷新（每 3 秒），并在退出时 `Stop`
