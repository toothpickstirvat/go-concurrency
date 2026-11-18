# 第 6 节：Pipeline

## 为什么学 Pipeline

Pipeline是把一个复杂处理拆成多个stage，每个stage是一个goroutine，用channel连起来。好处是每个stage可以独立调并发数、独立背压、独立
cancel。fan-out项目里你已经写过两级的pipeline（路由 → relay → worker），这一节正式写多级版本。

---

## 要学的内容

1. 多级stage的数据流：`stage1 → chan1 → stage2 → chan2 → stage3`
2. 每个stage的并发度可以不同
3. 背压自动传播：下游慢，上游的channel满了，上游自动阻塞
4. 取消传播：ctx取消时，所有stage都能通过`ctx.Done()`退出
5. close传播：上游close它的输出channel，下游的`range`自然结束

---

## 任务

在 `06-pipeline/main.go` 里写一个**数据处理流水线**。

### 数据流

```
生成器 ──► 过滤偶数 ──► 平方 ──► 打印
```

### 要求

1. **stage 1 生成器**：生成 1 到 20 的整数，写进 channel，写完 close。
2. **stage 2 过滤器**：只保留偶数。从 stage 1 的 channel 读，写进 stage 2 的 channel。
3. **stage 3 平方器**：每个数平方。从 stage 2 读，写进 stage 3 的 channel。
4. **stage 4 消费者**：打印结果，统计总数。
5. 每个stage用单独的函数实现，函数签名形如 `func stage2(in <-chan int, out chan<- int)`。
6. main里把所有stage用`go`启动，用WaitGroup等stage 1-3完成，主goroutine跑stage 4。

### 加分项

1. 给每个stage加随机sleep（10-50ms），观察不同stage的速度差异如何自动背压。
2. 把stage 3的并发数改成3（3个goroutine同时跑平方），用WaitGroup在3个都结束后再close out。观察输出顺序。
3. 加一个ctx，超时100ms就取消整个pipeline，确认所有stage都能退出。

---

## 写之前先想清楚

1. 每个stage是它输出channel的唯一写入方吗？那谁来close输出channel？
2. 如果stage 3有3个goroutine同时写一个channel，怎么close？用WaitGroup汇总。
3. 如果中途某个stage出错了（比如除零），怎么把错误传出来，并让整个pipeline停下来？（提示：加一个`errCh chan error`，或者用下一节的
   errgroup）

答案在文末。

---

## 自查方法

```bash
go run -race ./06-pipeline
```

应该打印 2、4、6、8、10、12、14、16、18、20 的平方：4、16、36...400。
stage3 并发且每个数的处理时间波动比数据到达间隔大时，顺序会乱（见实验结果），但总数必须是 10。

---

## 实验结果

### 基础版（无 sleep、无超时）

```
4
16
36
64
100
144
196
256
324
400
总共 10 个数
```

1 到 20 里的偶数（2, 4, ..., 20）平方后得到 4, 16, ..., 400，共 10 个数。

### 加分项 1：随机 sleep 观察背压

每个 stage 加 10–50ms 随机延迟，总耗时约 820ms：

```
[   151ms] 4
[   220ms] 16
[   299ms] 36
[   369ms] 64
[   484ms] 100
[   526ms] 144
[   595ms] 196
[   693ms] 256
[   753ms] 324
[   821ms] 400
总共 10 个数，耗时 820.996057ms
```

每个数之间间隔约 50–100ms，是三个 stage 各自随机 sleep 叠加的结果。因为所有 channel 都是无缓冲的，每个 stage 写入时必须等下游读走才能继续。如果某个 stage 这一轮的随机 sleep 特别长（比如 50ms），它就会成为瓶颈，整条 pipeline 的速度由最慢的那一级决定。这就是**背压自动传播**。

### 加分项 2：stage3 并发数改为 3

3 个 goroutine 同时从 ch2 读、平方、写进 ch3。单次运行总耗时在 775–820ms 之间，和单 worker 差不多：
瓶颈在 stage1、stage2（每个数各睡 10–50ms），stage3 不是最慢的一级，给它加并发提高不了整体吞吐。

**输出顺序仍然是有序的，给 ch3 加缓冲后也一样。** 原因是 stage3 的处理时间波动比数据到达的间隔小：

- stage2 大约每 60ms 才放出一个偶数（stage1、stage2 各睡 10–50ms，奇数被过滤掉）。
- stage3 每个数处理 10–50ms，最快和最慢只差 40ms。
- 后一个数比前一个晚到约 60ms，就算前一个处理得最慢、后一个最快，后一个也追不上，所以不会乱序。

在 `/tmp` 的副本里验证过（各跑 5 次，统计后一个数比前一个小的次数）：

| stage3 每个数的处理时间 | 乱序次数 |
|---|---|
| 10–50ms（原版） | 0 0 0 0 0 |
| 100–140ms（整体变慢，但波动仍是 40ms） | 0 0 0 0 0 |
| 10–310ms（波动 300ms） | 3 2 2 4 2 |

**乱序取决于处理时间的波动，不是快慢本身。** 多个 worker 并发处理时，只要后到的数可能比先到的数处理得快，顺序就会乱。
pipeline 里一旦某一级并发了，就不能再假设输出有序；需要顺序时要带上序号，在下游重新排序。

### 加分项 3：context 超时取消

超时 200ms 时：

```
[   114ms] 4
[   186ms] 16
总共 2 个数，耗时 200.304357ms
```

打印 2 个数后在第 200ms 停止，所有 stage 都退出了。

超时 2 秒时：

```
总共 10 个数，耗时 820.996057ms
```

正常跑完 10 个数，说明超时没有影响正常流程。

---

## 调试过程中踩的坑

### 第一版 ctx 监听：只检查一次就不管了

```go
func stage1(ctx context.Context, out chan<- int) {
    defer close(out)
    select {
    case <-ctx.Done():
        return
    default:
        for i := 1; i <= 20; i++ {  // ← 循环里不再检查 ctx
            time.Sleep(...)
            out <- i
        }
    }
}
```

这个 select 在函数刚启动时检查一次 ctx，如果那时还没超时就走 default 进入循环，然后在循环里不再检查，一直跑到结束。超时 100ms 时 stage 可能已经在循环里了，就不会退出。

**正确做法：在循环的每一轮都检查 ctx**。发送时用 select 同时等 `out <- v` 和 `ctx.Done()`：

```go
func stage1(ctx context.Context, out chan<- int) {
    defer close(out)
    for i := 1; i <= 20; i++ {
        time.Sleep(...)
        select {
        case out <- i:
        case <-ctx.Done():
            return
        }
    }
}
```

从 channel 读也一样：

```go
for {
    select {
    case v, ok := <-in:
        if !ok { return }
        // 处理 v
    case <-ctx.Done():
        return
    }
}
```

---

## 核心机制

### close 传播链

```
stage1 写完 → close(ch1)
             ↓
stage2 的 for v := range ch1 结束 → defer close(ch2)
                                    ↓
stage3 的 for v := range ch2 结束 → wg.Wait() + close(ch3)
                                    ↓
main 的 for v := range ch3 结束 → 程序退出
```

每个 stage 是它输出 channel 的唯一写入方，写完后 close。下游的 `range` 在 channel 关闭且读空后自动结束，然后下游 close 自己的输出，一级一级传到最后。整条链自动收尾，**不需要 WaitGroup 或额外通知**。

### 多个写入方：用 WaitGroup 汇总后再 close

stage3 有 3 个 goroutine 同时写 `out`，不能在每个 goroutine 里 `close(out)`——第一个 close 后，第二个再 close 会 panic（`close of closed channel`）。

```go
func stage3(ctx context.Context, in <-chan int, out chan<- int) {
    defer close(out)  // ← 或者 wg.Wait() 后再 close(out)，效果一样
    var wg sync.WaitGroup
    for i := 0; i < 3; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            // 不在这里 close
            for { ... }
        }()
    }
    wg.Wait()  // 等 3 个都写完
    // defer close(out) 在函数返回时执行
}
```

`defer close(out)` 写在函数开头或 `wg.Wait()` 之后都可以，效果一样，因为 defer 是在函数返回时执行，那时 `wg.Wait()` 已经返回了。

### 背压自动传播

无缓冲 channel 的写入是阻塞的：写入方必须等读取方读走才能继续。下游处理慢，上游的写入就会阻塞，上游也变慢。这就是背压（backpressure）。

```
stage1 写入 ch1 → 阻塞，等 stage2 读走
                ↓
stage2 读到后写入 ch2 → 阻塞，等 stage3 读走
                      ↓
stage3 读到后写入 ch3 → 阻塞，等 main 读走
```

整条 pipeline 的速度取决于最慢的那一级。带缓冲的 channel 可以缓解短暂的速度差异（缓冲满之前不阻塞），但长期的速度差异最终还是会传导上来。

### context 取消传播

每个 stage 在每轮循环里都用 select 监听 `ctx.Done()`，ctx 取消时所有 stage 几乎同时退出。注意两点：

1. **发送和接收都要监听 ctx**：`select { case out <- v: case <-ctx.Done(): return }`
2. **子 goroutine 也要监听**：stage3 的 3 个 worker 各自有一个 `select { case <-ctx.Done(): return }`

---

## 结论

1. **每个 stage 是它输出 channel 的唯一写入方，用 `defer close(out)` 关闭。** close 自动传播，不需要额外通知。
2. **多个 goroutine 写同一个 channel 时，用 WaitGroup 等所有 goroutine 都结束后再 close**，不能在每个 goroutine 里各自 close。
3. **背压自动传播**：下游慢，上游的 channel 写入阻塞，上游也变慢。整条 pipeline 速度由最慢的一级决定。
4. **context 取消：发送和接收都要在 select 里同时监听 `ctx.Done()`**，子 goroutine 也一样。
5. **单向 channel 类型**（`<-chan` / `chan<-`）让编译器帮你检查读写方向，防止误用。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| 多个 goroutine 各自 close 同一个 channel | `close of closed channel` panic | WaitGroup 等所有写入方结束后只 close 一次 |
| ctx 监听只在函数开头检查一次 | 超时后 stage 不退出，继续跑到结束 | 循环的每一轮都检查，发送和接收用 select 同时监听 ctx.Done() |
| 子 goroutine 不监听 ctx | 父 goroutine 退出了，子 goroutine 还在跑 | 每个 goroutine 都要监听 ctx.Done() |
| stage 里只监听发送，不监听接收 | 上游超时关闭，下游卡在读上永远不退出 | 读和写都用 select 监听 ctx.Done() |
| `defer close(out)` 写在子 goroutine 里 | 每个子 goroutine 都会关一次，panic | defer close 写在父函数里，等 WaitGroup 后再关 |

---

## 最佳实践

- 每个 stage 写成独立函数，签名 `func(ctx, in, out)` 或 `func(ctx, out)`（第一个 stage 没有 in）。
- 参数用单向 channel 类型（`<-chan` / `chan<-`），编译器帮你检查读写方向。
- 函数开头或 `wg.Wait()` 后写 `defer close(out)` 或 `close(out)`，效果一样。
- 每个阻塞操作（读、写、sleep）都要能被 ctx 打断，用 select 同时监听。
- 需要并发处理时（如 stage3 的 3 个 worker），每个 worker 都是一个独立的 goroutine，都要监听 ctx，都不 close out。
- main 跑最后一个 stage（消费者），它的 `range` 结束就说明整条 pipeline 完成了。

---

## 思考题和答案

1. **每个 stage 是它输出 channel 的唯一写入方吗？谁来 close？**
   是的。每个 stage 写完后自己 close 输出 channel。多个 goroutine 写同一个 channel 时，用 WaitGroup 等所有 goroutine 都结束后只 close 一次。

2. **stage3 有 3 个 goroutine，怎么 close？**
   在 stage3 函数里用 WaitGroup 等 3 个 goroutine 都 `Done()`，然后 `close(out)`。不能在每个 goroutine 里各自 close。

3. **某个 stage 出错了，怎么把错误传出来并让整个 pipeline 停下来？**
   - 方法 1：加一个 `errCh chan error`，stage 出错时 `select { case errCh <- err: default: }`，main 同时监听 errCh 和数据 channel。
   - 方法 2：用 `context.WithCancel`，stage 出错时调用 `cancel()`，所有监听 `ctx.Done()` 的 stage 都会退出。
   - 方法 3（推荐）：下一节的 `errgroup`，它把 context 和错误收集封装在一起，一个 goroutine 出错会自动取消整个 group。

---

## 对应 gmaker 的代码

- gmaker 的整个引擎就是一个大 pipeline：WebSocket → fanOut → dispatcher → worker → 下游服务
- `internal/engine/engine_dispatch.go:25-29` 每个市场两级 channel：fanOut → `dispatchCh` → `runMarketDispatcher`（engine_dispatch.go:50）→ `workerCh` → worker。
  每一级只由上一级写入，背压沿着 channel 往回传
- `internal/engine/engine.go:35` `workerChanBufferSize = 4096`、engine.go:42 `dispatchChanBufferSize`：缓冲区只能吸收突发流量，
  注释里记录了缓冲太小导致的问题（docs/buglog.md #51）。这和 fan-out 项目里"缓冲只是推迟阻塞"的结论一致
