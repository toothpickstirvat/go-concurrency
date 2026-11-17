# 第 1 节：互斥锁sync.Mutex

## 为什么需要Mutex

channel的口号是"不要通过共享内存来通信，而是通过通信来共享内存"。但有些场景共享内存更直接，比如：

- 一个共享计数器，多个goroutine都要读写
- 一个map，多个goroutine要查询和更新

Go里map不是并发安全的，两个goroutine同时写同一个map会直接panic。这种"保护一段临界区"的场景就用Mutex。

---

## 目录结构

```
01-mutex/
├── README.md
├── main.go            # 基本任务：并发安全的计数器 + 锁粒度对比
├── rwmutex/main.go    # 加分项 1：Mutex 和 RWMutex 在读多写少场景下的对比
└── deadlock/main.go   # 加分项 2：三种死锁演示 + 死锁检测的局限
```

同一个目录下只能有一个`package main`的`main`函数，所以每个独立程序放一个子目录。

```bash
cd ~/go/src/go-concurrency
go run -race ./01-mutex             # 查正确性
go run ./01-mutex                   # 测耗时
go run ./01-mutex/rwmutex
timeout 5 go run ./01-mutex/deadlock
```

---

## 任务

### 基本任务（`main.go`）

1. 启动100个goroutine，每个对计数器执行1000次`+1`。
2. 计数器用`int`变量，用`sync.Mutex`保护。
3. main等所有goroutine结束后，打印最终值。期望是100000。
4. 不用channel传递这个计数器，必须用共享变量 + 锁。
5. 对比锁在循环外和锁在循环内的耗时。

### 加分项 1：RWMutex（`rwmutex/main.go`）

1个写goroutine写10次；10个读goroutine各读10次，**每次持锁10ms**（模拟慢读）。分别用`Mutex`和`RWMutex`跑，对比总耗时。

> 最初的设计是"一个读goroutine每10ms读一次"，但只有一个读者时没有读读并发，两种锁看不出区别。RWMutex的优势要在
> **多个读者同时读**时才体现。

### 加分项 2：死锁（`deadlock/main.go`）

1. 重复加锁
2. 锁和channel互相等待
3. 读锁升级成写锁
4. 加一个后台心跳goroutine，观察死锁还能不能被检测到

---

## 实验结果

### 锁的粒度（不带`-race`）

| 写法       | 加锁次数 | 耗时       | 并发度            |
|------------|----------|------------|-------------------|
| 锁在循环外 | 100      | 约 0.2ms   | 完全串行          |
| 锁在循环内 | 100000   | 约 0.5–2ms | goroutine交替执行 |

带`-race`时两者分别是约2ms和约30ms，差距被放大到15倍左右，因为race detector在每次加锁解锁时都要做额外检查。

锁在循环内更慢，不只是因为加锁次数多，还有**竞争**的代价：抢不到锁的goroutine先自旋，再挂起、被唤醒；锁和`counter`所在的cache
line在多个CPU核之间来回搬运。

### Mutex vs RWMutex（不带`-race`）

| 写法                                 | 耗时     | `-race`       |
|--------------------------------------|----------|---------------|
| 全部用Mutex                          | 约 1s    | 无警告        |
| RWMutex：读用`RLock`，写用`Lock`     | 约 200ms | 无警告        |
| RWMutex：读写**都用`RLock`**（错误） | 约 107ms | **DATA RACE** |

- Mutex：读操作也互斥，10 × 10 × 10ms = 1s，全部串行。
- RWMutex：读操作可以同时进行，本来只要约100ms。但写者调用`Lock`后，**新来的`RLock`也会被挡住**
  ，直到写完成（防止写者饥饿）。10次写各造成约10ms停顿，所以总共约200ms。
- 错误写法看起来最快，但写的同时有人在读，是data race。最终值碰巧正确只是因为只有一个写者。

### 死锁

| 演示                | 卡住的位置                        | 报错里的goroutine状态                 |
|---------------------|-----------------------------------|---------------------------------------|
| 重复加锁            | 第二次`Lock`                      | `[sync.Mutex.Lock]`                   |
| 锁和channel互相等待 | main在`<-ch`，子goroutine在`Lock` | `[chan receive]`、`[sync.Mutex.Lock]` |
| 读锁升级成写锁      | `rwm.Lock()`                      | `[sync.RWMutex.Lock]`                 |
| 加了心跳goroutine   | 同上                              | **不报错，程序一直卡住**              |

---

## 结论

### 1. Mutex和RWMutex的方法

| 类型           | 方法                                                     |
|----------------|----------------------------------------------------------|
| `sync.Mutex`   | `Lock`、`Unlock`、`TryLock`（没有`RLock`，写了编译报错） |
| `sync.RWMutex` | 以上全部，加上`RLock`、`RUnlock`、`TryRLock`             |

### 2. RWMutex的使用规则

| 操作     | 用什么锁          | 能同时持有的goroutine数   |
|----------|-------------------|---------------------------|
| 修改数据 | `Lock`/`Unlock`   | 1个，而且这时没有任何读者 |
| 只读数据 | `RLock`/`RUnlock` | 多个读者可以同时持有      |

- **修改共享数据必须用`Lock`。** `RLock`只是给只读操作用的优化，用错了就是data race。
- 读操作用`Lock`也可以：结果正确，只是失去并发读的好处，效果和Mutex一样。
- RWMutex**优先照顾写者**：有写者在等时，新的`RLock`也会被挡住。

### 3. 什么时候可以不加锁读

`wg.Wait()`返回后，所有`wg.Done()`之前的写入都对当前goroutine可见（Go内存模型的happens-before保证），而且已经没有goroutine在写了，这时读共享变量不需要加锁。

### 4. Go的死锁检测有局限

运行时只在**所有goroutine都在等待**时才报`fatal error: all goroutines are asleep - deadlock!`
。只要还有一个goroutine活着（在sleep、跑ticker、处理请求），死锁就不会被检测到，程序只是一直卡住。

真实服务总有后台goroutine，所以死锁通常表现为"程序不动了"，而不是崩溃。带`-race`运行时也可能检测不到。

---

## 坑

| 坑                                               | 后果                                             | 正确做法                                           |
|--------------------------------------------------|--------------------------------------------------|----------------------------------------------------|
| 复制Mutex（按值传参、按值复制结构体）            | 拿到一把新锁，等于没加锁                         | 用指针传递；`go vet`能查出来                       |
| `wg.Add`写在goroutine里面                        | main可能先执行到`Wait`，计数还是0，直接返回      | 在`go`之前`Add`                                    |
| 同一个goroutine重复`Lock`                        | 死锁（Go的Mutex不可重入）                        | 理清加锁路径，不要在持锁时调用会再加同一把锁的函数 |
| 持有`RLock`时调用`Lock`（锁升级）                | 死锁                                             | 先`RUnlock`再`Lock`，**拿到写锁后重新检查条件**    |
| 持锁时等channel，而对方要先拿这把锁才会写channel | 死锁                                             | 不要在持锁时做阻塞操作（channel、网络、sleep）     |
| `Lock`配`RUnlock`，或`RLock`配`Unlock`           | `fatal error: sync: RUnlock of unlocked RWMutex` | 加锁解锁严格配对                                   |
| 写操作用`RLock`                                  | data race                                        | 修改数据一定用`Lock`                               |
| 在循环里写`defer m.Unlock()`                     | defer要等整个函数返回才执行，第二轮循环就死锁    | 手动`Unlock`，或把循环体抽成一个函数               |
| 用`-race`的结果测性能                            | race detector放大加锁开销，结论失真              | `-race`查正确性，测性能时去掉                      |

---

## 最佳实践

1. **用`defer Unlock()`**：函数中途`return`或panic时也能保证解锁。紧跟在`Lock()`后面写，不容易漏。
2. **临界区越短越好**：锁只包住访问共享数据的那几行，耗时操作放到锁外面。
3. **持锁时不要做阻塞操作**：channel收发、网络请求、sleep、调用不熟悉的外部函数，都可能导致死锁或长时间卡住其他goroutine。
4. **临界区极短、加锁极频繁时**，考虑减少加锁次数（批量处理），或者换atomic（第 2 节）。
5. **读多写少用RWMutex**，读写差不多或临界区很短时，Mutex往往更快（RWMutex本身的开销更大）。
6. **锁和它保护的数据放在一起**：比如放在同一个结构体里，`mu`写在被保护字段的上面，加注释说明它保护哪些字段。
7. **每次都跑`go vet`和`go run -race`**。

---

## 程序卡住时怎么排查

- **发送SIGQUIT**：终端里按`Ctrl+\`，或者`kill -QUIT <pid>`。Go程序会打印所有goroutine的调用栈后退出。找状态是
  `[sync.Mutex.Lock]`、`[chan receive]`而且等待时间很长的goroutine。
- **pprof**：服务开了`net/http/pprof`的话，访问`/debug/pprof/goroutine?debug=2`，能看到同样的信息，而且不会让程序退出。

---

## 思考题和答案

1. **`mu.Lock(); mu.Unlock()`和`mu.Lock(); defer mu.Unlock()`有什么区别？**
   前者在中途`return`或panic时执行不到`Unlock`，锁永远不会释放，其他goroutine全部卡死。后者一定会解锁。
2. **`counter++`不加锁会怎样？**
   它是读、加1、写三步。两个goroutine同时读到同一个值，各自加1再写回，就丢了一次加法，最终值小于100000。`-race`会报DATA RACE。
3. **100个goroutine争一把锁，性能怎么样？**
   竞争激烈时，抢锁本身的开销会超过实际工作。可以减少加锁次数，或者把数据分片、每片一把锁（降低竞争），或者换atomic。

---

## 对应gmaker的代码

- `internal/engine/engine.go`里`engine.mu sync.RWMutex`保护`workers` map
- `internal/engine/engine_dispatch.go`里`routingMu sync.RWMutex`保护`dispatchChans`路由表。`fanOut`在读锁下查表，
  **先拷贝一份快照，再在锁外做阻塞发送**，正是"持锁时不要做阻塞操作"的实践。
