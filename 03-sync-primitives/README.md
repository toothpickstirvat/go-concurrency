# 第 3 节：其余同步原语

## 为什么学这些

Mutex 和 atomic 覆盖了 80% 的场景，但有几个专门用途的原语，在特定问题上更合适：`sync.Once`（只执行一次）、`sync.Cond`（等待条件满足）、
`sync.Map`（并发安全的 map）。学会它们能少写很多重复代码，也能读懂别人的库。

---

## 要学的内容

1. `sync.Once`：初始化、懒加载，为什么比 `if initialized { ... }` + Mutex 更可靠
2. `sync.Cond`：等待某个条件成立才继续，为什么不能用 channel 代替
3. `sync.Map`：什么时候用它，什么时候用 Mutex + 普通 map（大多数时候后者更好）

---

## 任务

在 `03-sync-primitives/main.go` 里写三个小程序。

### 任务 A：sync.Once 实现单例

写一个 `Config` 类型，有一个 `Load()` 方法：第一次调用时从"磁盘"（用 `time.Sleep(100ms)` 模拟）加载，之后的调用直接返回缓存。多个
goroutine 同时第一次调用 `Load()`，确认只加载一次。

提示：需要一个 `sync.Once` 和一个存放结果的字段。怎么统计"加载只发生了一次"？可以用一个 `atomic.Int64` 计数器。

### 任务 B：sync.Cond 实现生产者-消费者

一个容量为 3 的 slice 当队列。生产者往里放数据，满了就等；消费者取数据，空了就等。用 `sync.Cond` 配合 `sync.Mutex` 实现，不用
channel。

要点：

- `cond.Wait()` 会自动释放锁，被唤醒后重新获取锁，所以 `Wait()` 必须在 `cond.L.Lock()` 之后
- 用 `for` 循环检查条件，不要用 `if`（虚假唤醒）

### 任务 C：sync.Map vs Mutex + map

写两个并发安全的 map，一个用 `sync.Map`，一个用 `Mutex + map[string]int`。100 个 goroutine，每个写 100 个不同的 key，然后读
100 次。用 `time.Since` 对比两者的速度。

---

## 写之前先想清楚

1. `sync.Once` 的 `Do` 方法如果传入的函数 panic 了，下次调用 `Do` 还会执行吗？（答案在文档里）
2. 为什么 `sync.Cond.Wait()` 要在 `for` 循环里调用，而不是 `if`？
3. `sync.Map` 在什么场景下比 Mutex + map 快？读多写少，还是读写都多？key 的数量有没有影响？

---

## 自查方法

```bash
go run -race ./03-sync-primitives
```

任务 A：加载计数器最终是 1。
任务 B：生产者和消费者最终都退出，队列里没有残留数据。
任务 C：打印两种实现的耗时。

---

## 对应 gmaker 的代码

- `sync.Once` 在 gmaker 里主要用来保证 close 只执行一次（幂等关闭）：`internal/ledger/ledger.go:105`、
  `internal/history/store.go:35` 的 `closeOnce`，以及 `internal/engine/worker_snapshot.go:137` 订阅取消函数里的 `once`
- gmaker 大量用 `sync.RWMutex + map` 而不是 `sync.Map`，因为 key 数量有限、读写比例不极端，普通 map 更直观
