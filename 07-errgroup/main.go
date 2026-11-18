// 第 7 节：errgroup 并发抓取多个 URL，第一个错误会取消所有其他 goroutine。
//
// 运行：
//
//	go run -race ./07-errgroup
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"golang.org/x/sync/errgroup"
)

// fetchURL 模拟抓取一个 URL。
// sleep 随机 100–500ms；URL 包含 "fail" 时返回错误。
// 监听 ctx.Done()，取消时提前返回。
func fetchURL(ctx context.Context, url string) error {
	// 随机延迟 100–500ms
	duration := time.Duration(100+rand.IntN(400)) * time.Millisecond

	// 用 select 同时等延迟和 ctx.Done()，让 goroutine 能在 ctx 取消时立刻退出
	select {
	case <-time.After(duration):
		// 延迟结束，检查是否应该失败
		if contains(url, "fail") {
			return errors.New("抓取失败: " + url)
		}
		fmt.Printf("✓ 抓取成功: %s (耗时 %v)\n", url, duration)
		return nil
	case <-ctx.Done():
		// ctx 取消了（因为其他 goroutine 出错），立刻返回
		fmt.Printf("✗ 已取消: %s (ctx.Err: %v)\n", url, ctx.Err())
		return ctx.Err()
	}
}

// contains 简单的字符串包含检查
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func main() {
	// 10 个 URL，其中 2 个包含 "fail"
	urls := []string{
		"https://example.com/page1",
		"https://example.com/page2",
		"https://example.com/fail-page3", // 会失败
		"https://example.com/page4",
		"https://example.com/page5",
		"https://example.com/fail-page6", // 会失败
		"https://example.com/page7",
		"https://example.com/page8",
		"https://example.com/page9",
		"https://example.com/page10",
	}

	// errgroup.WithContext 创建一个 group 和 ctx。
	// 任意一个 goroutine 返回非 nil error 时，ctx 自动取消，所有监听 ctx.Done() 的 goroutine 都能退出。
	g, ctx := errgroup.WithContext(context.Background())

	// SetLimit 限制最多同时运行 3 个 goroutine，其余的在 g.Go() 调用时阻塞等待。
	// 内部用信号量实现，避免一次性启动大量 goroutine。
	// 不加限制时，10 个 goroutine 同时启动，第一个失败就取消所有，耗时约 170ms。
	// 加限制后，前 3 个跑完才启动后面的，如果前 3 个都成功，总耗时会变长（约 400–500ms）。
	g.SetLimit(3)
	start := time.Now()

	// 循环启动 10 个 goroutine
	for _, url := range urls {
		url := url // 闭包变量捕获：Go 1.22+ 不需要，但为了兼容性保留
		g.Go(func() error {
			return fetchURL(ctx, url)
		})
	}

	// Wait 阻塞，直到所有 goroutine 都返回。
	// 返回第一个非 nil error（多个 error 时返回第一个，但不保证是哪个）。
	if err := g.Wait(); err != nil {
		fmt.Printf("\n遇到错误: %v\n", err)
	} else {
		fmt.Println("\n所有 URL 抓取成功")
	}

	fmt.Printf("总耗时: %v\n", time.Since(start))
}
