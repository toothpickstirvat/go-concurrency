# 第 3 节：其余同步原语

## 为什么学这些

Mutex和atomic覆盖了80%的场景，但有几个专门用途的原语，在特定问题上更合适：`sync.Once`（只执行一次）、`sync.Cond`（等待条件满足）、
`sync.Map`（并发安全的map）。学会它们能少写很多重复代码，也能读懂别人的库。

---

## 要学的内容

1. `sync.Once`：初始化、懒加载，为什么比`if initialized { ... }` + Mutex更可靠
2. `sync.Cond`：等待某个条件成立才继续，为什么不能用channel代替
3. `sync.Map`：什么时候用它，什么时候用Mutex + 普通map（大多数时候后者更好）

---

## 任务

每个任务一个子目录（同一个目录只能有一个`main`函数）：

| 目录           | 内容                                                   |
|----------------|--------------------------------------------------------|
| `once/`        | 任务 A：sync.Once 单例                                 |
| `cond/`        | 任务 B：sync.Cond 生产者-消费者（两个 Cond）           |
| `cond-single/` | 加分实验：只用一个 Cond，演示 Signal 的丢失唤醒        |
| `syncmap/`     | 任务 C：Mutex+map、RWMutex+map、sync.Map 三种实现对比 |

### 任务 A：sync.Once 实现单例

写一个`Config`类型，有一个`Load()`方法：第一次调用时从"磁盘"（用`time.Sleep(100ms)`模拟）加载，之后的调用直接返回缓存。多个
goroutine同时第一次调用`Load()`，确认只加载一次。

提示：需要一个`sync.Once`和一个存放结果的字段。怎么统计"加载只发生了一次"？可以用一个`atomic.Int64`计数器。

### 任务 B：sync.Cond 实现生产者-消费者

一个容量为3的slice当队列。1个生产者放入1到20，1个消费者取20次并打印。满了生产者就等，空了消费者就等。用`sync.Cond`配合
`sync.Mutex`实现，不用channel。

要点：

- `cond.Wait()`会自动释放锁，被唤醒后重新获取锁，所以`Wait()`必须在`cond.L.Lock()`之后
- 用`for`循环检查条件，不要用`if`（虚假唤醒）
- 用两个Cond：`notFull`（生产者等）和`notEmpty`（消费者等）

加分：只用一个Cond，2个生产者 + 2个消费者，看看`Signal`会出什么问题。

### 任务 C：sync.Map vs Mutex + map

写三个并发安全的map：`Mutex + map[string]int`、`RWMutex + map[string]int`、`sync.Map`，实现同一个`Store`接口。两个场景：

- 写多：100个goroutine，每个写100个不同的新key
- 读多：预先写入1000个key，100个goroutine各读10000次

用`time.Since`对比三者的速度，打印一张3×2的表。

---

## 写之前先想清楚

1. `sync.Once`的`Do`方法如果传入的函数panic了，下次调用`Do`还会执行吗？
2. 为什么`sync.Cond.Wait()`要在`for`循环里调用，而不是`if`？
3. `sync.Map`在什么场景下比 Mutex + map快？读多写少，还是读写都多？key的数量有没有影响？

答案在文末。

---

## 自查方法

```bash
go run -race ./03-sync-primitives/once
go run -race ./03-sync-primitives/cond
go run -race ./03-sync-primitives/cond-single
go run -race ./03-sync-primitives/syncmap
go run ./03-sync-primitives/syncmap      # 测耗时不要带 -race
```

任务 A：加载计数器最终是 1。
任务 B：生产者和消费者最终都退出，队列里没有残留数据。
任务 C：打印三种实现在两个场景下的耗时。

---

## 实验结果：sync.Once

10个goroutine同时第一次调用`Load()`：

```
data: 0xc000180000, elapsed: 101.089634ms    ← 10 行都是同一个地址、都约 101ms
counter = 1
```

- 加载函数只执行了一次，10个goroutine拿到的是**同一个指针**。
- **没抢到执行权的goroutine不会直接返回**，而是阻塞约100ms，等加载完成后才返回。所以`Load()`返回后一定能拿到完整的数据。

### 原理：done 标志 + Mutex 的双重检查

`once.Do(f)`的实现（简化）：

```go
if o.done.Load() == 0 {      // 快路径：已经执行过，只花一次原子读
    o.m.Lock()               // 慢路径：没抢到的 goroutine 阻塞在这里
    defer o.m.Unlock()
    if o.done.Load() == 0 {  // 双重检查：拿到锁后再确认一次，防止重复执行
        defer o.done.Store(1) // f 返回之后才标记完成
        f()
    }
}
```

- 已经初始化过之后，每次调用只是一次原子读，几乎没有开销。
- 为什么不用CAS抢`done`？如果用`CompareAndSwap(0, 1)`，抢到的执行f，没抢到的**立刻返回**，这时f可能还没执行完，拿到的数据是空的。
  Once用锁让后来者等f执行完。
- `done`在f返回之后才置1，配合Mutex，保证`Do`返回时f的所有写入都可见（happens-before），读`c.data`不需要再加锁。

### 为什么比`if initialized` + Mutex更可靠

手写的话每次调用都要加锁（慢），或者写双重检查但`initialized`不用atomic（data race），或者先置标志再初始化（别人看到标志拿到半成品）。
Once把这些细节都处理好了。

---

## 实验结果：sync.Cond

### Wait 做了三件事

```
cond.Wait():
  ① 释放 cond.L（必须释放，否则别人拿不到锁去改变条件）
  ② 挂起，等 Signal / Broadcast
  ③ 被唤醒后重新加锁，然后才返回
```

所以`Wait`返回时一定持有锁，但**条件不一定成立**：从被唤醒到重新拿到锁之间，别的goroutine可能已经抢先改了状态。
必须写成：

```go
for !条件 {
    cond.Wait()
}
```

### 两个 Cond：正确

`cond/`用`notFull`和`notEmpty`两个Cond（绑定同一把锁）。生产者只在`notFull`上等，消费者只在`notEmpty`上等。`Put`后
`notEmpty.Signal()`，`Get`后`notFull.Signal()`，叫醒的一定是关心这个条件的一方。1生产者1消费者，20个数据全部按顺序取出，队列剩余0。

### 一个 Cond + Signal：丢失唤醒

`cond-single/`只用一个Cond，2个生产者 + 2个消费者，容量1，跑200次：

| 写法      | 死锁次数 |
|-----------|----------|
| Signal    | 49/200   |
| Broadcast | 0/200    |

死锁过程（P1、P2是生产者，C1、C2是消费者）：

1. 队列满，P1、P2都在Wait（等"不满"）
2. C1取走一个，Signal叫醒P1；C1再取，队列空，C1 Wait（等"不空"）
3. C2来取，队列空，C2 Wait
4. P1放入一个，Signal：本该叫醒消费者，却叫醒了P2
5. P2发现队列满，又回去Wait，这次唤醒被浪费了
6. P1下一次Put也发现队列满，Wait。4个goroutine全在等，没人再Signal：`fatal error: all goroutines are asleep - deadlock!`

**根本原因：两种不同的条件挂在同一个等待队列上，Signal只叫醒一个，可能叫醒的是同类。**

修法：

1. 改用`Broadcast`：全部叫醒，各自在for循环里重新检查。正确，但每次都叫醒所有人，大部分是白醒。
2. **用两个Cond分开等待**（推荐）：Signal就够，不会叫错人。

### 复现并发 bug 的经验

- 最初`wg.Add(2)`却启动了4个goroutine，要么提前退出，要么`Done`过多直接panic。**Add的数量必须和goroutine数量一致**，最好每启动一个就`Add(1)`。
- 丢失唤醒是概率性的：容量3时很难遇到，容量1才容易触发；而且要跑几百次。**一次没遇到不代表没有bug。**
- 测死锁不要带`-race`：它会让程序变慢、改变调度时机，问题反而更难出现。
- 一次只改一个变量（容量、Signal/Broadcast），否则不知道是哪个改动起的作用。

```bash
go build -o /tmp/cond-single ./03-sync-primitives/cond-single
for i in $(seq 200); do timeout 5 /tmp/cond-single >/dev/null || echo "第 $i 次卡住"; done
```

### 为什么不用 channel 代替

这个有界队列本身用带缓冲的channel就能实现，而且更简单，真实代码应该用channel。Cond的用武之地是**条件比"有没有数据"更复杂**，
或者要**一次叫醒所有等待者**（Broadcast），比如"等到某个状态变成X"。channel只能close一次来广播，不能反复广播。不过Cond没法和
`select`、超时、context一起用，所以Go里Cond用得很少。

---

## 实验结果：sync.Map vs Mutex + map

不带`-race`，4个逻辑CPU，连续跑5次：

| 实现        | 写多（1 万个新 key） | 读多（100 万次读） |
|-------------|----------------------|--------------------|
| Mutex+map   | 约 1.4–1.5ms         | 约 25–30ms         |
| RWMutex+map | 约 1.3–1.4ms         | 约 37–43ms         |
| sync.Map    | 约 1.9–5.1ms         | **约 5–12ms**      |

带`-race`时是 5–16ms / 117–330ms，不能作为性能参考。

### 读多：RWMutex 反而比 Mutex 慢

第1节里RWMutex比Mutex快5倍，这里却更慢，区别在**临界区的长短**：

- 第1节读操作里有`time.Sleep`，临界区很长。Mutex让读者排队，RWMutex让读者并行，差距巨大。
- 这里临界区只是一次map查找（几十纳秒），时间都花在加锁解锁上。
- **RLock不是"只读"操作**：每次`RLock`/`RUnlock`都要原子地增减读者计数，这是一次写入。4个核同时读，就是在抢同一个cache
  line（第2节的`LOCK XADD`竞争）。和Mutex抢锁状态一样贵，内部逻辑还更多。

### 读多：sync.Map 快 4–5 倍

sync.Map读一个已有的key时**不写任何共享内存**，全是原子读。MESI协议允许多个核同时持有一个cache line的只读副本，4个核各读
各的缓存，真正并行。

### 写多：sync.Map 不占优势

- Go 1.24 起sync.Map底层是并发哈希树（HashTrieMap），写入只锁局部节点，所以写新key不像旧版那么慢。旧版（1.24之前）写新key
  要走全局锁，还会定期复制整个只读map，网上"sync.Map写新key很慢"说的是旧版。
- 但它每个key是一个独立节点，值存成`any`（大于255的int装箱要堆分配），**分配次数比普通map多**，GC压力大。
- 单独跑sync.Map时写多是0.85–1.9ms，排在另外两种后面跑时变成1.9–5.1ms：前面留下的垃圾加上自己的分配，更容易触发GC。

### 关于测量

- 同一段代码单独跑和放在别的代码后面跑，结果可能差几倍（GC、缓存、CPU频率）。
- 毫秒级的单次计时只能看个大概。可靠的比较要用`testing.B`基准测试，`-benchmem`还能看每次操作的内存分配。
- key要在计时前生成好，否则测的是`fmt.Sprintf`。

---

## 结论

1. **`sync.Once`用于"只做一次"**：懒加载、单例、幂等关闭（`closeOnce`）。没抢到的调用者会**等f执行完**，不会拿到半成品。
2. **`sync.Cond`的Wait必须在持锁时调用、必须放在for循环里。** 不同条件用不同的Cond，用一个Cond时要Broadcast。
3. **大多数场景用channel就够，Cond很少用。**
4. **`sync.Map`只在两种场景下占优**：key写一次读很多次（缓存），或者多个goroutine读写的key互不相交。其他情况用Mutex/RWMutex + map。
5. **RWMutex不是读多就一定快**：临界区很短时，读者计数本身就是竞争点。

---

## 坑

| 坑                                     | 后果                                         | 正确做法                                               |
|----------------------------------------|----------------------------------------------|--------------------------------------------------------|
| 在`once.Do(f)`的f里再调用同一个`Do`    | Once内部Mutex不可重入，死锁                  | f里不要调用自己的Do                                    |
| 以为f panic了下次还会重试              | panic也算执行过，之后的Do都不再执行          | 需要重试或返回错误用`sync.OnceValues`或自己处理        |
| 用CAS手写"只执行一次"                  | 没抢到的调用者立刻返回，拿到未初始化的数据   | 用`sync.Once`                                          |
| 复制含`sync.Once`/`sync.Cond`的结构体  | 副本有自己的状态，Once会再执行一次           | 用指针；`go vet`能查出来                               |
| `cond.Wait()`前没加锁                  | Wait内部Unlock一个没锁的Mutex，fatal error   | 先`cond.L.Lock()`                                      |
| 用`if`检查条件后Wait                   | 被唤醒时条件可能已经被别人改了               | 用`for`循环                                            |
| 生产者和消费者共用一个Cond还用Signal   | 叫醒同类，丢失唤醒，概率性死锁               | 两个Cond分开等，或者Broadcast                          |
| `wg.Add`的数量和goroutine数不一致      | 提前退出，或计数变负panic                    | 每启动一个goroutine就`Add(1)`                          |
| 跑一两次没出问题就认为没bug            | 概率性bug上线后才暴露                        | 构造易触发的条件（容量1），循环跑几百次                |
| `sync.Map`的`Load`结果直接断言         | key不存在时v是nil，`v.(int)`panic            | 先判断ok，或用`n, ok := v.(int)`                       |
| 以为`sync.Map`处处比加锁map快          | 写多、需要多步原子操作、需要Len时更差        | 默认用Mutex/RWMutex + map，测出瓶颈再换                |
| 读的临界区很短也用RWMutex              | 读者计数成为竞争点，比Mutex还慢              | 临界区长或读者很多时才用RWMutex，用基准测试确认        |
| 带`-race`测性能                        | 耗时被放大几倍到几十倍                       | 正确性用`-race`，性能不带                              |

---

## 最佳实践

- **只做一次**：`sync.Once`；需要返回值时用Go 1.21的`sync.OnceValue(f)` / `sync.OnceValues(f)`，返回一个函数，直接调用拿结果。
  ```go
  var loadConfig = sync.OnceValues(func() (*ConfigData, error) { return readFromDisk() })
  cfg, err := loadConfig() // 多次调用只读一次磁盘，错误也会被缓存
  ```
- **等待条件**：优先用channel；真用Cond时，每种条件一个Cond，都绑定同一把锁，Wait放在for循环里。
- **并发map**：默认`Mutex + map`或`RWMutex + map`，类型安全、能做复合操作；只读或几乎只读的缓存才考虑`sync.Map`。
- **性能结论要靠测量**：先用`-race`保证正确，再不带`-race`多跑几次，最终用`testing.B`。

---

## 思考题和答案

1. **`once.Do`的f panic了，下次还会执行吗？**
   不会。Once认为f已经"返回"了（`done`在defer里置1），之后所有`Do`直接返回。如果需要失败后重试，Once不合适，要自己用Mutex + 标志位；
   `sync.OnceValue`/`OnceValues`遇到panic时，之后每次调用都会重新panic同一个值。
2. **为什么Wait要放在for里？**
   Wait返回只说明"有人叫醒了我"，不说明条件成立：从被唤醒到重新拿到锁之间，别的goroutine可能已经抢先消耗了这个条件；用Broadcast时
   所有人都被叫醒，但只有一部分能满足条件。所以醒来后必须重新检查，不满足就继续等。
3. **sync.Map什么时候比Mutex + map快？**
   读多写少，而且是读已经存在的key（本节实测快4–5倍）；或者多个goroutine各自操作互不相交的key。写新key多的场景没有优势，
   内存分配还更多。key数量多、核数多时，加锁map的锁竞争更严重，sync.Map的优势更明显。

---

## 对应gmaker的代码

- `sync.Once` 在 gmaker 里主要用来保证 close 只执行一次（幂等关闭）：`internal/ledger/ledger.go:105`、
  `internal/history/store.go:35` 的 `closeOnce`，以及 `internal/engine/worker_snapshot.go:137` 订阅取消函数里的 `once`
- gmaker 大量用 `sync.RWMutex + map` 而不是 `sync.Map`，因为 key 数量有限、读写比例不极端，普通 map 更直观
