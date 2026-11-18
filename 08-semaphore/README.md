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

100 个任务，每个 100ms，最多同时 5 个：

```
[A] 任务 99 开始 (163µs)
[A] 任务 0 开始 (215µs)
[A] 任务 1 开始 (224µs)
[A] 任务 2 开始 (274µs)
[A] 任务 3 开始 (277µs)     ← 前 5 个立刻开始
[A] 任务 0 结束 (101ms)
[A] 任务 4 开始 (101ms)     ← 第 6 个等任务 0 释放信号量后启动
...
[A] 总耗时: 2.001s
```

每隔约 100ms 就有 5 个任务同时结束、下一批 5 个立刻开始。总耗时约 2 秒（100 ÷ 5 × 100ms）。

### 任务 B：weighted semaphore

20 个任务，权重轮流 1、2、3，总容量 10：

```
[B] 任务 10 (权重 2) 开始，剩余配额约 8 (100µs)
[B] 任务 11 (权重 3) 开始，剩余配额约 7 (100µs)
[B] 任务 12 (权重 1) 开始，剩余配额约 9 (100µs)
[B] 任务 13 (权重 2) 开始，剩余配额约 8 (100µs)
[B] 任务 17 (权重 3) 开始，剩余配额约 7 (100µs)  ← 配额刚好用完
[B] 任务 13 (权重 2) 结束 (202ms)
[B] 任务 3 (权重 1) 开始，剩余配额约 9 (202ms)   ← 释放 2，下一个权重 1 的任务启动
...
[B] 总耗时: 504ms
```

多个任务凑齐配额时同时运行，高权重任务在配额不足时等待。

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

**defer 的位置很重要：**

```go
// 正确
sem <- struct{}{}
defer func() { <-sem }()

// 错误：defer 在 sem <- 之前
defer func() { <-sem }()  // ← 还没拿到信号量就注册释放，会多释放一次
sem <- struct{}{}
```

### weighted semaphore

```go
sem := semaphore.NewWeighted(10)
sem.Acquire(ctx, 3)  // 获取 3 个配额，不足就阻塞
defer sem.Release(3) // 释放 3 个配额
```

内部实现（简化）：

```go
type Weighted struct {
    size    int64
    cur     int64
    mu      sync.Mutex
    waiters list.List  // 等待队列
}

func (s *Weighted) Acquire(ctx context.Context, n int64) error {
    s.mu.Lock()
    if s.cur + n <= s.size {  // 配额够
        s.cur += n
        s.mu.Unlock()
        return nil
    }
    // 配额不够，加入等待队列，等 Release 唤醒
    ...
}

func (s *Weighted) Release(n int64) {
    s.mu.Lock()
    s.cur -= n
    // 遍历等待队列，唤醒配额够的任务
    s.mu.Unlock()
}
```

- 用 Mutex 保护当前已用配额 `cur`
- Acquire 时检查 `cur + n <= size`，不够就加入等待队列
- Release 时遍历等待队列，唤醒配额够的任务

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
4. **defer 必须在获取信号量之后**：否则会多释放一次，导致超过容量。
5. **Acquire 可能阻塞，可以加超时**：`ctx, cancel := context.WithTimeout(...); sem.Acquire(ctx, n)`。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| defer 在获取信号量之前 | 还没拿到就注册释放，多释放一次，超过容量 | 先获取，再 defer 释放 |
| 忘记释放信号量 | 信号量泄漏，可用配额越来越少，最终全部阻塞 | 用 defer 保证一定释放 |
| Acquire 后不检查 error | ctx 取消时继续执行，没拿到配额 | `if err := sem.Acquire(ctx, n); err != nil { return err }` |
| weighted semaphore 的 Acquire 和 Release 权重不一致 | 配额计数错乱 | 同一个任务的 Acquire 和 Release 必须用相同的权重 |
| 以为 buffered channel 满了会 panic | 不会，写入阻塞 | 正常用就行；要非阻塞用 select + default |

---

## 最佳实践

- 限流场景（API QPS、数据库连接、文件句柄）优先用信号量。
- 简单场景（固定容量）用 buffered channel，不需要引入 `golang.org/x/sync`。
- 需要权重或超时时用 `semaphore.Weighted`，配合 context。
- 获取信号量后立刻 defer 释放，保证即使 panic 也能释放。
- 需要非阻塞获取时用 `TryAcquire`（Go 1.18+）或 buffered channel + select。

---

## 思考题和答案

1. **任务A里，defer 放在 `sem <- struct{}{}` 之前还是之后？放错了会怎样？**
   必须在之后。如果在之前，defer 在还没拿到信号量时就注册了释放动作，goroutine 退出时会多释放一次，信号量计数错乱，可能超过容量。

2. **信号量满了，`sem <- struct{}{}` 是阻塞还是报错？怎么加超时？**
   阻塞，不报错。加超时用 select：`select { case sem <- struct{}{}: case <-time.After(dur): return ErrTimeout }`。weighted semaphore 的 Acquire 接受 ctx，用 `context.WithTimeout`。

3. **任务B里，如果总权重刚好等于容量，所有任务能同时跑吗？**
   不一定。取决于启动顺序和权重组合。如果前几个任务的权重和刚好等于 10，它们能同时跑；如果第一个任务权重 11（大于容量），它会一直阻塞（deadlock）。weighted semaphore 不保证公平性，也不会自动拆分任务。

---

## 对应 gmaker 的代码

- gmaker 没有显式用信号量，因为每个 market 就一个 worker，并发度天然等于 market 数
- 如果要做"最多同时订阅 N 个 market 的 WebSocket"，信号量就是合适的工具
