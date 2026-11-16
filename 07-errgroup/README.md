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

1. 一个 URL 列表（10 个），用 errgroup 并发"抓取"。
2. "抓取"函数：sleep 随机 100-500ms，模拟失败：如果 URL 里包含 "fail" 就返回 `errors.New("抓取失败: " + url)`。
3. 用 `errgroup.WithContext(context.Background())`，让失败时其他 goroutine 通过 ctx 尽早退出。
4. `Wait()` 返回第一个错误，main 打印它。

### 加分项

1. 用 `SetLimit(3)` 限制最多 3 个并发，观察抓取总时间的变化。
2. 在每个 goroutine 里 `select { case <-ctx.Done(): return ctx.Err() ... }`，确认失败后其他 goroutine 能提前退出而不是跑完。
3. 对比：把同一个任务用 WaitGroup 实现，体会 errgroup 省了什么。

---

## 写之前先想清楚

1. errgroup 的 `Go` 是异步的，`Wait` 是同步等。如果 `Go` 之后立刻 `Wait`，行为是什么？
2. 多个 goroutine 同时返回错误，`Wait()` 返回哪一个？是第一个返回的，还是最后一个？
3. `SetLimit(3)` 之外的 `Go` 调用会发生什么？是阻塞还是 panic？

---

## 自查方法

```bash
go get golang.org/x/sync
go run -race ./07-errgroup
```

打印出第一个失败 URL 的错误信息。带 ctx 检查的版本，其他 goroutine 应该提前退出而不是跑完。

---

## 对应 gmaker 的代码

- gmaker 用 WaitGroup + `goSvc` 管理 goroutine，没有用 errgroup（因为 worker 不返回错误，靠 ctx 取消）
- 如果要给 gmaker 加一个"任意 worker 失败则整个引擎停止"的语义，errgroup 是最直接的工具
