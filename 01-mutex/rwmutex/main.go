// 加分项 1：Mutex 和 RWMutex 在读多写少场景下的对比。
// 1 个写 goroutine 写 10 次；10 个读 goroutine 各读 10 次，每次持锁 10ms（模拟慢读）。
//
// 实测（不带 -race）：
//   - Mutex：约 1s。读操作互斥，10 × 10 × 10ms 全部串行。
//   - RWMutex：约 200ms。读操作可以同时持有读锁，本来只要约 100ms，
//     但写者每次 Lock 都会挡住新的 RLock（防止写者饥饿），10 次写造成约 100ms 停顿。
//   - 错误写法（写操作也用 RLock）：约 107ms，看起来最快，但 -race 会报 DATA RACE。
//
// 切换方式：把 m 的类型和读操作的加锁方式一起换，写操作始终用 Lock/Unlock。
//
// 注意：持锁期间 sleep 只是为了让持锁时间明显到能测出来，真实代码里持锁时 sleep 或做网络请求是错误写法。
package main

import (
	"fmt"
	"sync"
	"time"
)

func main() {
	start := time.Now()
	counter := 0
	m := new(sync.RWMutex)
	//m := new(sync.Mutex) // Mutex 没有 RLock/RUnlock，切换时读操作要改回 Lock/Unlock
	wg := new(sync.WaitGroup)

	// 写 goroutine：修改共享数据，必须用 Lock/Unlock（独占）。
	// 用 RLock 的话，写的同时读者也可能在读，就是 data race。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 10 {
			m.Lock()
			counter++
			m.Unlock()
			time.Sleep(10 * time.Millisecond) // 在锁外 sleep，不占锁
		}
	}()

	// 读 goroutine：只读共享数据，用 RLock/RUnlock，多个读者可以同时持有读锁。
	// 读操作也可以用 Lock（结果正确，但失去并发读的好处，效果和 Mutex 一样）。
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range 10 {
				//m.Lock()
				m.RLock()
				_ = counter                       // race detector 能检测到这种读取
				time.Sleep(10 * time.Millisecond) // 持锁期间 sleep，模拟慢读
				m.RUnlock()                       // RLock 必须配 RUnlock，混用会 fatal error
				//m.Unlock()
			}
		}()
	}
	wg.Wait()
	fmt.Printf("counter: %d, duration: %v\n", counter, time.Since(start))
}
