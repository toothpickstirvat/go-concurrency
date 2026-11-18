# 第 11 节：进阶方向

前 10 节解决的是"怎么写对"。这一节整理四个"为什么能对"的底层话题，每个话题都对应前面已经观察到的现象：

| 话题 | 解释前面哪些现象 |
|------|------------------|
| Go 内存模型 | 为什么 `wg.Wait()` 之后读变量不用加锁、自旋锁能保护普通 int、buffered channel 能当信号量 |
| GOMAXPROCS 和调度器 | 为什么 100 个 goroutine 同时最多 4 个在跑、自旋锁 4 核比单核慢、`Gosched` 后要多等一会儿 |
| pprof | 程序卡住、变慢时怎么找到是哪个 goroutine、哪把锁 |
| channel 底层 | 为什么 select 是随机的、关闭的 channel 会立刻返回零值、无缓冲 channel 是"交接" |

本节没有必须完成的任务，每个话题末尾有可选的小练习。文中的源码位置和实测数据都来自本机 Go 1.27，4 个逻辑 CPU。

---

## 一、Go 内存模型：happens-before

### 为什么需要它

编译器和 CPU 都会重排指令，每个核还有自己的缓存。一个 goroutine 写了变量，另一个 goroutine **不一定**能看到，
看到的顺序也不一定和写的顺序一样。内存模型规定了：**在什么条件下，一个 goroutine 的写入保证对另一个 goroutine 可见。**

这个条件就是 happens-before：如果写 W happens-before 读 R，R 一定能读到 W（或者更新的值）。
没有 happens-before 关系、又至少有一方在写，就是 data race，`-race` 报的就是这个。

### 规则（Go 1.19 修订版，节选）

| 同步操作 | 保证 | 课程里的例子 |
|----------|------|--------------|
| `go f()` | `go` 语句之前的写入，对 f 可见 | 所有给 goroutine 传参的地方 |
| 无缓冲 channel 的接收 | happens-before 对应的发送**完成** | 第 6 节每一级 stage 之间的交接 |
| channel 的发送 | happens-before 对应的接收完成 | pipeline 里上游写的数据，下游读到时一定完整 |
| `close(ch)` | happens-before 因关闭而返回的接收 | 第 6 节 close 链，下游 range 结束时上游的写入都可见 |
| 容量 C 的 channel，第 k 次接收 | happens-before 第 k+C 次发送完成 | 第 8 节 buffered channel 信号量能限流的理论依据 |
| `mu.Unlock()` 第 n 次 | happens-before 第 n+1 次 `Lock()` 返回 | 第 1 节计数器 |
| `once.Do(f)` 里 f 返回 | happens-before 任何 `Do` 返回 | 第 3 节，Load 之后读 `c.data` 不用加锁 |
| atomic 操作 | 所有 atomic 操作像按某个全局顺序依次执行（顺序一致） | 第 2 节自旋锁，Store(0) 前的 `counter++` 对下一个 CAS 成功者可见 |
| `wg.Done()` | happens-before 它解除阻塞的 `wg.Wait()` 返回 | 第 3 节 cond，`wg.Wait()` 后读 `len(q.items)` |

### 反例：没有同步的"标志位"

```go
var data int
var ready bool          // 普通 bool，不是 atomic

go func() {
    data = 42
    ready = true
}()
for !ready {}           // 可能永远循环：编译器可以把 ready 读到寄存器里只读一次
fmt.Println(data)       // 就算跳出循环，也不保证读到 42
```

这段代码在 x86 上"大多数时候能跑"，但它有两个问题：编译器可以把循环里的 `ready` 提到循环外，变成死循环；
写入 `data` 和 `ready` 之间没有 happens-before，读到 `ready == true` 不代表能读到 `data == 42`。
把 `ready` 换成 `atomic.Bool`，两个问题都解决了：atomic 的 Store/Load 建立了 happens-before。

### 和课程的联系

- **第 1 节**：`wg.Wait()` 之后读 `counter` 不用加锁，靠的是 `Done` → `Wait` 的 happens-before。
- **第 2 节**：自旋锁用 `atomic.Int32` 的 CAS 和 Store，保护的是普通 `int`。能保护住，是因为 atomic 操作之间有 happens-before；
  第一版丢掉 CAS 返回值时，没拿到锁的 goroutine 和持锁者之间没有任何同步，`-race` 就报了。
- **第 3 节**：Once 的 `done` 要在 f 返回之后才置 1，正是为了让"f 里的写入" happens-before "其他 goroutine 的 Do 返回"。
- **`-race` 的原理**：它在运行时记录每次同步操作建立的 happens-before 关系（向量时钟），发现两个访问之间没有这种关系、
  又至少有一个是写，就报 DATA RACE。所以它只会漏报（没执行到的路径），不会误报。

### 练习

1. 把上面的反例写出来，`go run -race` 看报告；再改成 `atomic.Bool` 确认不报。
2. 读官方文档 <https://go.dev/ref/mem>，找到"容量 C 的 channel"那条规则，想一想：为什么有了它，
   `make(chan struct{}, 3)` 就能保证同时最多 3 个 goroutine 进入临界区？

---

## 二、GOMAXPROCS 和调度器

### G、M、P

| | 是什么 | 本机数量 |
|---|---|---|
| G（goroutine） | 要执行的任务，有自己的栈（初始几 KB） | 想开多少开多少 |
| M（machine） | 操作系统线程，真正在 CPU 上跑 | 按需创建，阻塞在系统调用时会多开 |
| P（processor） | 运行 Go 代码的"许可证"，持有一个本地运行队列 | `GOMAXPROCS` 个，默认等于可用 CPU 数，本机是 4 |

M 必须拿到一个 P 才能执行 Go 代码，所以**同一时刻最多 GOMAXPROCS 个 goroutine 在真正执行**。
第 2 节"100 个 goroutine 竞争 counter，实际任意时刻最多 4 个在抢"就是这个原因。

### 调度过程（Go 1.27 源码 `src/runtime/`）

- **本地运行队列**：每个 P 有一个 256 个位置的环形队列（`runtime2.go` 里的 `runq [256]guintptr`），
  外加一个 `runnext` 槽位，刚被唤醒的 goroutine 放这里，下一个就运行它。
- **全局运行队列**：本地队列满了、或者调用 `runtime.Gosched()` 时，goroutine 放进全局队列。
  P 每调度 61 次才检查一次全局队列（`proc.go` 里的 `schedtick%61 == 0`），防止全局队列里的 goroutine 饿死。
- **work stealing**：P 的本地队列空了，先看全局队列，再随机找别的 P，偷走它一半的 goroutine。
- **抢占**：一个 goroutine 连续运行超过 10ms（`proc.go` 里的 `forcePreemptNS = 10ms`），后台的 sysmon 线程会给它发信号，
  强制它让出 CPU（Go 1.14 起的异步抢占，死循环也能被打断）。
- **系统调用**：goroutine 进入阻塞的系统调用时，M 带着它一起阻塞，P 被交给另一个 M 继续跑别的 goroutine。

### 用调度器解释第 2 节的实验

| 现象 | 原因 |
|------|------|
| 自旋锁不让出 CPU，持锁者被换下后程序几乎停滞 | 等锁的 goroutine 占着 P 空转，每个要等约 10ms 才被抢占，持锁者排在它们后面 |
| 自旋时 `Gosched`，4 核（505ms）反而比单核（264ms）慢 | `Gosched` 把 goroutine 放进全局队列，P 每 61 次才看一次全局队列；多核时 CAS 还要在核之间搬 cache line |
| `sync.Mutex` 只要 22ms | 抢不到锁的 goroutine 挂在锁自己的等待队列上，不在任何运行队列里，调度器不会选到它们 |

### GOMAXPROCS 的默认值和设置

- 默认值是可用 CPU 数。本机 `runtime.NumCPU()` 和 `runtime.GOMAXPROCS(0)` 都是 4（传 0 只查询，不修改）。
- **容器里**：Go 1.25 起，Linux 上默认值会读 cgroup 的 CPU 限制（源码 `runtime/cgroup_linux.go`），并在运行中定期更新。
  以前容器限制 2 核、宿主机 64 核时，GOMAXPROCS 会是 64，大量线程抢 2 核的配额，导致被限流。
- 设置方式：环境变量 `GOMAXPROCS=1 go run .`，或代码里 `runtime.GOMAXPROCS(n)`。代码里设置过之后，
  可以用 `runtime.SetDefaultGOMAXPROCS()`（`runtime/debug.go:117`）恢复成默认行为。

本机对第 2 节的 atomic 计数器各跑一次：

```
go run ./02-atomic                 → counter: 100000, elapsed: 901µs
GOMAXPROCS=1 go run ./02-atomic    → counter: 100000, elapsed: 305µs
```

单核更快：只有一个 P，`counter` 的 cache line 一直留在同一个核里，没有核之间的搬运。
**并发不等于并行，多核也不一定更快**：临界区只能串行执行时，多出来的核只会增加竞争。
（单次运行波动很大，只看量级；要可靠比较请用 `testing.B`。）

### 练习

1. 用 `GODEBUG=schedtrace=1000 go run ./06-pipeline`，每秒打印一次调度器状态，看 `runqueue`（全局队列长度）和每个 P 的本地队列长度。
2. 把第 2 节自旋锁实验分别用 `GOMAXPROCS=1/2/4` 跑，对照上面的调度过程解释耗时差异。

---

## 三、pprof：分析 goroutine 和锁竞争

### 能看什么

| profile | 回答的问题 | 默认开启？ |
|---------|------------|------------|
| `goroutine` | 现在有哪些 goroutine，各自卡在哪一行 | 是 |
| `mutex` | 哪把锁竞争最严重，等锁的总时间花在哪 | 否，要 `runtime.SetMutexProfileFraction(n)` |
| `block` | goroutine 阻塞在 channel、select、锁、Cond 上的时间 | 否，要 `runtime.SetBlockProfileRate(n)` |
| `cpu` | CPU 时间花在哪些函数上 | 采样期间开启 |
| `heap` / `allocs` | 内存在哪分配（第 9 节的分配次数问题） | 是 |

### 两种接入方式

**服务程序**：引入 `net/http/pprof`，开一个 HTTP 端口：

```go
import _ "net/http/pprof"

go http.ListenAndServe("localhost:6060", nil)   // 只监听本机，不要暴露到公网
```

```bash
curl 'localhost:6060/debug/pprof/goroutine?debug=2'   # 所有 goroutine 的完整调用栈，排查卡住、泄漏
go tool pprof localhost:6060/debug/pprof/mutex        # 交互式分析锁竞争
go tool pprof -http=:8080 localhost:6060/debug/pprof/profile?seconds=10   # CPU，浏览器看火焰图
```

**短程序**：用 `runtime/pprof` 写文件。

本机实测：8 个 goroutine 各抢 200 次同一把锁，每次持锁 100µs：

```go
runtime.SetMutexProfileFraction(1)   // 1 表示记录每一次竞争，生产环境用更大的值抽样
// ……跑完并发代码……
f, _ := os.Create("mutex.pprof")
pprof.Lookup("mutex").WriteTo(f, 0)
f.Close()
```

```bash
go build -o pp . && ./pp
go tool pprof -top pp mutex.pprof
```

```
Type: delay
Showing nodes accounting for 14.03s, 100% of 14.03s total
      flat  flat%   sum%        cum   cum%
    14.03s   100%   100%     14.03s   100%  sync.(*Mutex).Unlock (inline)
         0     0%   100%     14.03s   100%  main.main.func1
```

- `Type: delay`：统计的是**等锁的总时间**，所有 goroutine 加起来。程序只跑了约 0.16 秒（1600 次 × 100µs），
  但 7 个 goroutine 一直在排队，总共等了 14 秒。
- 时间记在 `Unlock` 上：mutex profile 在解锁时记录"这次解锁让多少人等了多久"，看 `cum` 列找调用方（这里是 `main.main.func1`）。

### 和课程的联系

- **第 1 节"程序卡住时怎么排查"**：`kill -QUIT` 打印的就是 goroutine profile 的完整版；服务程序用 `/debug/pprof/goroutine?debug=2`。
- **第 3 节 cond-single 的丢失唤醒**：卡住时 goroutine profile 会显示 4 个 goroutine 都停在 `sync.(*Cond).Wait`。
- **第 10 节退出前检查 `runtime.NumGoroutine()`**：数字对不上时，goroutine profile 能告诉你多出来的那个卡在哪一行。
- **第 9 节的分配次数**：`go test -bench . -benchmem` 或 `allocs` profile 能看到每轮 3 次分配具体是哪几次。

### 进一步：`go tool trace`

pprof 是统计信息，trace 是时间线：每个 P 上什么时候在跑哪个 goroutine、谁唤醒了谁、GC 什么时候停顿。

```go
f, _ := os.Create("trace.out")
trace.Start(f)        // import "runtime/trace"
defer trace.Stop()
```

```bash
go tool trace trace.out   # 打开浏览器
```

### 练习

1. 给第 3 节 syncmap 的读多场景开 mutex profile，对比 Mutex+map 和 RWMutex+map 的等锁时间。
2. 给第 6 节 pipeline 加 trace，在时间线上看三个 stage 怎么轮流阻塞（背压）。

---

## 四、channel 的底层实现：hchan

### 结构（`src/runtime/chan.go`，节选）

```go
type hchan struct {
    qcount   uint           // 缓冲区里现在有几个元素
    dataqsiz uint           // 缓冲区容量，make(chan T, N) 的 N；无缓冲是 0
    buf      unsafe.Pointer // 环形缓冲区
    elemsize uint16
    closed   uint32
    sendx    uint           // 下一次写缓冲区的位置
    recvx    uint           // 下一次读缓冲区的位置
    recvq    waitq          // 阻塞在接收上的 goroutine 队列
    sendq    waitq          // 阻塞在发送上的 goroutine 队列
    lock     mutex          // 保护上面所有字段
}
```

channel 就是**一把锁 + 一个环形缓冲区 + 两个等待队列**。等待队列里放的是 `sudog`，它记录了"哪个 goroutine、要发送或接收的数据放在哪"。

### 发送 `ch <- v`（`chansend`）

加锁后按顺序检查：

1. **有接收者在 recvq 里等**：直接把 v 拷贝到那个接收者的栈上，唤醒它，**不经过缓冲区**（`send` 函数）。
2. **缓冲区没满**：v 拷贝进 `buf[sendx]`，`sendx` 加 1（到头了绕回 0），`qcount` 加 1。
3. **都不行**：自己包成 sudog 挂到 sendq，解锁，挂起（`gopark`），等接收者来唤醒。

接收 `<-ch`（`chanrecv`）对称：有发送者在等就直接拿；缓冲区有数据就从 `buf[recvx]` 取；都没有就挂到 recvq。
有一个细节：缓冲区满、又有发送者在等时，接收者从缓冲区头部取走一个，再把等待的发送者的数据放进缓冲区尾部，保证顺序不乱。

### 关闭 `close(ch)`（`closechan`）

1. nil channel 或已关闭的 channel：panic（`close of nil channel` / `close of closed channel`）。
2. `closed` 置 1。
3. 唤醒 recvq 里**所有**接收者，它们拿到零值和 `ok=false`；唤醒 sendq 里**所有**发送者，它们醒来后 panic（`send on closed channel`）。

### 用它解释课程里的现象

| 现象 | 底层原因 |
|------|----------|
| 无缓冲 channel 是"交接"，发送方要等接收方 | `dataqsiz = 0`，只能走第 1 步直接拷贝，或者挂起等对方 |
| 第 6 节"缓冲只是推迟阻塞" | 缓冲区满了就走第 3 步，和无缓冲一样挂起 |
| 第 5 节：closed channel 的缓冲区里还有数据时，先读完再返回 `ok=false` | `chanrecv` 先看 `qcount`，缓冲区空了才看 `closed` |
| 第 5 节：nil channel 在 select 里永远不会被选中 | 对 nil channel 的阻塞操作直接挂起，永远没人唤醒；select 会跳过 nil channel 的 case |
| 第 6 节：多个写入方各自 close 会 panic | 第二次 close 走到第 1 步 |
| 第 8 节任务 A 的开始顺序是乱的 | sendq 本身先进先出，但 100 个 goroutine 谁先跑到发送语句、先排进队列，取决于调度 |
| `close` 能当广播用（第 4 节 ctx.Done） | close 唤醒等待队列里的所有人，普通发送只唤醒一个 |

### select（`src/runtime/select.go`）

1. **随机打乱 case 的轮询顺序**（`pollorder`，用 `cheaprandn` 洗牌）。这就是第 5 节"多个 case 同时就绪时随机选"的来源：
   不按书写顺序，是为了防止排在前面的 case 永远优先、后面的饿死。
2. **按 channel 的地址排序后依次加锁**（`lockorder`，堆排序）。所有 select 都按同一个顺序加锁，两个 select 同时操作同一组 channel 时不会互相死锁。
   这和第 1 节"多把锁要按固定顺序加"是同一个原则。
3. 按 pollorder 找第一个就绪的 case，有就执行；没有且有 default 就走 default；否则把自己挂到**所有** channel 的等待队列上，
   任何一个就绪都会唤醒它，唤醒后再从其他 channel 的队列上撤下来。

### 练习

1. 读 `chan.go` 里的 `chansend`，找到上面三步各自对应的代码。
2. 想一想：为什么 hchan 用一把锁保护所有字段就够了，而不像 sync.Map 那样做无锁读？（提示：channel 的每次操作几乎都要改字段。）

---

## 推荐阅读

- Go 内存模型：<https://go.dev/ref/mem>
- 调度器设计文档：<https://golang.org/s/go11sched>
- pprof 使用：<https://go.dev/doc/diagnostics>
- 本机源码：`$(go env GOROOT)/src/runtime/` 下的 `chan.go`、`select.go`、`proc.go`、`runtime2.go`
