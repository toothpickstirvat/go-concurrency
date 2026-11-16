# 第 2 节：原子操作 sync/atomic

## 为什么需要原子操作

Mutex 能解决所有共享内存问题，但有些场景太重了：一个简单的计数器，每次 Lock/Unlock 都要切换 goroutine 状态，开销比实际操作大得多。
`sync/atomic` 提供 CPU 级别的原子指令，不需要锁，适合单个整数的读/写/加减/CAS（比较并交换）。

---

## 要学的内容

1. `atomic.Int64`（Go 1.19+）的 `Add`、`Load`、`Store`、`CompareAndSwap`
2. 旧式 API：`atomic.AddInt64`、`atomic.LoadInt64`（见到旧代码时能认出来）
3. 适用场景：单个数值的计数、标志位（开关）
4. 什么时候不能用 atomic：保护复合数据结构、多个变量之间的原子性

---

## 任务

在 `02-atomic/main.go` 里写两个程序，放在同一个文件里，用函数隔开。

### 任务 A：原子计数器

和第 1 节的任务一模一样：100 个 goroutine，每个 `+1` 1000 次，最终打印 100000。但这次**不用 Mutex**，改用 `atomic.Int64`。

### 任务 B：自旋锁

用 `atomic.Int32` 和 `CompareAndSwap` 手写一个自旋锁：

```go
type SpinLock struct {
    state atomic.Int32 // 0=未锁，1=已锁
}

func (s *SpinLock) Lock()   { /* 用 CAS 实现 */ }
func (s *SpinLock) Unlock() { /* 用 Store 实现 */ }
```

用这个 SpinLock 替换任务 A 里的 `atomic.Int64`，保证计数器结果仍然是 100000。

---

## 写之前先想清楚

1. `atomic.Int64` 的 `Add` 是原子的，那 `counter.Load()` 再手动加 1 再 `Store` 是原子的吗？
2. 自旋锁的 `Lock` 里如果 CAS 失败，goroutine 会怎么做？这和 Mutex 阻塞等待有什么区别？高竞争时哪个更好？
3. `atomic` 操作能保证可见性（一个 goroutine 写的值，另一个能立刻读到）吗？和 Mutex 一样吗？

---

## 自查方法

```bash
go run -race ./02-atomic
```

两个任务都要输出 100000，不能有 DATA RACE。

---

## 对应 gmaker 的代码

- `internal/engine/engine.go` 里有 `atomic.Uint64` 用于生成单调递增的 generation ID（`hotaddGen`），每次热插拔市场时递增，不需要持大锁。
