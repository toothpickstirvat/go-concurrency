# 第 8 节：信号量

## 为什么学信号量

有时候不是所有goroutine都该无限并发。比如调外部API有QPS限制、打开文件有fd上限、数据库连接池有大小。信号量就是"同时最多
N个"的控制工具。

errgroup 的 `SetLimit` 内部就是一个信号量。这一节我们手动实现，理解原理。

---

## 要学的内容

1. 用buffered channel当信号量：`sem := make(chan struct{}, N)`，`sem <- struct{}{}`占位，`<-sem`释放
2. `golang.org/x/sync/semaphore`的`Weighted`：支持不同权重的任务
3. 信号量和worker pool的区别：worker pool限制goroutine数，信号量限制并发数（goroutine可以多于N）

---

## 任务

在 `08-semaphore/main.go` 里写两个实现。

### 任务 A：用buffered channel限流

模拟100个任务，每个sleep 100ms，但同时最多跑5个。用`make(chan struct{}, 5)`当信号量。打印每个任务开始和结束的时间，确认任意时刻不超过
5个在跑。

### 任务 B：用weighted semaphore

用 `golang.org/x/sync/semaphore.NewWeighted(10)`，有20个任务，权重分别为1、2、3，总权重可能超过10。每个任务
`Acquire(ctx, weight)`，`defer Release(weight)`。观察高权重任务会不会因为配额不足而等待。

---

## 写之前先想清楚

1. 任务A里，如果用 `defer func() { <-sem }()`，defer 放在 `sem <- struct{}{}` 之前还是之后？放错了会怎样？
2. 信号量满了，`sem <- struct{}{}` 是阻塞还是报错？怎么加超时？
3. 任务B里，如果总权重刚好等于容量，所有任务能同时跑吗？

---

## 自查方法

```bash
go get golang.org/x/sync
go run -race ./08-semaphore
```

任务 A：打印的并发开始数不超过 5。总时间约 `100 / 5 * 100ms = 2 秒`。
任务 B：高权重任务在配额不足时等待，最终全部完成。

---

## 实验结果

### 任务 A：buffered channel 信号量

100 个任务，每个 100ms，最多同时 5 个。代码里用一个 atomic 计数器统计"同时在跑"的任务数，并用 CAS 循环记录最大值：

```
[A] 任务 99 开始，同时在跑 2 个 (0s)
[A] 任务 12 开始，同时在跑 3 个 (0s)
[A] 任务 27 开始，同时在跑 1 个 (0s)
[A] 任务  4 开始，同时在跑 4 个 (0s)
[A] 任务 28 开始，同时在跑 5 个 (0s)      ← 前 5 个立刻开始
[A] 任务  5 开始，同时在跑 5 个 (101ms)   ← 之后每结束一个才能进一个
...
[A] 同时在跑的最大值: 5，总耗时: 2.018s
```

- 同时在跑的最大值是 5，信号量生效了。
- 总耗时约 2 秒（100 ÷ 5 × 100ms）。
- 开始的顺序是乱的（99、12、27……）：100 个 goroutine 抢 5 个名额，谁先抢到取决于调度，信号量不保证先来先得。
- "同时在跑"的数字会出现 2、3、1 这种不按顺序的值：`running.Add(1)` 和打印之间别的 goroutine 也在加，打印的是各自加完那一刻的值。

### 任务 B：weighted semaphore

20 个任务，权重轮流 1、2、3，总容量 10。代码里另用一个 atomic 记录已用配额（semaphore 自己不对外暴露这个值）：

```
[B] 任务 19 (权重 2) 开始，已用配额  2/10 (0s)
[B] 任务 10 (权重 2) 开始，已用配额  4/10 (0s)
[B] 任务 11 (权重 3) 开始，已用配额  7/10 (0s)
[B] 任务 12 (权重 1) 开始，已用配额  9/10 (0s)
[B] 任务  9 (权重 1) 开始，已用配额  8/10 (0s)
[B] 任务 13 (权重 2) 开始，已用配额 10/10 (101ms)
[B] 任务 14 (权重 3) 开始，已用配额 10/10 (101ms)
...
[B] 总耗时: 504.67378ms
```

- 已用配额从没超过 10。
- 第一批只用了 9（2+2+3+1+1），第 6 个任务没能进来，因为排在队头的那个任务要的配额比剩下的 1 多。原因见下面的"FIFO 和队头阻塞"。
- 第 5 行显示 8 比第 4 行的 9 小，原因和任务 A 一样：几个 goroutine 同时在改计数器，打印的是各自改完那一刻的值。
- 总配额 10，20 个任务的总权重约 40，每批 100ms，理论上至少要 4 批（400ms）。实测 504ms，是因为配额经常凑不满。

---

## 核心机制

### buffered channel 当信号量

```go
sem := make(chan struct{}, N)  // 容量 N

// 获取
sem <- struct{}{}  // 满了就阻塞

// 释放
<-sem
```

原理：
- channel 容量是 N，写入第 N+1 个元素时阻塞
- 读走一个元素，腾出一个位置，被阻塞的写入者之一被唤醒
- `struct{}{}` 是空结构体，不占内存，只用来占位

**和 Linux C 的 POSIX semaphore 对比：**

| Linux C | Go buffered channel |
|---------|---------------------|
| `sem_init(&sem, 0, 5)` | `make(chan struct{}, 5)` |
| `sem_wait()` 减 1，为 0 时阻塞 | `sem <- struct{}{}` 写入，满时阻塞 |
| `sem_post()` 加 1，唤醒等待者 | `<-sem` 读出，唤醒被阻塞的写入者 |

本质都是一个计数器：
- Linux C 的信号量操作计数器（值从 N 减到 0）
- Go 的 buffered channel 操作缓冲区空位（空位从 N 减到 0）
- 概念上是"反着数"，但效果完全一样

**defer 要写在"拿到信号量"之后：**

```go
// 写法 1：获取总会成功
sem <- struct{}{}
defer func() { <-sem }()

// 写法 2：获取可能失败（加了 ctx）
select {
case sem <- struct{}{}:
case <-ctx.Done():
    return ctx.Err()      // 没拿到，直接返回
}
defer func() { <-sem }()  // 走到这里才说明拿到了
```

写法 1 里，`sem <- struct{}{}` 要么成功、要么一直阻塞，defer 放在它前面和后面效果其实一样。真正出问题的是写法 2：
如果 defer 写在 select 前面，ctx 取消导致没拿到名额时，返回前 defer 照样执行 `<-sem`，读走的是**别人的**名额。
要么让一个本该等待的 goroutine 多进来一个，超过容量；要么 sem 里正好是空的，`<-sem` 永远阻塞。
所以习惯上 defer 总是紧跟在"确认拿到"之后。

### weighted semaphore

```go
sem := semaphore.NewWeighted(10)
sem.Acquire(ctx, 3)  // 获取 3 个配额，不足就阻塞
defer sem.Release(3) // 释放 3 个配额
```

内部实现（摘自 `golang.org/x/sync@v0.25.0/semaphore/semaphore.go`，有删减）：

```go
func (s *Weighted) Acquire(ctx context.Context, n int64) error {
    s.mu.Lock()
    // 快路径：配额够，而且没有人在排队
    if s.size-s.cur >= n && s.waiters.Len() == 0 {
        s.cur += n
        s.mu.Unlock()
        return nil
    }
    if n > s.size {           // 要的比总容量还多，永远拿不到
        s.mu.Unlock()
        <-ctx.Done()          // 一直等到 ctx 结束，然后返回 ctx.Err()
        return ctx.Err()
    }
    // 慢路径：排到等待队列的队尾，等 Release 叫醒，或者 ctx 结束
    ...
}

func (s *Weighted) notifyWaiters() {   // Release 里调用
    for {
        next := s.waiters.Front()
        if next == nil { break }
        w := next.Value.(waiter)
        if s.size-s.cur < w.n {
            break                       // 队头的配额不够，后面的也不看了
        }
        s.cur += w.n
        s.waiters.Remove(next)
        close(w.ready)
    }
}
```

**FIFO 和队头阻塞：**

- 快路径除了配额够，还要求**没有人在排队**。有人排队时，新来的就算配额够也要排到队尾，不会插队。
- Release 时按队列顺序叫醒，**遇到第一个配额不够的就停下**，后面权重小、本来放得下的也不叫醒。
- 好处是大任务不会饿死：如果允许小任务插队，权重 3 的任务可能一直凑不齐配额。代价是吞吐量：配额剩 1、队头要 3 时，后面权重 1 的任务也只能等。任务 B 第一批只用了 9 就是这个原因。

**和 buffered channel 的区别：** channel 信号量里等待的发送者没有顺序保证（任务 A 的开始顺序是乱的），也不支持一次拿多个名额。

### 信号量 vs worker pool

| | 信号量 | worker pool |
|---|---|---|
| 限制的是 | 并发数（同时运行的任务数） | goroutine 数 |
| goroutine 总数 | 可以远大于 N | 固定 N 个 |
| 适用场景 | 限流（API QPS、数据库连接） | 固定线程池处理任务队列 |

信号量：100 个 goroutine，但最多同时 5 个在跑，其余 95 个阻塞在 `sem <- struct{}{}`。
worker pool：5 个 goroutine 从任务队列里取任务，循环处理。

---

## 结论

1. **buffered channel 是最简单的信号量**：`make(chan struct{}, N)`，写入获取、读出释放。
2. **weighted semaphore 支持不同权重**：一个任务可以占多个配额，适合资源大小不同的场景。
3. **信号量限制并发数，不限制 goroutine 数**：100 个 goroutine，5 个并发，其余 95 个阻塞等待。
4. **defer 释放要写在确认拿到信号量之后**：获取可能失败（带 ctx）时，写在前面会释放别人的名额。
5. **Acquire 可能阻塞，可以加超时**：`ctx, cancel := context.WithTimeout(...); sem.Acquire(ctx, n)`。
6. **semaphore.Weighted 是 FIFO**：不插队，大任务不会饿死，但队头的大任务会挡住后面的小任务。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| defer 释放写在获取之前，而获取可能因 ctx 失败 | 没拿到也执行释放，读走别人的名额：超过容量，或者永远阻塞 | 确认拿到之后再 defer 释放 |
| `Acquire` 的权重大于总容量 | 永远拿不到，一直阻塞到 ctx 结束；ctx 是 Background 就永远卡住 | 权重不能超过 `NewWeighted` 的容量 |
| 以为 Weighted 会让小任务先走 | FIFO，队头的大任务挡住后面所有任务 | 权重差异大时分成几个信号量，或者限制单个任务的权重 |
| 用信号量做 QPS 限流 | 信号量限的是"同时多少个"，不是"每秒多少个"，任务很快时 QPS 仍然可能超 | 每秒请求数用 `golang.org/x/time/rate` 的令牌桶 |
| 忘记释放信号量 | 信号量泄漏，可用配额越来越少，最终全部阻塞 | 用 defer 保证一定释放 |
| Acquire 后不检查 error | ctx 取消时继续执行，没拿到配额 | `if err := sem.Acquire(ctx, n); err != nil { return err }` |
| weighted semaphore 的 Acquire 和 Release 权重不一致 | 配额计数错乱 | 同一个任务的 Acquire 和 Release 必须用相同的权重 |
| 以为 buffered channel 满了会 panic | 不会，写入阻塞 | 正常用就行；要非阻塞用 select + default |

---

## 最佳实践

- 限制"同时多少个"（数据库连接、文件句柄、同时进行的请求）用信号量；限制"每秒多少个"（API QPS）用 `rate.Limiter`。
- 简单场景（固定容量）用 buffered channel，不需要引入 `golang.org/x/sync`。
- 需要权重或超时时用 `semaphore.Weighted`，配合 context。
- 获取信号量后立刻 defer 释放，保证即使 panic 也能释放。
- 需要非阻塞获取时，用 `semaphore.Weighted` 的 `TryAcquire(n)`，或者 buffered channel + select + default。

---

## 思考题和答案

1. **任务A里，defer 放在 `sem <- struct{}{}` 之前还是之后？放错了会怎样？**
   放在之后。任务 A 里获取只会成功或一直阻塞，放在前面其实也不出错；但获取一旦可能失败（比如 select 里同时等 ctx.Done），
   放在前面就会在没拿到名额时执行 `<-sem`，读走别人的名额，可能超过容量，也可能永远阻塞。养成"确认拿到再 defer"的习惯。

2. **信号量满了，`sem <- struct{}{}` 是阻塞还是报错？怎么加超时？**
   阻塞，不报错。加超时用 select：`select { case sem <- struct{}{}: case <-time.After(dur): return ErrTimeout }`。weighted semaphore 的 Acquire 接受 ctx，用 `context.WithTimeout`。

3. **任务B里，如果总权重刚好等于容量，所有任务能同时跑吗？**
   能，前提是它们拿配额的顺序允许。Weighted 是 FIFO 的：配额不够的任务排队，Release 时只从队头开始叫醒，队头放不下就停。
   所以权重和等于容量时，只要排队顺序刚好能一个个塞进去，就能同时跑；本节实测第一批只用了 9/10，就是队头的任务要 2、只剩 1。
   另外，单个任务的权重大于容量（比如 11）时，Acquire 会一直等到 ctx 结束；用 Background 就永远卡住。

---

## 对应 gmaker 的代码

- gmaker 没有显式用信号量：每个 market 一个 worker（`engine_dispatch.go:25` 起，每个 marketID 一个 dispatcher + 一个 worker），并发度天然等于 market 数
- 交易所 REST 客户端用的是令牌桶限流，不是信号量：`internal/exchange/polymarket/rest.go:78`、`internal/exchange/guessis/rest.go:104`
  的 `rate.NewLimiter(rate.Limit(rps), burst)`，限的是每秒请求数。对比本节：信号量限"同时多少个"，rate.Limiter 限"每秒多少个"
- 如果要做"最多同时订阅 N 个 market 的 WebSocket"，信号量就是合适的工具
