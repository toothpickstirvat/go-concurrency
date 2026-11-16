# 第 1 节：互斥锁 sync.Mutex

## 为什么需要 Mutex

channel 的口号是"不要通过共享内存来通信，而是通过通信来共享内存"。但有些场景共享内存更直接，比如：

- 一个共享计数器，多个 goroutine 都要读写
- 一个 map，多个 goroutine 要查询和更新

Go 里 map 不是并发安全的，两个 goroutine 同时写同一个 map 会直接 panic。这种"保护一段临界区"的场景就用 Mutex。

---

## 要学的内容

1. `sync.Mutex` 的 `Lock()` / `Unlock()`，以及为什么一定要用 `defer Unlock()`
2. `sync.RWMutex` 的 `RLock()` / `RUnlock()`，读多写少时用它
3. 锁的粒度：锁太大 → 并发度低；锁太小 → 容易漏
4. 死锁的两种常见原因：重复加锁、锁和 channel 互相等待

---

## 任务

在 `01-mutex/main.go` 里写一个**并发安全的计数器**。

### 要求

1. 启动 100 个 goroutine，每个对计数器执行 1000 次 `+1`。
2. 计数器用 `int` 变量，用 `sync.Mutex` 保护。
3. main 等所有 goroutine 结束后，打印最终值。期望是 100000。
4. 不用 channel 传递这个计数器，必须用共享变量 + 锁。

### 加分项（做完基本要求后再做）

1. 把 `Mutex` 换成 `RWMutex`，读操作用 `RLock`，写操作用 `Lock`。再加一个只读 goroutine，每 10ms 读一次计数器并打印。观察用
   `Mutex` 和 `RWMutex` 时读 goroutine 的表现有没有区别。
2. 故意制造一个死锁（比如在已经 `Lock` 之后再调一次 `Lock`），看看 Go 运行时报什么。然后写一段代码，演示"锁和 channel
   互相等待"造成的死锁。

---

## 写之前先想清楚

1. `mu.Lock(); mu.Unlock()` 和 `mu.Lock(); defer mu.Unlock()` 有什么区别？尤其在函数中途 `return` 或 `panic` 的时候。
2. 如果把 `counter++` 拆成"读 counter、加 1、写 counter"三步，中间不持锁，会发生什么？用 `-race` 跑一下看。
3. 100 个 goroutine 同时争一把锁，性能怎么样？试着把锁的粒度变细（比如把计数器拆成 10 个分片，每个分片一把锁，最后求和），看看速度有没有变化。

---

## 自查方法

```bash
cd ~/go/src/go-concurrency
go run -race ./01-mutex
```

- 最终值必须是 100000。如果不是，说明有 race。
- 带 `-race` 不能出现 `WARNING: DATA RACE`。
- 加分项里的死锁 demo 会触发 `fatal error: all goroutines are asleep - deadlock!`，这是预期的。

---

## 对应 gmaker 的代码

- `internal/engine/engine.go` 里 `engine.mu sync.RWMutex` 保护 `workers` map
- `internal/engine/engine_dispatch.go` 里 `routingMu sync.RWMutex` 保护 `dispatchChans` 路由表

写完告诉我，我来检查并讲解下一节。
