// 第 2 节任务 A：用 atomic.Int64 实现的并发计数器，不用 Mutex。
// 100 个 goroutine 各加 1000 次，最终值必须是 100000。
//
// 运行：
//
//	go run -race ./02-atomic          // 检查正确性
//	go run ./02-atomic                // 测耗时（不要带 -race）
//	GOMAXPROCS=1 go run ./02-atomic   // 只给一个核，对比硬件竞争的影响
//
// 实测（不带 -race，4 个逻辑 CPU）：约 0.4–0.8ms。
// 比 Mutex 锁在循环内（约 0.5–2ms）快，比 Mutex 锁在循环外（约 0.2ms，只同步 100 次）慢。
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	start := time.Now()
	// atomic.Int64 不能复制（内含 noCopy，go vet 会检查），这里闭包直接引用同一个变量。
	counter := atomic.Int64{}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1) // 在 go 之前 Add
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				// Add 在 amd64 上是一条 LOCK XADD 指令："读、加 1、写回"不可分割。
				// 多个核同时 Add 时，抢的是 counter 所在 cache line 的独占权，由 CPU 硬件排队，
				// goroutine 不会被挂起。
				// 注意：counter.Store(counter.Load() + 1) 不是原子的，两步之间可能被别人改过。
				counter.Add(1)
			}
			// 进一步优化（批量处理）：先在局部变量里累加 1000 次，最后只 Add 一次，
			// 共享写入从 10 万次降到 100 次。
		}()
	}
	wg.Wait()
	// 读 atomic 变量也要用 Load，不能直接访问内部字段。
	fmt.Printf("counter: %d, elapsed: %s\n", counter.Load(), time.Since(start))
}
