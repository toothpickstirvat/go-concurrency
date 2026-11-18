# 第 5 节：Select 进阶

## 为什么单独学 Select

fan-out项目里你已经用过`select { case ch <- ev: default: }`做非阻塞写入。`select`
还有几个重要用法：超时控制、优先级选择、多路复用。这些是写真实并发程序时每天都在用的工具。

---

## 要学的内容

1. 超时：`case <-time.After(dur)`给一个操作设上限
2. 优先级：先做一个非阻塞`select`，再做一个阻塞`select`，实现"优先处理某个channel"
3. `default` 的副作用：让select变成非阻塞，但也会让空select变成忙等待（CPU 100%）
4. `select {}` 永久阻塞，什么时候用得上
5. 多个case同时就绪时的随机选择：不要依赖case的顺序

---

## 任务

在`05-select/main.go`里写三个小程序。

### 任务 A：超时控制

一个worker从channel读数据，但如果500ms内没数据就打印"超时"并退出。生产者每1秒发一个数据。

### 任务 B：优先级 channel

有两个数据源channel `high`和`low`。一个消费者循环读取，规则是：只要`high`有数据就先读`high`，`high`空了才读`low`。

提示：用两层select。外层非阻塞试`high`，内层阻塞等`high`或`low`。

### 任务 C：多路复用

两个生产者往同一个channel写数据（fan-in的标准实现）。一个消费者读。写完后关闭channel。注意：有两个写入方，谁来close？用WaitGroup。

---

## 写之前先想清楚

1. `case <-time.After(dur)` 每次进入select都会创建一个新的timer。如果select在循环里，会不会有性能问题？怎么改？
2. 任务B的"优先级"是绝对的还是公平的？如果`high`一直有数据，`low`永远读不到，怎么办？
3. `select { default: }` 放在循环里，如果所有case都不就绪会怎样？

答案在文末。

---

## 自查方法

```bash
go run -race ./05-select
```

任务 A：生产者第 1 秒才发第一个数据，worker 在第 500ms 就打印"超时"并退出。
任务 B：`high` 的数据总是先被消费，`low` 在 `high` 空了后才被消费。
任务 C：两个生产者的数据交错出现，最后程序正常退出。

---

## 实验结果

```
=== 任务 A ===
worker 超时
=== 任务 B ===
[B] high: 10
[B] high: 20
[B] high: 30
[B] high: 40
[B] high: 50
[B] low:  1
...
[B] low:  5
=== 任务 C ===
[C] 100
[C] 0
[C] 101
[C] 1
...                ← 两个生产者交替出现，每次顺序不同
[C] 104
```

---

## 调试过程中踩的坑

### 任务 A：程序跑了 2 分钟都不退出

第一版有三个问题叠在一起：

1. **超时后没有 `return`**：只打印"超时"又回到循环，永远不会退出。
2. **main 阻塞在 `<-exit`，但没人 close exit**：exit 是 main 用来通知 goroutine 的，main 自己等它就是三方互相等。
   main 应该用 WaitGroup 等 goroutine 退出。
3. **读 channel 没检查 `ok`**：channel 关闭后，`case v := <-ch` 每次都会立刻以零值触发，变成死循环。

### 任务 B 第一版：low 的数据一个都没读到

用 `done` channel 通知退出，`go taskB()` 后立刻 `close(done)`。high 读空后进内层 select，这时 low 有数据、done 也已关闭，
**两个 case 同时就绪，Go 随机选**，选到 done 就直接退出了。

教训：**select 多个 case 同时就绪时是随机的，不能靠 case 顺序保证"先读完数据再退出"。** 改成关闭数据 channel 本身，
读空后自然得到 `ok=false`，就不存在这种竞争。

### 任务 B 第二版：死循环、CPU 100%

1. 内层 select 加了 `default`：high、low 都暂时没数据时走 default，什么也不做又回到 for，就是忙等待。
2. 退出条件写成 `high == nil || low == nil`：一个读完就退出，另一个剩下的数据被丢掉。应该用 `&&`。

### 任务 C：panic: sync: negative WaitGroup counter

`wg3` 从来没有 `Add`，goroutine 一 `Done` 计数就变成负数。另外第一版只有一个生产者，碰不到"多个写入方谁来 close"的问题。

---

## 核心机制

### select 的选择规则

1. 所有 case 的 channel 表达式先求值一遍。
2. 有就绪的 case：**随机**选一个执行（防止饥饿，不是按书写顺序）。
3. 没有就绪的 case：有 `default` 就执行 default，没有就阻塞，直到某个 case 就绪。

| 写法 | 行为 |
|------|------|
| `select { case ...: }` | 阻塞，直到某个 case 就绪 |
| `select { case ...: default: }` | 非阻塞，试一次就走 |
| `select {}` | 永久阻塞，不占 CPU（比如 main 启动服务后挂住；但通常用信号或 ctx 更好） |
| 循环里 `select { ... default: }` 且 default 什么也不做 | 忙等待，CPU 100% |

### nil channel 和 closed channel

| 操作 | nil channel | closed channel |
|------|-------------|----------------|
| 读 | 永久阻塞 | 立刻返回零值，`ok=false`（缓冲里有数据时先读完数据） |
| 写 | 永久阻塞 | **panic** |
| close | **panic** | **panic** |

利用"nil channel 永远不会被 select 选中"：channel 关闭后把变量置 nil，就从 select 里注销了这个 case。

### 优先级的两层 select

```go
for {
    select {               // 外层：非阻塞试 high
    case v := <-high: 处理; continue
    default:
    }
    select {               // 内层：阻塞等任意一个，不能带 default
    case v := <-high: 处理
    case v := <-low:  处理
    }
}
```

- 单个 select 做不到优先级，因为就绪 case 是随机选的。
- 内层 high 和 low 同时就绪时仍然随机，所以这是"尽量优先"。
- high 持续有数据时 low 会饿死。

### 多个写入方：谁来 close

```go
var wg sync.WaitGroup
wg.Add(2)
go 生产者1() // 写完 Done，不 close
go 生产者2()
go func() { wg.Wait(); close(ch) }() // 只关一次
for v := range ch { ... }           // close 后 range 自动结束
```

---

## 结论

1. **select 选中就绪 case 是随机的**，不要依赖 case 的书写顺序；要优先级就用两层 select。
2. **`default` 让 select 变成非阻塞**：用于"试一下"（非阻塞发送/接收、丢弃满的事件），不能用在需要等待的地方。
3. **从 channel 读要检查 `ok`**，关闭后置 nil 注销 case。
4. **只有写入方 close；多个写入方时由 WaitGroup + 专门的 goroutine close。**
5. **超时有两种语义**：循环里每轮 `time.After` 是"两次数据间隔超过 dur"；要限制总时长，定时器或 ctx 放在循环外创建。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| 超时 case 里只打印不 return | 永远不退出 | 超时后 return 或 break 出循环 |
| main 阻塞在用来通知 goroutine 的 channel 上 | 没人 close，main 永远卡住 | main 用 WaitGroup 等 goroutine 退出 |
| `case v := <-ch` 不检查 ok | channel 关闭后零值死循环 | `v, ok := <-ch`，`!ok` 时退出或置 nil |
| 以为 case 顺序就是优先级 | 同时就绪时随机选，结果不确定 | 两层 select |
| 退出信号和数据同时就绪 | 随机选到退出，数据没读完 | 关闭数据 channel 本身，读空自然退出 |
| 阻塞等待的 select 里加 default | 忙等待，CPU 100% | 需要等待就不加 default |
| 多个 channel 都关闭才退出，写成 `\|\|` | 一个读完就退出，丢另一个的数据 | `high == nil && low == nil` |
| 每个写入方各自 close | `close of closed channel` 或 `send on closed channel` panic | WaitGroup + 专门的 goroutine close |
| `wg.Add` 漏写 | `negative WaitGroup counter` panic，或提前返回 | 每启动一个 goroutine 先 `Add(1)` |
| 发送语句不在 select 里 | 接收方已经退出时发送方永远阻塞 | 发送也放进 select，同时监听退出信号 |

---

## 最佳实践

- 每个可能阻塞的 select 都加一个退出 case（`<-ctx.Done()` 或 `<-done`），发送和接收都一样。
- 需要丢弃时（行情这类非关键事件）用 `select { case ch <- v: default: 记日志 }`；关键事件不能丢，就阻塞发送，但要同时监听对方是否已退出（见 gmaker 重连广播）。
- 高频循环里的超时用 `time.NewTimer` + `Reset`，或 `time.Ticker`，不要每轮 `time.After`。
- 能用 `for v := range ch` 就不要手写 select + ok 判断。

---

## 思考题和答案

1. **循环里的 `time.After` 有性能问题吗？**
   每轮都新建一个定时器。Go 1.23 之前，ctx 先就绪时这个定时器要等到期才能被回收，高频循环里会堆积大量定时器；Go 1.23 起
   不被引用的定时器会被 GC 回收，不再泄漏，但每轮仍有一次分配。高频循环里改成在循环外 `t := time.NewTimer(d)`，每轮
   `t.Reset(d)`。还要注意语义：每轮新建表示"距上一条数据超过 d"，循环外创建表示"总共只等 d"。

2. **任务 B 的优先级是绝对的还是公平的？**
   绝对的（而且内层同时就绪时还带点随机）。high 一直有数据，low 就永远读不到，即饥饿。缓解办法：连续读了 N 个 high 后强制读一次
   low；或者按权重轮询；或者干脆给 low 独立的消费者。

3. **循环里 `select { default: }`，所有 case 都不就绪会怎样？**
   每次都立刻走 default，然后回到循环开头再试，goroutine 一直占着 CPU 空转（CPU 100%），这就是忙等待。需要等待时去掉 default，
   让 select 阻塞；确实需要轮询时，在 default 里加 sleep 或改用 Ticker。

---

## 对应gmaker的代码

- `internal/engine/engine_dispatch.go:97` fanOut主循环：外层`select`同时等`ctx.Done()`和`src`
  channel；收到行情事件后，内层用非阻塞`select { case ch <- ev: default: slog.Warn(...) }`往mktDataCh/featuresCh
  广播，满了就丢弃并打日志
- `internal/engine/engine_dispatch.go:150` 重连广播用三路阻塞 select：`case ch <- ev` / `case <-w.Done()` /
  `case <-ctx.Done()`。关键事件不能丢，所以不用default；但worker可能已退出，直接阻塞会卡死，用`w.Done()`做保护
- `internal/engine/engine_series.go` 大量嵌套`select`：同时等 ctx.Done、定时器、settlement事件等多个channel，是多路复用的典型写法
