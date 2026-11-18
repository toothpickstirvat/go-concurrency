// 第 6 节：Pipeline（流水线）
//
// 四级 pipeline：生成器(1–20) → 过滤偶数 → 平方 → 打印统计
//
// 要点：
//   - 每个 stage 是它输出 channel 的唯一写入方，用 defer close(out) 关闭
//   - close 自动传播：上游 close → 下游 range 结束 → 下游 close 自己的输出 → ...
//   - stage3 有 3 个并发 worker，用 WaitGroup 等待后再 close out
//   - 每个 stage 监听 ctx.Done()，超时时所有 stage 都能退出
//
// 运行：
//
//	go run -race ./06-pipeline
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// stage1 生成 1 到 20 的整数。
// 它是 out 的唯一写入方，写完后 close。
// 每轮循环都检查 ctx 是否取消：发送时用 select 同时等 out 和 ctx.Done()。
func stage1(ctx context.Context, out chan<- int) {
	defer close(out)
	for i := 1; i <= 20; i++ {
		time.Sleep(time.Duration(10+rand.IntN(40)) * time.Millisecond)
		select {
		case out <- i:
		case <-ctx.Done():
			return
		}
	}
}

// stage2 过滤偶数：只保留 in 里的偶数，写进 out。
// 它是 out 的唯一写入方，range 结束后 close。
// 用 select 同时等 in 和 ctx.Done()，发送时也一样。
func stage2(ctx context.Context, in <-chan int, out chan<- int) {
	defer close(out)
	for {
		select {
		case v, ok := <-in:
			if !ok {
				return
			}
			time.Sleep(time.Duration(10+rand.IntN(40)) * time.Millisecond)
			if v%2 == 0 {
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// stage3 平方：每个数平方后写进 out。
// 3 个 goroutine 并发处理，每个都要监听 ctx.Done()。
func stage3(ctx context.Context, in <-chan int, out chan<- int) {
	defer close(out)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case v, ok := <-in:
					if !ok {
						return
					}
					time.Sleep(time.Duration(10+rand.IntN(40)) * time.Millisecond)
					select {
					case out <- v * v:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	wg.Wait()
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch1 := make(chan int)
	ch2 := make(chan int)
	ch3 := make(chan int, 5)
	// 启动 stage 1-3
	start := time.Now()
	go stage1(ctx, ch1)
	go stage2(ctx, ch1, ch2)
	go stage3(ctx, ch2, ch3)

	// stage 4：主 goroutine 消费并统计
	count := 0
	for v := range ch3 {
		fmt.Printf("[%6.0fms] %d\n", time.Since(start).Seconds()*1000, v)
		count++
	}
	fmt.Printf("总共 %d 个数，耗时 %v\n", count, time.Since(start))
}
