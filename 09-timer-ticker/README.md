# 第 9 节：定时器 Timer 和 Ticker

## 为什么单独学

Go 的 `time.Timer` 和 `time.Ticker` 看起来简单，但网上很多"经典陷阱"已经过时了：Go 1.23 改了定时器的实现，没被引用的定时器
即使没 Stop 也会被 GC 回收，`Reset` 前也不再需要手动清空 channel。本机是 Go 1.27，这一节用实验分清哪些坑还在、哪些已经没了，
以及旧代码里那些写法是为了解决什么问题。

---

## 要学的内容

1. `time.After(dur)` 返回一个 channel，到点后往里发一个值。适合一次性等待；放在循环里每轮都会新建一个定时器。
2. `time.NewTimer(dur)` 可以 `Stop()` 和 `Reset()`，适合循环里复用。
3. `time.Tick(dur)` 返回一个周期性发值的 channel，但拿不到 Ticker 对象，没法 Stop；`time.NewTicker` 可以 Stop。
4. 在 select 里用 timer 的正确写法
5. Go 1.23 前后定时器行为的变化：哪些旧写法（Stop 后 drain channel）现在不需要了

---

## 任务

在 `09-timer-ticker/main.go` 里写三个小程序。

### 任务 A：循环里的 time.After vs 复用 Timer

写两个版本，各循环 100 万次。每轮先往一个缓冲为 1 的 channel 放一个数，再用 select 同时等这个 channel 和超时（超时设 1 分钟，
保证永远不会触发）：

- 版本 1：每轮 `case <-time.After(time.Minute):`
- 版本 2：循环外 `t := time.NewTimer(time.Minute)`，循环里每轮 `t.Reset(time.Minute)`

用 `runtime.ReadMemStats` 对比两个版本的**分配次数**（`Mallocs` 的差值）、**堆内存**（`HeapInuse`，先 `runtime.GC()`），
以及总耗时；顺便打印 `runtime.NumGoroutine()`。

### 任务 B：Ticker 周期任务

用 `time.NewTicker(100ms)` 每 100ms 打印一次时间。10 次后停止。确认 `ticker.Stop()` 被调用。

### 任务 C：超时 + 取消

一个 worker 做耗时操作，同时有两个退出条件：ctx 取消、超时 500ms。用 `select` 同时等 `ctx.Done()` 和
`time.NewTimer(500ms).C`。哪个先到就退出，打印是哪个触发的。

---

## 写之前先想清楚

1. 网上常说"循环里的 `time.After` 会泄漏"。它会创建 goroutine 吗？Go 1.23 之前和之后，它创建的 timer 分别什么时候能被 GC？
2. 旧文档要求 `Reset` 前先 `Stop` 并清空（drain）channel，否则可能读到上一轮残留的值。Go 1.23 起还需要吗？为什么？
3. `time.Tick` 和 `time.NewTicker` 的区别是什么？前者在 Go 1.23 之前为什么危险，现在呢？

答案在文末。

---

## 自查方法

```bash
go run -race ./09-timer-ticker
```

任务 A：两个版本 goroutine 数都是 1、堆内存都不涨（Go 1.23+）；区别在分配次数和耗时。
任务 B：打印 10 次后退出，没有泄漏。
任务 C：三组参数分别打印"完成"、"被取消"、"超时"。

---

## 实验结果

### 任务 A：循环里的 time.After vs 复用 Timer

100 万轮，超时 1 分钟（永远不会触发），不带 `-race` 连续跑 3 次：

| 版本 | 耗时 | 分配次数 | 每轮 | goroutines |
|------|------|----------|------|------------|
| V1：每轮 `time.After` | 约 230–405ms | 约 300 万次 | 3 次 | 1 |
| V2：循环外 `NewTimer`，每轮 `Reset` | 约 110–125ms | 4 次 | 0 次 | 1 |

另外单独测了 V1 的堆内存：每 25 万轮 `runtime.GC()` 一次，`HeapInuse` 一直在 0.4–0.5MB，没有增长。

- **goroutine 数一直是 1**：`time.After` 从来不创建 goroutine。原来 README 写的"goroutine 数会涨到几十万"是错的，把定时器和 goroutine 混为一谈了。
- **内存不泄漏（Go 1.23+）**：没人引用的定时器即使没到期、没 Stop，也能被 GC 回收。Go 1.23 之前，定时器要到期才能回收，1 分钟内的 100 万个都会留在内存里。
- **代价在分配**：V1 每轮 3 次分配，GC 工作量大，耗时也更不稳定（405ms 那次就是 GC）。V2 的 `Reset` 只是改已有定时器的到期时间，不分配。
- 想切回旧行为对比也不行：Go 1.27 删掉了 `GODEBUG=asynctimerchan=1`，设置后程序启动就 fatal。

### 任务 B：Ticker

正常节奏：

```
tick  1: 101ms
tick  2: 200ms
...
tick 10: 1.001s
```

第 3 次 tick 里多睡 250ms：

```
tick  3: 301ms   ← 开始睡 250ms，551ms 才处理完
tick  4: 552ms   ← 醒来立刻收到 400ms 那次攒下的 tick
tick  5: 600ms   ← 回到原来的 100ms 刻度
...
tick 10: 1.101s  ← 500ms 那次被丢了，拿满 10 次晚了 100ms
```

**Ticker 最多保留一个没取走的 tick**，多出来的直接丢掉。处理慢了不会积压一串 tick 连续执行，节奏也不会整体后移，但执行次数不保证。

### 任务 C：完成 / 取消 / 超时

两个版本结果相同：

| work | cancelAfter | 结果 | 耗时 |
|------|-------------|------|------|
| 200ms | 1s | 完成 | 201ms |
| 1s | 300ms | 被取消：`context canceled` | 301ms |
| 1s | 1s | 超时 | 500ms |

---

## 调试过程中踩的坑

### 任务 B 第一版：死锁

用了 `time.NewTimer` 而不是 `time.NewTicker`。Timer 只触发一次：第 100ms 打印一行后，循环回来又等 `<-t.C`，再也不会有值。
main 是唯一的 goroutine，运行时检测到所有 goroutine 都在睡且没有会触发的定时器，报 `fatal error: all goroutines are asleep - deadlock!`。

### 任务 B 第二版：打印了 11 次

counter 从 0 开始，判断写成 `counter > 10`，0 到 10 一共 11 次。改成先 `counter++` 再打印，`counter == 10` 时 return，第 10 次打印完立刻退出，不用多等一个 tick。

---

## 核心机制

### Timer 和 Ticker

| | Timer | Ticker |
|---|---|---|
| 触发 | 一次 | 每隔 d 一次，直到 Stop |
| 再次触发 | `Reset(d)` | 自动；`Reset(d)` 改周期 |
| 处理慢时 | — | 最多保留一个 tick，多余的丢弃 |
| 创建 | `time.NewTimer(d)`、`time.After(d)`、`time.AfterFunc(d, f)` | `time.NewTicker(d)`、`time.Tick(d)` |

`time.After(d)` 等于 `time.NewTimer(d).C`，拿不到 Timer 对象，没法 Stop 或 Reset。`time.Tick(d)` 同理，拿不到 Ticker。
`time.AfterFunc(d, f)` 到期后在新的 goroutine 里执行 f，适合"到时间做一件事"，比如 `time.AfterFunc(d, cancel)`。

### Go 1.23 的两个变化

1. **没被引用的 Timer/Ticker 可以被 GC 回收**，不需要 Stop。之前必须到期（Ticker 永远不到期）才能回收，所以 `time.Tick` 曾经是"必定泄漏"的。
2. **Timer 的 channel 变成无缓冲（同步）**：`Stop`/`Reset` 返回后，保证不会再收到旧的值。之前 channel 缓冲为 1，到期时值已经放进去了，Reset 后还能读到旧值，所以要先 drain：

```go
// Go 1.23 之前的写法
if !t.Stop() {
    <-t.C   // 读掉残留的值（注意：如果已经被别人读过，这里会永远阻塞，所以实际更复杂）
}
t.Reset(d)

// Go 1.23 起
t.Reset(d)
```

本机实测（Go 1.27）：

```
定时器已到期、值没被读，再 Stop：Stop 返回 true    ← 新语义下到期没读的值被一起撤销
值已经被读过，再 Stop：Stop 返回 false，接着 <-t.C 阻塞 500ms 仍读不到
```

所以旧写法 `if !t.Stop() { <-t.C }` 在"值已经被 select 读走"的情况下会永远阻塞。

这两个变化只对 go.mod 里 `go 1.23` 及以上的模块生效。本项目是 `go 1.27.3`；gmaker 是 `go 1.25.0`，也是新行为。

### 超时的两种写法

```go
// 写法 1：超时也是一个 Timer，select 等三个 case
timeout := time.NewTimer(500 * time.Millisecond)
defer timeout.Stop()
select {
case <-done.C:     // 完成
case <-ctx.Done(): // 被取消
case <-timeout.C:  // 超时
}

// 写法 2：超时交给 context，select 只等两个 case，用 ctx.Err() 区分
ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
defer cancel()
select {
case <-done.C:
case <-ctx.Done():
    if errors.Is(ctx.Err(), context.DeadlineExceeded) { /* 超时 */ } else { /* 被取消 */ }
}
```

写法 2 更常用：ctx 可以继续传给下游（HTTP、数据库），超时一起传下去。写法 1 的超时只对这一个 select 有效。

---

## 结论

1. **Go 1.23 之后，循环里的 `time.After` 不再泄漏**，但每轮分配 3 次。普通循环可以用；高频循环（每秒几万次以上）复用 Timer + `Reset`。
2. **`time.After` 不创建 goroutine**，"goroutine 暴涨"的说法是错的。
3. **Timer 只触发一次，Ticker 周期触发。** 用错了 Timer 去做周期任务，第二次就永远等不到。
4. **Ticker 处理慢时丢 tick**：最多攒一个，节奏不后移。不要靠数 tick 来计次。
5. **Go 1.23 起 `Reset` 前不需要 Stop + drain。** 网上和旧代码里的 drain 写法是给旧版本用的。
6. **需要超时时优先 `context.WithTimeout`**，用 `ctx.Err()` 区分超时和取消。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| 用 Timer 做周期任务 | 第一次之后永远等不到，单 goroutine 时 deadlock | 用 Ticker，或者每次触发后 Reset |
| 高频循环里每轮 `time.After` | 大量分配，GC 压力大、耗时抖动 | 循环外 `NewTimer`，每轮 `Reset` |
| 以为 `time.After` 会让 goroutine 暴涨 | 排查方向错误 | 它只创建定时器，不创建 goroutine |
| 在 Go 1.23+ 照抄旧的 drain 写法 `if !t.Stop() { <-t.C }` | 新语义下 Stop 后 channel 里不会有值，`<-t.C` 可能永远阻塞 | 直接 `t.Reset(d)` |
| 以为处理慢了 Ticker 会补发 | 按 tick 计次会少算 | 需要精确次数就自己计时间或计数 |
| 计数判断差一（`> 10`） | 多执行一次，还多等一个周期 | 先计数再判断，`== 10` 立刻 return |
| 多个实例同时创建 Ticker | 周期相位对齐，同时打到共享资源上 | 创建前错峰（见 gmaker worker.go） |
| 测耗时带 `-race` | 耗时放大，结论失真 | 正确性用 `-race`，性能不带 |

---

## 最佳实践

- 一次性等待：`select` 里用 `time.After` 就行，简单清楚。
- 循环里的超时：循环外 `t := time.NewTimer(d)`，`defer t.Stop()`，每轮 `t.Reset(d)`。
- 周期任务：`t := time.NewTicker(d)`，紧跟 `defer t.Stop()`，`for { select { case <-t.C: ...; case <-ctx.Done(): return } }`。
- 到时间做一件事：`time.AfterFunc(d, f)`，比"起 goroutine 再 Sleep"短，还能 Stop。
- 超时控制优先用 `context.WithTimeout`，方便往下游传。
- Stop 仍然值得写：表达"不再需要了"，也兼容旧版本 Go。

---

## 思考题和答案

1. **循环里的 `time.After` 会创建 goroutine 吗？它的 timer 什么时候能被 GC？**
   不创建 goroutine，只创建定时器（Timer 对象 + channel）。Go 1.23 之前，定时器要到期才会从运行时的定时器堆里移除，才能被 GC，
   所以超时 1 分钟的循环会在内存里堆积 1 分钟内创建的所有定时器。Go 1.23 起，没被引用的定时器随时可以被 GC，不再堆积，本节实测堆内存不涨。

2. **`Reset` 前还需要先 Stop 并 drain channel 吗？**
   Go 1.23 起不需要。新实现里 Timer 的 channel 是同步的，`Stop`/`Reset` 返回后保证不会再收到旧值，直接 `Reset` 就行。
   Go 1.23 之前 channel 缓冲为 1，到期时值已经放进缓冲区，不 drain 就 Reset，下一轮会立刻读到上一轮的旧值，所以要 `if !t.Stop() { <-t.C }`。

3. **`time.Tick` 和 `time.NewTicker` 的区别？前者为什么曾经危险？**
   `time.Tick(d)` 只返回 channel，拿不到 Ticker 对象，没法 Stop。Go 1.23 之前，Ticker 不 Stop 就永远不会被回收，在函数里调用 `time.Tick`
   每次都泄漏一个。Go 1.23 起没被引用的 Ticker 也能被 GC，`time.Tick` 不再泄漏，但仍然没法主动停止，长期运行的服务里还是用 `NewTicker` + `defer Stop()` 更清楚。

---

## 对应 gmaker 的代码

- `internal/engine/engine_series.go:91` 等多处在 select 里用 `time.After` 做等待和超时（5s、10s、2s 等）
- `internal/engine/engine.go:502` 余额刷新：`time.NewTicker(3 * time.Second)` + `defer ticker.Stop()`，select 同时等 `ctx.Done()`
- `internal/engine/worker.go:553` 启动错峰：`time.NewTimer` 等一段固定延迟，select 同时等 `ctx.Done()`，ctx 先到时 `stagger.Stop()`
- `internal/engine/worker.go:563` requote ticker：注释（worker.go:543 起）解释了为什么要先错峰再创建 ticker。Ticker 的相位取决于创建时刻，
  多个市场同时创建会让周期永久对齐，每次触发同时打到共享限流器上
