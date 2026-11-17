# 第 2 节：原子操作 sync/atomic

## 为什么需要原子操作

Mutex能解决所有共享内存问题，但有些场景太重了：一个简单的计数器，每次Lock/Unlock都要切换goroutine状态，开销比实际操作大得多。
`sync/atomic`提供CPU级别的原子指令，不需要锁，适合单个整数的读/写/加减/CAS（比较并交换）。

---

## 要学的内容

1. `atomic.Int64`（Go 1.19+）的 `Add`、`Load`、`Store`、`CompareAndSwap`
2. 旧式API：`atomic.AddInt64`、`atomic.LoadInt64`（见到旧代码时能认出来）
3. 适用场景：单个数值的计数、标志位（开关）
4. 什么时候不能用 atomic：保护复合数据结构、多个变量之间的原子性

---

## 任务

任务A写在`02-atomic/main.go`，任务B放在子目录`02-atomic/spinlock/main.go`（同一个目录只能有一个`main`函数，做法同第
1节）。

### 任务 A：原子计数器

和第1节的任务一模一样：100个goroutine，每个 `+1` 1000次，最终打印100000。但这次**不用Mutex**，改用`atomic.Int64`。

### 任务 B：自旋锁

用`atomic.Int32`和`CompareAndSwap`手写一个自旋锁：

```go
type SpinLock struct {
    state atomic.Int32 // 0=未锁，1=已锁
}

func (s *SpinLock) Lock()   { /* 用CAS实现 */ }
func (s *SpinLock) Unlock() { /* 用Store实现 */ }
```

用这个SpinLock替换任务A里的`atomic.Int64`，保证计数器结果仍然是100000。

---

## 写之前先想清楚

1. `atomic.Int64`的`Add`是原子的，那`counter.Load()`再手动加1再`Store`是原子的吗？
2. 自旋锁的`Lock`里如果CAS失败，goroutine会怎么做？这和Mutex阻塞等待有什么区别？高竞争时哪个更好？
3. `atomic`操作能保证可见性（一个goroutine写的值，另一个能立刻读到）吗？和Mutex一样吗？

---

## 自查方法

```bash
go run -race ./02-atomic
go run -race ./02-atomic/spinlock
```

两个任务都要输出 100000，不能有 DATA RACE。测耗时时去掉 `-race`。

---

## 实验结果：atomic 和 Mutex 对比

不带 `-race`，4 个逻辑 CPU，100 个 goroutine 各加 1000 次：

| 写法              | 同步操作次数        | 耗时         |
|-------------------|---------------------|--------------|
| Mutex，锁在循环内 | 10 万次 Lock/Unlock | 约 0.5–2ms   |
| **atomic.Add**    | 10 万次原子加       | 约 0.4–0.8ms |
| Mutex，锁在循环外 | 100 次 Lock/Unlock  | 约 0.2ms     |

- **公平的比较是 atomic 和"锁在循环内"**：两者都对每一次 +1 做同步。atomic 更快，因为它是一条 CPU 指令，goroutine 不会被挂起和唤醒。
- **atomic 比"锁在循环外"慢**，因为后者总共只同步了 100 次。atomic 也不是免费的，见下面的原理。
- 结论：**atomic 让每次同步变便宜了，但最有效的优化还是减少同步次数。** 比如每个 goroutine 先在局部变量里累加，最后只调用一次
  `counter.Add(本地结果)`（批量处理）。
- 这个量级的耗时单次波动很大（0.4ms 和 0.8ms 差一倍），只能看个大概，可靠的比较要用 `testing.B` 基准测试。

---

## 原理：100 个 goroutine 是怎么竞争 counter 的

要分两层看：Go 调度器决定哪些 goroutine 真正同时在跑，CPU 硬件决定它们怎么抢同一块内存。

### 第一层：Go 调度器，同一时刻最多 GOMAXPROCS 个在跑

Go 默认 `GOMAXPROCS` 等于逻辑 CPU 数（本机是 4）。调度器开 4 个运行位（P），每个绑定一个操作系统线程（M）：

- **同一时刻最多只有 4 个 goroutine 真正在执行**，其余的在运行队列里排队。
- 一个 goroutine 做 1000 次 `Add` 只需要几微秒，往往还没被切换就已经做完退出了。
- main 在循环里逐个创建 goroutine，先创建的可能在后面的还没创建时就已经结束了。

所以"100 个 goroutine 竞争"，实际上**任意时刻最多 4 个在抢**。

### 第二层：CPU 硬件，抢的是 cache line 的独占权

`counter` 占 8 个字节，它所在的 64 字节叫一个 **cache line**。每个 CPU 核有自己的 L1 缓存，CPU 读写内存都以 cache line 为单位。

多核之间用**缓存一致性协议**（MESI）保证数据一致：

- 多个核可以同时持有这个 cache line 的**只读副本**。
- 要**修改**它，一个核必须先拿到**独占权**，其他核的副本全部作废。

`counter.Add(1)` 在 amd64 上编译成一条带 `LOCK` 前缀的指令 `LOCK XADD`：

1. 当前核向其他核要这个 cache line 的独占权。
2. 拿到后，在自己的缓存里一次性完成"读、加 1、写回"，期间其他核都不能插手。
3. 下一个核想 `Add` 时，再把独占权要过去。

```
核 0:  [拿独占权][读+1写]                    [等独占权…][读+1写]
核 1:            [等独占权…][读+1写]
核 2:                       [等独占权…][读+1写]
核 3:                                  [等独占权…][读+1写]
          cache line 在 4 个核之间来回传递（几十纳秒一次）
```

**"竞争"的本质就是 cache line 在核之间来回搬运。** 硬件把这些修改排成一个队，每次修改都完整、不可分割，所以不会丢失。

### 不加锁的 counter++ 为什么会错

普通的 `counter++` 会被编译成三条独立的指令：读、加 1、写回。两条指令之间，另一个核可能已经写过了：

```
核 0: 读到 5 ──────── 加 1 → 6 ── 写回 6
核 1:      读到 5 ── 加 1 → 6 ──────────── 写回 6   ← 两次加法只生效了一次
```

`LOCK` 前缀的作用就是把这三步合成**一个不可分割的操作**。

### atomic 和 Mutex 抢的方式不一样

|              | atomic.Add                                     | Mutex                                                        |
|--------------|------------------------------------------------|--------------------------------------------------------------|
| 抢的是什么   | cache line 的独占权                            | 锁的所有权                                                   |
| 谁来排队     | CPU 硬件                                       | Go 运行时                                                    |
| 抢不到怎么办 | CPU 在硬件层面等几十纳秒，goroutine 不会被挂起 | 先自旋一会儿，还抢不到就挂起 goroutine，以后再唤醒（微秒级） |
| 会失败吗     | 不会，一定成功                                 | 不会失败，但可能要等很久                                     |

`CompareAndSwap` 又不一样：值已经被别人改了，它就返回 false，**需要自己写循环重试**。任务 B 的自旋锁就建立在这个特性上。

### 自己验证

```bash
GOMAXPROCS=1 go run ./02-atomic
GOMAXPROCS=2 go run ./02-atomic
GOMAXPROCS=4 go run ./02-atomic
```

只给一个核时，硬件层面没有竞争：cache line 一直留在同一个核里，goroutine 在这个核上轮流运行。对比三种情况的耗时。

---

## 实验结果：自旋锁 vs Mutex

### 第一版的 bug：丢掉了 CAS 的返回值

```go
func (s *SpinLock) Lock() {
    s.state.CompareAndSwap(0, 1) // 返回值被丢掉
}
```

锁被占用时 CAS 返回 false，但 goroutine 不管，照样往下执行，锁形同虚设。`-race` 报了 2 处 DATA RACE。不带 `-race` 连续跑 8 次，结果是：

```
99743  100000  99510  100000  100000  99769  99258  100000
```

8 次里 4 次是错的，"看起来对"只是运气好。

教训：

1. **data race 不会崩溃、不会报错，只会让结果时对时错**，必须用 `-race` 查。
2. **race detector 报了警告就是真有问题**。它不会误报，只会漏报（没执行到的代码路径检测不到）。

### 第二版：Lock 和 TryLock 分开

自旋循环一开始写在调用方（`for !sl.Lock() {}`）。这样 `Lock()` 名不副实，而且每个调用方都要自己写循环，漏写一处就又回到第一版的 bug。改成和 `sync.Mutex` 一样的接口：

- `TryLock() bool`：只试一次 CAS
- `Lock()`：在内部循环调用 `TryLock`，拿到锁才返回

### 不让 goroutine 被换下 CPU 时，看不出问题

临界区里只有 `counter++` 时，SpinLock 约 0.5–3.7ms（波动大），`GOMAXPROCS=1` 约 0.55ms，**并不慢**。原因是调度器约 10ms 才强制切换一次，而整个程序 0.5ms 就跑完了，持锁的 goroutine 从来没在临界区里被换下去，每次拿锁时锁都是空闲的。

### 持锁时被换下 CPU：问题出现

在 `counter++` 和 `Unlock()` 之间加 `runtime.Gosched()`，模拟持锁的 goroutine 被调度器换下：

| 写法 | `GOMAXPROCS` | 耗时 | CPU 时间 |
|---|---|---|---|
| SpinLock，自旋时不让出 CPU | 1 | **超过 2 分钟（超时）** | 约 2 分钟 |
| SpinLock，自旋时不让出 CPU | 4 | **超过 2 分钟（超时）** | 约 7 分钟 |
| SpinLock，自旋时 `Gosched` 让出 | 1 | 约 264ms | 约 261ms |
| SpinLock，自旋时 `Gosched` 让出 | 4 | 约 505ms | 约 850ms |
| **sync.Mutex** | 1 | **约 22ms** | 约 23ms |
| **sync.Mutex** | 4 | **约 21–25ms** | 约 23–29ms |

**自旋时不让出 CPU**：CPU 全部满负荷，程序却几乎没有进展。持锁者 A 让出 CPU 后，等锁的 B、C、D…轮流上来空转，每个都要等调度器约 10ms 后强制切换。A 要排在它们后面才能拿回 CPU 去解锁，每次 `counter++` 可能要等几百毫秒。

**自旋时 `Gosched` 让出**：等锁的 goroutine CAS 失败一次就让出，每个只占几百纳秒，A 很快能拿回 CPU。但等锁的 goroutine 还在运行队列里，会被反复调度、反复失败，这些调度都是白做的。

4 核反而比单核慢，原因有两个：

1. 多个核同时 CAS `state`，cache line 在核之间来回搬运（单核时一直留在同一个核里）。
2. `Gosched` 会把 goroutine 放进全局运行队列，每个 P 主要从本地队列取，全局队列隔一段时间才检查。A 让出后要多等一会儿才能被重新调度。这和调度器实现有关，不同 Go 版本可能不同。

**sync.Mutex**：抢不到锁的 goroutine 被挂起，放进**这把锁自己的等待队列**，不在运行队列里，调度器根本不会选到它们。A 让出 CPU 后几乎马上又被调度回来，解锁时只唤醒一个等待者。4 核时 CPU 时间约等于实际耗时，说明同一时间只有一个核在干活，多出来的核闲着，没有被空转浪费。临界区本来就只能串行执行，多给核也没法并行。

### 自旋锁的根本问题

**持锁的 goroutine 需要 CPU 才能释放锁，但 CPU 被等锁的 goroutine 占着。** 真实程序里不需要主动调用 `Gosched`，持锁的 goroutine 随时可能被换下去：时间片用完、系统调用、GC。

`sync.Mutex` 把两种做法结合起来：先短暂自旋（仅在多核、有其他 P 在运行、锁可能很快释放时，这时自旋比挂起便宜），还抢不到就挂起，由解锁方负责唤醒。

---

## 结论

1. **atomic 适合单个值的操作**：计数器、序号、标志位、时间戳。多个变量要一起保持一致时，用 Mutex。
2. **atomic 让每次同步变便宜，但减少同步次数更有效**（本地累加、批量提交）。
3. **atomic 也有开销**：高竞争时 cache line 在核之间来回搬运。
4. **CAS 可能失败，要检查返回值并循环重试**。
5. **atomic 操作之间存在 happens-before 关系**，所以用 atomic 实现的锁能保护普通变量。但这只是为了理解原理。
6. **真实代码里用 `sync.Mutex`，不要手写自旋锁。**

---

## 坑

| 坑                                                 | 后果                                 | 正确做法                                         |
|----------------------------------------------------|--------------------------------------|--------------------------------------------------|
| 复制 `atomic.Int64` 等类型（按值传参、复制结构体） | 操作的是副本，原值没变               | 用指针传递；它们内含 `noCopy`，`go vet` 能查出来 |
| `Load` 后手动加 1 再 `Store`                       | 两步之间可能被别人改过，丢失更新     | 用 `Add`，或者用 `CompareAndSwap` 循环           |
| 以为 atomic 没有开销                               | 高竞争时 cache line 来回搬运，照样慢 | 减少共享写入次数（本地累加、批量提交）           |
| 丢掉 `CompareAndSwap` 的返回值                     | 不知道有没有成功，锁形同虚设         | 检查返回值，失败就重试或走别的分支               |
| 输出"看起来对"就忽略 `-race` 警告                  | 结果时对时错，上线后才暴露           | race detector 报警就是真问题，必须修             |
| 让调用方自己写自旋循环                             | 漏写一处就是 bug                     | 循环封装在 `Lock()` 里，另提供 `TryLock()`       |
| 锁的内部状态字段导出（大写）                       | 外部可以绕过 Lock/Unlock 直接改      | 用小写字段                                       |
| 手写自旋锁                                         | 持锁者被换下 CPU 时，程序几乎停滞    | 用 `sync.Mutex`                                  |
| 用短时间的单次运行下结论                           | 没触发调度切换，问题没暴露           | 设计能触发问题的场景（如持锁时 `Gosched`）       |

---

## 工具：timeout 和 time

程序可能卡住时，用 `timeout` 限制运行时间，用 `time` 看耗时和 CPU 时间：

```bash
go build -o /tmp/spinlock ./02-atomic/spinlock
time (GOMAXPROCS=1 timeout 120 /tmp/spinlock; echo "exit=$?")
```

- `timeout 120 命令`：超过 120 秒就强制结束。时长可以写 `60`、`60s`、`5m`、`0.5`。
- 退出码：0 正常结束，**124 超时**，其他是程序自己出错（死锁是 2）。
- 先 `go build` 再对可执行文件用 `timeout`：`go run` 会启动子进程，个别情况下超时后子进程没被一起结束。
- `time` 输出的 `real` 是实际耗时，`user` 是 CPU 时间。**`user` 远大于 `real` 说明多个核在忙；CPU 一直满负荷但程序没进展，说明在空转。**

---

## 思考题和答案

1. **`counter.Load()` 再加 1 再 `Store` 是原子的吗？**
   不是。`Load` 和 `Store` 各自是原子的，但两步之间别的 goroutine 可能已经改过了，会丢失更新。要么用 `Add`，要么用 CAS 循环：读出旧值，计算新值，`CompareAndSwap(旧值, 新值)`，失败就重来。
2. **CAS 失败时 goroutine 在做什么？和 Mutex 有什么区别？高竞争时哪个好？**
   自旋锁的 goroutine 在空转或反复被调度，一直占着 CPU 或运行队列；Mutex 会把它挂起到等待队列，不占 CPU。高竞争、或持锁者可能被换下 CPU 时，Mutex 好得多（本节实测：22ms 对 264ms 或超时）。
3. **atomic 能保证可见性吗？**
   能。Go 内存模型规定 atomic 操作之间存在 happens-before 关系：一个 goroutine 的 `Store` 之前的写入，对之后 `Load` 或 CAS 读到这个值的 goroutine 可见。这和 Mutex 的 Unlock → Lock 是一样的保证，所以自旋锁能正确保护普通的 `int`。

---

## 对应 gmaker 的代码

- `internal/ordermgr/depth.go:175` `nonce atomic.Int64`：生成订单 CID 用的单调递增序号，无锁安全
- `internal/engine/engine.go:112` `nextRequoteSlot atomic.Uint64`：轮转分配的序号
- `internal/risk/manager.go:49` `killed atomic.Bool`：kill switch 标志位，一个 goroutine 设置、多个 goroutine 读取
- `internal/engine/worker.go:175` `closesAtUnix atomic.Int64`：用 Unix 时间戳存时间，Engine 后台更新、worker 读取
- 进阶（本节不要求）：`atomic.Pointer[T]`（`risk/manager.go:50` 余额缓存）、`atomic.Value`（`engine.go:128` 存
  context），用来原子地替换整个对象
