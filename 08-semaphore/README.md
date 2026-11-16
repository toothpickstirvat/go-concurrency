# 第 8 节：信号量

## 为什么学信号量

有时候不是所有 goroutine 都该无限并发。比如调外部 API 有 QPS 限制、打开文件有 fd 上限、数据库连接池有大小。信号量就是"同时最多
N 个"的控制工具。

errgroup 的 `SetLimit` 内部就是一个信号量。这一节我们手动实现，理解原理。

---

## 要学的内容

1. 用 buffered channel 当信号量：`sem := make(chan struct{}, N)`，`sem <- struct{}{}` 占位，`<-sem` 释放
2. `golang.org/x/sync/semaphore` 的 `Weighted`：支持不同权重的任务
3. 信号量和 worker pool 的区别：worker pool 限制 goroutine 数，信号量限制并发数（goroutine 可以多于 N）

---

## 任务

在 `08-semaphore/main.go` 里写两个实现。

### 任务 A：用 buffered channel 限流

模拟 100 个任务，每个 sleep 100ms，但同时最多跑 5 个。用 `make(chan struct{}, 5)` 当信号量。打印每个任务开始和结束的时间，确认任意时刻不超过
5 个在跑。

### 任务 B：用 weighted semaphore

用 `golang.org/x/sync/semaphore.NewWeighted(10)`，有 20 个任务，权重分别为 1、2、3，总权重可能超过 10。每个任务
`Acquire(ctx, weight)`，`defer Release(weight)`。观察高权重任务会不会因为配额不足而等待。

---

## 写之前先想清楚

1. 任务 A 里，如果用 `defer func() { <-sem }()`，defer 放在 `sem <- struct{}{}` 之前还是之后？放错了会怎样？
2. 信号量满了，`sem <- struct{}{}` 是阻塞还是报错？怎么加超时？
3. 任务 B 里，如果总权重刚好等于容量，所有任务能同时跑吗？

---

## 自查方法

```bash
go get golang.org/x/sync
go run -race ./08-semaphore
```

任务 A：打印的并发开始数不超过 5。总时间约 `100 / 5 * 100ms = 2 秒`。
任务 B：高权重任务在配额不足时等待，最终全部完成。

---

## 对应 gmaker 的代码

- gmaker 没有显式用信号量，因为每个 market 就一个 worker，并发度天然等于 market 数
- 如果要做"最多同时订阅 N 个 market 的 WebSocket"，信号量就是合适的工具
