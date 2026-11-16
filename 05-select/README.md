# 第 5 节：Select 进阶

## 为什么单独学 Select

fan-out 项目里你已经用过 `select { case ch <- ev: default: }` 做非阻塞写入。`select`
还有几个重要用法：超时控制、优先级选择、多路复用。这些是写真实并发程序时每天都在用的工具。

---

## 要学的内容

1. 超时：`case <-time.After(dur)` 给一个操作设上限
2. 优先级：先做一个非阻塞 `select`，再做一个阻塞 `select`，实现"优先处理某个 channel"
3. `default` 的副作用：让 select 变成非阻塞，但也会让空 select 变成忙等待（CPU 100%）
4. `select {}` 永久阻塞，什么时候用得上
5. 多个 case 同时就绪时的随机选择：不要依赖 case 的顺序

---

## 任务

在 `05-select/main.go` 里写三个小程序。

### 任务 A：超时控制

一个 worker 从 channel 读数据，但如果 500ms 内没数据就打印"超时"并退出。生产者每 1 秒发一个数据。

### 任务 B：优先级 channel

有两个数据源 channel `high` 和 `low`。一个消费者循环读取，规则是：只要 `high` 有数据就先读 `high`，`high` 空了才读 `low`。

提示：用两层 select。外层非阻塞试 `high`，内层阻塞等 `high` 或 `low`。

### 任务 C：多路复用

两个生产者往同一个 channel 写数据（fan-in 的标准实现）。一个消费者读。写完后关闭 channel。注意：有两个写入方，谁来 close？用
WaitGroup。

---

## 写之前先想清楚

1. `case <-time.After(dur)` 每次进入 select 都会创建一个新的 timer。如果 select 在循环里，会不会有性能问题？怎么改？
2. 任务 B 的"优先级"是绝对的还是公平的？如果 `high` 一直有数据，`low` 永远读不到，怎么办？
3. `select { default: }` 放在循环里，如果所有 case 都不就绪会怎样？

---

## 自查方法

```bash
go run -race ./05-select
```

任务 A：打印一两次数据后开始打印"超时"。
任务 B：`high` 的数据总是先被消费，`low` 在 `high` 空了后才被消费。
任务 C：两个生产者的数据交错出现，最后程序正常退出。

---

## 对应 gmaker 的代码

- `internal/engine/engine_dispatch.go:97-210` fanOut 主循环就是一个大 select，同时处理 ctx.Done 和事件
- `internal/engine/engine_series.go` 里大量 `select` 同时等多个 channel：ctx.Done、workerDone、settlement 事件
- gmaker 的关键事件投递用"先非阻塞试一次，失败再阻塞 select"实现快路径
