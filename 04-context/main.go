// 第 4 节：Context。根 ctx 4 秒超时，3 个 worker 分别需要 1s、3s、5s；
// 每个 worker 再派生一个子 goroutine，并用 WithValue 传 requestID。
//
// 运行：
//
//	go run -race ./04-context
//
// 预期：worker 1、2 完成，它们的子任务被主动 cancel（context canceled）；
// worker 3 和它的子任务因根 ctx 超时退出（context deadline exceeded）；总耗时约 4 秒。
package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ctxKey 是专门用来做 context key 的未导出类型。
// 不能用字符串或内置类型直接当 key：如果另一个包也用字符串 "requestID" 当 key，
// 两个包会互相覆盖对方的值，而且 go vet 会警告 "should not use built-in type string as key for value".
// 用未导出类型，包外代码无法构造出相同的 key，不会冲突。
type ctxKey struct{}

// requestIDFromCtx 从 ctx 里取 requestID，取不到就返回空字符串。
// 单独封装成函数：调用方不需要知道 key 类型和类型断言，也不会忘记处理 nil 的情况。
func requestIDFromCtx(ctx context.Context) string {
	v, ok := ctx.Value(ctxKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

func child(ctx context.Context, id int, ch chan struct{}) {
	// ctx 是从 childCtx 派生的，childCtx 又从 workerCtx 派生，workerCtx 从带 requestID 的 ctx 派生。
	// Value 查找会沿着 ctx 树往上找：子 ctx 自己没有这个 key，就去父 ctx 找，所以这里能拿到。
	rid := requestIDFromCtx(ctx)
	select {
	case <-ctx.Done():
		fmt.Printf("[%s] child %d 子任务被取消, err: %v\n", rid, id, ctx.Err())
	case <-time.After(10 * time.Second):
		fmt.Printf("[%s] child %d 子任务完成\n", rid, id)
	}
	ch <- struct{}{}
}

func worker(ctx context.Context, id int, d time.Duration, wg *sync.WaitGroup) {
	defer wg.Done()

	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan struct{})
	go child(childCtx, id, ch)

	rid := requestIDFromCtx(ctx)
	select {
	case <-ctx.Done():
		fmt.Printf("[%s] worker %d 被取消, err: %v\n", rid, id, ctx.Err())
	case <-time.After(d):
		fmt.Printf("[%s] worker %d 完成\n", rid, id)
	}
	cancel() // 先通知子任务退出，再等它真的退出；只放 defer 里会导致两边互相等
	<-ch
}

func main() {
	start := time.Now()
	wg := sync.WaitGroup{}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// WithValue 把值挂在 ctx 树上，不修改父 ctx，只影响这条链往下的派生。
	// 不要用 WithValue 传普通业务参数（比如 d time.Duration）：
	//   1. 类型不安全：Value 返回 any，调用方必须断言，编译器查不出错误。
	//   2. 看不见：函数签名里没有这个参数，读代码的人不知道函数依赖了 ctx 里的某个值。
	// WithValue 只适合传"请求范围内的元数据"：链路追踪 ID、认证信息等，不适合传控制流或业务数据。
	ctx = context.WithValue(ctx, ctxKey{}, "req-42")

	wg.Add(1)
	go worker(ctx, 1, 1*time.Second, &wg)
	wg.Add(1)
	go worker(ctx, 2, 3*time.Second, &wg)
	wg.Add(1)
	go worker(ctx, 3, 5*time.Second, &wg)

	wg.Wait()
	fmt.Printf("elapsed: %v\n", time.Since(start))
}
