// 第 8 节：信号量（semaphore）限制并发数
//
// 任务 A：用 buffered channel 当信号量，100 个任务最多同时跑 5 个
// 任务 B：用 weighted semaphore，20 个任务带权重（1、2、3），总容量 10
//
// 运行：
//
//	go run -race ./08-semaphore
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// taskA 用 buffered channel 限流
func taskA() {
	fmt.Println("=== 任务 A：buffered channel 信号量 ===")

	// buffered channel 当信号量：容量 5，最多同时 5 个任务持有
	// 原理：channel 满了，写入就阻塞；读走一个，腾出一个位置。
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// 获取信号量：写入一个元素，满了就阻塞
			// 注意：这一行必须在 defer 之前，否则 defer 会在还没拿到信号量时就注册释放动作
			sem <- struct{}{}
			// 释放信号量：读走一个元素，必须在 defer 里，保证一定释放
			// struct{}{} 是空结构体，不占内存，只用来占位
			defer func() { <-sem }()

			// 任务开始
			fmt.Printf("[A] 任务 %d 开始 (%v)\n", id, time.Since(start))
			time.Sleep(100 * time.Millisecond)
			fmt.Printf("[A] 任务 %d 结束 (%v)\n", id, time.Since(start))
		}(i)
	}

	wg.Wait()
	fmt.Printf("[A] 总耗时: %v\n\n", time.Since(start))
}

// taskB 用 weighted semaphore
func taskB() {
	fmt.Println("=== 任务 B：weighted semaphore ===")

	// NewWeighted(10)：总容量 10
	sem := semaphore.NewWeighted(10)
	var wg sync.WaitGroup
	ctx := context.Background()
	start := time.Now()

	// 20 个任务，权重轮流 1、2、3
	weights := []int64{1, 2, 3}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		weight := weights[i%3]
		go func(id int, w int64) {
			defer wg.Done()

			// Acquire 尝试获取 w 个配额，不足就阻塞
			// 注意：Acquire 可能返回错误（ctx 取消时），这里 ctx 是 Background，不会取消
			if err := sem.Acquire(ctx, w); err != nil {
				fmt.Printf("[B] 任务 %d 获取失败: %v\n", id, err)
				return
			}
			// Release 释放 w 个配额，必须在 defer 里
			defer sem.Release(w)

			// 任务开始
			fmt.Printf("[B] 任务 %d (权重 %d) 开始，剩余配额约 %d (%v)\n",
				id, w, 10-w, time.Since(start))
			time.Sleep(100 * time.Millisecond)
			fmt.Printf("[B] 任务 %d (权重 %d) 结束 (%v)\n", id, w, time.Since(start))
		}(i, weight)
	}

	wg.Wait()
	fmt.Printf("[B] 总耗时: %v\n\n", time.Since(start))
}

func main() {
	taskA()
	taskB()
}
