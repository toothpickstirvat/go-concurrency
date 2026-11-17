// 第 1 节基本任务：用 sync.Mutex 保护的并发计数器。
// 100 个 goroutine 各加 1000 次，最终值必须是 100000。
//
// 运行：
//
//	go run -race ./01-mutex   // 检查正确性（不能有 DATA RACE）
//	go run ./01-mutex         // 测耗时（-race 会严重放大加锁开销，不能用来测性能）
package main

import (
	"fmt"
	"sync"
	"time"
)

func main() {
	start := time.Now()
	counter := 0
	// 闭包直接引用外层的 m，所有 goroutine 共用同一把锁。
	// 注意 Mutex 不能复制：按值传给函数会得到一把新锁，等于没加锁（go vet 会报 passes lock by value）。
	m := sync.Mutex{}
	wg := sync.WaitGroup{}
	for range 100 {
		wg.Add(1) // 在 go 之前 Add，避免 main 先执行到 Wait 时计数还是 0
		go func() {
			defer wg.Done()
			// 锁的粒度对比（不带 -race 实测）：
			//   - 锁在循环外：只加锁 100 次，约 0.2ms，但 100 个 goroutine 实际上是串行执行的；
			//   - 锁在循环内（下面注释掉的写法）：加锁 10 万次，竞争激烈，约 0.5–2ms。
			// 这里临界区只有 counter++，加锁开销占大头，所以锁在外面反而快。
			// 真实代码里循环中通常还有不需要锁的耗时工作，原则仍然是：锁只包住访问共享数据的那几行。
			m.Lock()
			for range 1000 {
				//m.Lock()
				counter++ // 读、加 1、写三步，不加锁会被其他 goroutine 打断，导致结果小于 100000
				//m.Unlock()
			}
			m.Unlock()
		}()
	}
	wg.Wait()
	// 这里读 counter 不需要加锁：wg.Wait() 返回时，所有 wg.Done() 之前的写入都对 main 可见
	// （Go 内存模型的 happens-before 保证），而且已经没有 goroutine 在写了。
	fmt.Println(counter, time.Since(start))
}
