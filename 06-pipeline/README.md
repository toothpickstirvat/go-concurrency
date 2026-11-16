# 第 6 节：Pipeline

## 为什么学 Pipeline

Pipeline 是把一个复杂处理拆成多个 stage，每个 stage 是一个 goroutine，用 channel 连起来。好处是每个 stage 可以独立调并发数、独立背压、独立
cancel。fan-out 项目里你已经写过两级的 pipeline（路由 → relay → worker），这一节正式写多级版本。

---

## 要学的内容

1. 多级 stage 的数据流：`stage1 → chan1 → stage2 → chan2 → stage3`
2. 每个 stage 的并发度可以不同
3. 背压自动传播：下游慢，上游的 channel 满了，上游自动阻塞
4. 取消传播：ctx 取消时，所有 stage 都能通过 `ctx.Done()` 退出
5. close 传播：上游 close 它的输出 channel，下游的 `range` 自然结束

---

## 任务

在 `06-pipeline/main.go` 里写一个**数据处理流水线**。

### 数据流

```
生成器 ──► 过滤偶数 ──► 平方 ──► 打印
```

### 要求

1. **stage 1 生成器**：生成 1 到 20 的整数，写进 channel，写完 close。
2. **stage 2 过滤器**：只保留偶数。从 stage 1 的 channel 读，写进 stage 2 的 channel。
3. **stage 3 平方器**：每个数平方。从 stage 2 读，写进 stage 3 的 channel。
4. **stage 4 消费者**：打印结果，统计总数。
5. 每个 stage 用单独的函数实现，函数签名形如 `func stage2(in <-chan int, out chan<- int)`。
6. main 里把所有 stage 用 `go` 启动，用 WaitGroup 等 stage 1-3 完成，主 goroutine 跑 stage 4。

### 加分项

1. 给每个 stage 加随机 sleep（10-50ms），观察不同 stage 的速度差异如何自动背压。
2. 把 stage 3 的并发数改成 3（3 个 goroutine 同时跑平方），用 WaitGroup 在 3 个都结束后再 close out。观察输出顺序。
3. 加一个 ctx，超时 100ms 就取消整个 pipeline，确认所有 stage 都能退出。

---

## 写之前先想清楚

1. 每个 stage 是它输出 channel 的唯一写入方吗？那谁来 close 输出 channel？
2. 如果 stage 3 有 3 个 goroutine 同时写一个 channel，怎么 close？用 WaitGroup 汇总。
3. 如果中途某个 stage 出错了（比如除零），怎么把错误传出来，并让整个 pipeline 停下来？（提示：加一个 `errCh chan error`，或者用下一节的
   errgroup）

---

## 自查方法

```bash
go run -race ./06-pipeline
```

应该打印 2、4、6、8、10、12、14、16、18、20 的平方：4、16、36...400。
顺序可能乱（如果加了 sleep），但总数必须是 10。

---

## 对应 gmaker 的代码

- gmaker 的整个引擎就是一个大 pipeline：WebSocket → fanOut → dispatcher → worker → 下游服务
- `internal/engine/engine.go` 启动顺序就是按 stage 依次启动的
