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

## 这节做完之后

你已经覆盖了 Go 并发编程的核心。可以回头读 gmaker 的 `internal/engine/` 代码，会发现大部分模式你都亲手实现过了。剩下的是工程细节：锁的顺序、goroutine
泄漏排查、race detector 的解读、性能调优——这些在真实代码里练。

可选的进阶方向：

- Go memory model（happens-before 关系）
- `runtime.GOMAXPROCS` 和调度器行为
- pprof 分析 goroutine 数和锁竞争
- channel 的底层实现（hchan 结构）
