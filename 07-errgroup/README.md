# 第 7 节：errgroup

## 为什么学 errgroup

fan-out 项目里我们用 WaitGroup 等所有 goroutine，但 WaitGroup 不收集错误。如果一个 goroutine 出错了，其他 goroutine
还在跑，main 也不知道。`golang.org/x/sync/errgroup` 解决这个问题：第一个错误会被记住，并自动取消整个 group。

---

## 要学的内容

1. `errgroup.Group` 的 `Go(fn)` 和 `Wait()`
2. `errgroup.WithContext(parent)` 返回的 group：任意一个 goroutine 返回非 nil error，ctx 自动取消
3. `SetLimit(n)` 限制并发数（内置信号量）
4. 和 WaitGroup 的关系：errgroup 内部就是用 WaitGroup + ctx 实现的

---

## 任务

在 `07-errgroup/main.go` 里写一个**并发抓取多个 URL**（用假函数模拟）。

### 要求

1. 一个URL列表（10个），用errgroup并发"抓取"。
2. "抓取"函数：sleep随机100-500ms，模拟失败：如果URL里包含"fail"就返回`errors.New("抓取失败: " + url)`。
3. 用`errgroup.WithContext(context.Background())`，让失败时其他goroutine通过ctx尽早退出。
4. `Wait()`返回第一个错误，main打印它。

### 加分项

1. 用`SetLimit(3)`限制最多3个并发，观察抓取总时间的变化。
2. 在每个goroutine里`select { case <-ctx.Done(): return ctx.Err() ... }`，确认失败后其他goroutine能提前退出而不是跑完。
3. 对比：把同一个任务用WaitGroup实现，体会errgroup省了什么。

---

## 写之前先想清楚

1. errgroup的 `Go`是异步的，`Wait`是同步等。如果`Go`之后立刻`Wait`，行为是什么？
2. 多个goroutine同时返回错误，`Wait()`返回哪一个？是第一个返回的，还是最后一个？
3. `SetLimit(3)`之外的`Go`调用会发生什么？是阻塞还是panic？

---

## 自查方法

```bash
go get golang.org/x/sync
go run -race ./07-errgroup
```

打印出第一个失败URL的错误信息。带ctx检查的版本，其他goroutine应该提前退出而不是跑完。

---

## 实验结果

### 无并发限制

10 个 goroutine 同时启动，第一个失败就取消所有：

```
✓ 抓取成功: https://example.com/page8 (耗时 138ms)
✓ 抓取成功: https://example.com/page7 (耗时 170ms)
✗ 已取消: https://example.com/page10 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page9 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page5 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page4 (ctx.Err: context canceled)
✗ 已取消: https://example.com/fail-page3 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page1 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page2 (ctx.Err: context canceled)

遇到错误: 抓取失败: https://example.com/fail-page6
总耗时: 171.043103ms
```

fail-page6 在 171ms 时失败，其他 8 个 goroutine 因为 ctx 取消立刻退出。

### SetLimit(3)

限制最多 3 个并发：

```
✓ 抓取成功: https://example.com/page2 (耗时 178ms)
✓ 抓取成功: https://example.com/page1 (耗时 307ms)
✓ 抓取成功: https://example.com/page5 (耗时 107ms)
✗ 已取消: https://example.com/page7 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page8 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page9 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page10 (ctx.Err: context canceled)
✗ 已取消: https://example.com/page4 (ctx.Err: context canceled)
✗ 已取消: https://example.com/fail-page6 (ctx.Err: context canceled)

遇到错误: 抓取失败: https://example.com/fail-page3
总耗时: 441.504683ms
```

SetLimit 是一个滑动窗口：前 3 个（page1、page2、fail-page3）先启动，之后任何一个结束，main 里阻塞在 `g.Go` 的下一个才能启动。
fail-page3 这次睡得久，到 441ms 才失败，ctx 被取消。之后 main 继续调用 `g.Go`，剩下的 URL 照样会启动 goroutine，
但一进 select 就发现 ctx 已取消，立刻返回，所以 page7–page10、fail-page6 都打印"已取消"。

---

## 核心机制

### errgroup 做了什么

```go
g, ctx := errgroup.WithContext(parent)
g.Go(func() error { return doWork(ctx) })
g.Go(func() error { return doWork(ctx) })
err := g.Wait()  // 返回第一个非 nil error
```

内部实现（摘自 `golang.org/x/sync@v0.25.0/errgroup/errgroup.go`，有删减）：

```go
type Group struct {
    cancel  func(error)    // WithContext 时由 context.WithCancelCause 创建
    wg      sync.WaitGroup
    sem     chan token     // SetLimit 设置的信号量
    errOnce sync.Once      // 只记录第一个错误
    err     error
}

func (g *Group) Go(f func() error) {
    if g.sem != nil {
        g.sem <- token{}       // 有并发限制时先占一个名额，满了就阻塞
    }
    g.wg.Add(1)
    go func() {
        defer g.done()         // wg.Done()，并从 sem 里释放名额
        if err := f(); err != nil {
            g.errOnce.Do(func() {
                g.err = err
                if g.cancel != nil {
                    g.cancel(g.err)   // 取消 ctx，取消原因就是这个错误
                }
            })
        }
    }()
}

func (g *Group) Wait() error {
    g.wg.Wait()
    if g.cancel != nil {
        g.cancel(g.err)
    }
    return g.err
}
```

1. **WaitGroup 等所有 goroutine**：和手写 WaitGroup 一样。
2. **用 sync.Once 记住第一个错误**：最先执行到 `errOnce.Do` 的那个错误被记下，后面的错误被丢弃。这是第 3 节的 Once 的真实用法。
3. **自动取消 ctx，并带上原因**：ctx 是 `context.WithCancelCause` 创建的，`context.Cause(ctx)` 能拿到导致取消的那个错误，
   `ctx.Err()` 仍然只是 `context.Canceled`。第 10 节的综合项目就是靠 Cause 打印"worker 3 失败"。
4. **Wait 返回前也会取消 ctx**：所以 `Wait` 返回后，`gctx` 一定已经结束，不能再拿它去做别的事。

### SetLimit：内置信号量

```go
g.SetLimit(3)
for i := 0; i < 10; i++ {
    g.Go(func() error { ... })  // 第 4 个开始会阻塞
}
```

内部用信号量：
- 内部就是第 8 节的 buffered channel 信号量：`sem := make(chan token, n)`
- `Go()` 先 `g.sem <- token{}`，满了就阻塞，**阻塞的是调用 `Go` 的 goroutine**（这里是 main），不是新启动的 goroutine
- goroutine 返回时 `<-g.sem` 释放名额，阻塞在 `Go()` 的调用方继续
- 还有一个 `TryGo(f) bool`：名额满了不阻塞，直接返回 false

### 和 WaitGroup 的对比

| | WaitGroup | errgroup |
|---|---|---|
| 等所有 goroutine | ✓ | ✓ |
| 收集错误 | 需要自己用 channel 或 Mutex | 自动记第一个 |
| 失败时取消其他 | 需要自己传 ctx 和手动 cancel | 自动 cancel |
| 限制并发数 | 需要自己用 channel 或信号量 | `SetLimit(n)` |
| 代码量 | 多 | 少 |

用 WaitGroup 实现同样的功能需要：

```go
var wg sync.WaitGroup
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
errCh := make(chan error, 1)  // 缓冲 1，防止阻塞
sem := make(chan struct{}, 3) // 限制并发

for _, url := range urls {
    wg.Add(1)
    sem <- struct{}{}  // 获取信号量
    go func(u string) {
        defer wg.Done()
        defer func() { <-sem }()  // 释放信号量
        if err := fetchURL(ctx, u); err != nil {
            select {
            case errCh <- err:  // 只发一次
                cancel()
            default:
            }
        }
    }(url)
}
wg.Wait()
close(errCh)
err := <-errCh
```

errgroup 把这些都封装好了。

---

## 结论

1. **errgroup 适合"多个任务，任意一个失败就取消所有"的场景**：批量抓取、并行处理、pipeline 的多路输入。
2. **`Wait()` 返回第一个错误**，多个错误同时返回时不保证顺序。
3. **所有 goroutine 里都要监听 `ctx.Done()`**，否则失败后其他 goroutine 不会退出。
4. **`SetLimit` 限制并发数**，避免一次性启动大量 goroutine。
5. **errgroup 是 `golang.org/x/sync` 下的官方扩展库**，不在标准库里，需要 `go get`。

---

## 坑

| 坑 | 后果 | 正确做法 |
|----|------|----------|
| goroutine 里不监听 ctx.Done() | 失败后其他 goroutine 继续跑到结束，浪费资源 | 每个阻塞操作用 select 监听 ctx.Done() |
| 以为 Wait() 返回所有错误 | 只拿到第一个，其他错误丢失 | 需要所有错误就自己用 channel 收集 |
| 以为 SetLimit 后 Go() 会 panic | 不会，Go() 阻塞等待，有 goroutine 完成后才启动新的 | 正常用就行 |
| 以为第一个错误是"最早启动的那个" | 是最早返回错误的那个，和启动顺序无关 | 不要依赖错误的顺序 |

---

## 最佳实践

- 把 errgroup 的 ctx 传给每个 goroutine，每个阻塞操作都监听 ctx.Done()。
- 需要限制并发数时用 `SetLimit`，不用自己写信号量。
- 需要收集所有错误时，errgroup 不够用：每个 goroutine 把错误写进自己下标的 `errs[i]`（各写各的，不需要锁），`Wait` 之后用 `errors.Join(errs...)` 合并。
- 和 pipeline 结合：每个 stage 返回 error，用 errgroup 启动所有 stage，任意一个出错就停掉整条 pipeline。

---

## 思考题和答案

1. **errgroup 的 `Go` 是异步的，`Wait` 是同步等。如果 `Go` 之后立刻 `Wait`，行为是什么？**
   Wait 会阻塞，直到所有通过 `Go` 启动的 goroutine 都返回。行为和 WaitGroup 一样。

2. **多个 goroutine 同时返回错误，`Wait()` 返回哪一个？**
   返回最先执行到 `errOnce.Do` 的那个错误，也就是最先返回错误的 goroutine，和启动顺序无关。几个 goroutine 几乎同时失败时，谁先谁后取决于调度，不能依赖。后面的错误被丢弃。

3. **`SetLimit(3)` 之外的 `Go` 调用会发生什么？**
   `Go()` 调用会阻塞，直到有正在运行的 goroutine 完成、释放信号量，才启动新的。不会 panic，也不会被拒绝。

---

## 对应gmaker的代码

- gmaker用WaitGroup + `goSvc` 管理 goroutine，没有用 errgroup（因为worker不返回错误，靠ctx取消）
- 如果要给gmaker加一个"任意worker失败则整个引擎停止"的语义，errgroup是最直接的工具
