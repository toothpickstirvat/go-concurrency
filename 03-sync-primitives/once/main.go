// 第 3 节任务 A：用 sync.Once 实现只加载一次的配置（懒加载单例）。
// 10 个 goroutine 同时第一次调用 Load()，加载函数只执行一次。
//
// 运行：
//
//	go run -race ./03-sync-primitives/once
//
// 实测：10 个 goroutine 都等了约 100ms，拿到的是同一个指针，counter = 1。
// 没抢到执行权的 goroutine 不会直接返回 nil，而是阻塞到加载函数执行完才返回。
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Config 的 once 和 counter 都不能复制（内含 noCopy），所以 Config 要用指针或直接引用同一个变量。
type Config struct {
	once    sync.Once
	counter atomic.Int32 // 统计加载函数实际执行了几次，验证"只加载一次"
	data    *ConfigData  // 加载结果，只在 once.Do 里写入一次
}

type ConfigData struct {
	User     string
	Password string
}

// Load 第一次调用时加载配置，之后直接返回缓存。
//
// once.Do 的实现（简化）：
//
//	if done.Load() == 0 {      // 快路径：已经执行过就直接返回，只是一次原子读
//	    m.Lock()               // 慢路径：加锁，其他 goroutine 在这里阻塞
//	    if done.Load() == 0 {  // 双重检查：拿到锁后再确认一次
//	        f()
//	        done.Store(1)      // 注意：f 返回之后才标记完成
//	    }
//	    m.Unlock()
//	}
//
// 所以 Do 返回时，f 一定已经执行完了。在 Do 之后读 c.data 不需要再加锁，
// 写入 c.data 和 Do 返回之间存在 happens-before 关系。
func (c *Config) Load() *ConfigData {
	c.once.Do(func() {
		c.counter.Add(1)

		time.Sleep(100 * time.Millisecond) // 模拟从磁盘加载
		c.data = &ConfigData{
			User:     "admin",
			Password: "admin",
		}
		// 注意：这里面不能再调用 c.Load()（即 c.once.Do），
		// Once 内部的 Mutex 不可重入，会死锁。
	})
	return c.data
}

func main() {
	wg := sync.WaitGroup{}
	var cfg Config // 零值可用，不需要初始化 once

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			data := cfg.Load()
			// %p 打印指针地址：10 个 goroutine 应该是同一个地址
			fmt.Printf("data: %p, elapsed: %v\n", data, time.Since(start))
		}()
	}

	wg.Wait()

	fmt.Printf("counter = %d\n", cfg.counter.Load()) // 应该是 1
}
