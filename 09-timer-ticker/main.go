// 第 9 节：定时器 Timer 和 Ticker（本机 Go 1.27）
//
//	任务 A：循环里每轮 time.After vs 复用一个 Timer + Reset，对比耗时、分配次数、goroutine 数
//	任务 B：Ticker 每 100ms 执行一次，10 次后停止；模拟一次处理太慢，观察 tick 被丢弃
//	任务 C：worker 同时等"完成 / ctx 取消 / 超时"，两种写法：三个 case，或 context.WithTimeout + ctx.Err()
//
// 运行（测耗时不要带 -race）：
//
//	go run ./09-timer-ticker
package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// 循环次数
const loops = 1_000_000

// report 打印耗时、分配次数和 goroutine 数。
// m1 是循环前的内存统计，函数里读循环后的统计，两者相减得到循环期间的分配次数。
func report(name string, start time.Time, m1 *runtime.MemStats) {
	elapsed := time.Since(start)
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	mallocs := m2.Mallocs - m1.Mallocs
	fmt.Printf("%-8s 耗时 %-14v 分配 %8d 次（每轮 %.1f 次） goroutines=%d\n",
		name, elapsed, mallocs, float64(mallocs)/loops, runtime.NumGoroutine())
}

// taskAV1：每轮循环都调用 time.After，新建一个定时器。
// 实测每轮分配 3 次（Timer、带缓冲的 channel、内部结构），100 万轮约 300 万次分配，耗时约 230–400ms。
// goroutine 数始终是 1：time.After 不创建 goroutine。
// Go 1.23 起没人引用的定时器即使没到期也能被 GC 回收，所以堆内存不涨；Go 1.23 之前要等 1 分钟到期才能回收。
func taskAV1() {
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	start := time.Now()

	ch := make(chan int, 1)
	for i := 0; i < loops; i++ {
		ch <- i
		select {
		case <-ch:
		case <-time.After(time.Minute): // 每轮新建一个定时器
		}
	}

	report("taskAV1", start, &m1)
}

// taskAV2：循环外创建一个 Timer，循环里每轮 Reset 复用。
// 实测整个函数只分配 4 次，耗时约 110–125ms，比 taskAV1 快一倍多。
//
// Go 1.23 起 Timer 的 channel 是无缓冲的（同步），Stop/Reset 返回后保证不会再收到旧的值，
// 所以直接 Reset 就行。Go 1.23 之前要先 Stop，再把可能残留的值读掉（drain），否则下一轮会立刻读到上一轮的超时：
//
//	if !t.Stop() { <-t.C }
//	t.Reset(d)
func taskAV2() {
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	start := time.Now()

	ch := make(chan int, 1)
	t := time.NewTimer(time.Minute)
	defer t.Stop() // 不用的定时器及时 Stop，是兼容 Go 1.23 之前版本的好习惯
	for i := 0; i < loops; i++ {
		ch <- i
		t.Reset(time.Minute) // 复用同一个定时器，重新计时
		select {
		case <-ch:
		case <-t.C:
		}
	}

	report("taskAV2", start, &m1)
}

// taskB：用 Ticker 每 100ms 执行一次，执行 10 次后停止。
// slowTick > 0 时，在第 slowTick 次 tick 里多 sleep 250ms，模拟一次处理太慢，观察后面的 tick 怎么变化。
//
// Timer 只触发一次，想再触发必须 Reset；Ticker 每隔固定时间触发一次，直到 Stop。
// 这里如果用 NewTimer，第一次触发后 <-t.C 再也收不到值，main 永远阻塞，运行时报 deadlock。
//
// 实测 slowTick=3：tick 3 在 301ms 开始睡 250ms，tick 4 在 552ms 立刻到达（400ms 那次被保留），
// tick 5 回到 600ms 的刻度；500ms 那次被丢弃。Ticker 最多保留一个没取走的 tick，不会补发，也不会让节奏后移。
func taskB(slowTick int) {
	fmt.Printf("=== 任务 B（slowTick=%d）===\n", slowTick)
	start := time.Now()

	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop() // 保证函数返回时一定 Stop

	counter := 0
	for range t.C { // 只等一个 channel，用 range 比 for + select 更简洁
		counter++
		fmt.Printf("tick %2d: %v\n", counter, time.Since(start).Round(time.Millisecond))
		if counter == slowTick {
			time.Sleep(250 * time.Millisecond) // 这次处理比 tick 间隔长
		}
		if counter == 10 { // 第 10 次打印完立刻退出，不用多等一个 tick
			return
		}
	}
}

// worker 做一件耗时 work 的事，有两个退出条件：ctx 被取消，或者超时 500ms。
// 一个 select 等三个 case，哪个先到就因为哪个退出。
func worker(ctx context.Context, work time.Duration) string {
	done := time.NewTimer(work) // 模拟工作本身要花的时间
	defer done.Stop()
	timeout := time.NewTimer(500 * time.Millisecond) // 超时上限
	defer timeout.Stop()                             // 提前返回时也要 Stop

	select {
	case <-done.C:
		return "完成"
	case <-ctx.Done():
		return "被取消: " + ctx.Err().Error()
	case <-timeout.C:
		return "超时"
	}
}

// taskC：main 在 cancelAfter 后调用 cancel()，看 worker 因为哪个条件退出。
func taskC(work, cancelAfter time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 另起一个 goroutine，到时间就取消。用 time.AfterFunc 也可以：time.AfterFunc(cancelAfter, cancel)
	go func() {
		time.Sleep(cancelAfter)
		cancel()
	}()

	start := time.Now()
	result := worker(ctx, work)
	fmt.Printf("work=%-6v cancelAfter=%-6v → %s（%v）\n",
		work, cancelAfter, result, time.Since(start).Round(time.Millisecond))
}

// workerWithTimeout：超时交给 context.WithTimeout，select 只等两个 case。
// 超时和取消都会关闭 ctx.Done()，靠 ctx.Err() 区分：
// 超时是 context.DeadlineExceeded，手动 cancel 是 context.Canceled。
func workerWithTimeout(parent context.Context, work time.Duration) string {
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()

	done := time.NewTimer(work)
	defer done.Stop()

	select {
	case <-done.C:
		return "完成"
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "超时"
		}
		return "被取消: " + ctx.Err().Error()
	}
}

// taskC2：和 taskC 一样的三组参数，换成 workerWithTimeout。
func taskC2(work, cancelAfter time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(cancelAfter, cancel) // 到时间在另一个 goroutine 里执行 cancel

	start := time.Now()
	result := workerWithTimeout(ctx, work)
	fmt.Printf("work=%-6v cancelAfter=%-6v → %s（%v）\n",
		work, cancelAfter, result, time.Since(start).Round(time.Millisecond))
}

func main() {
	fmt.Println("=== 任务 A ===")
	taskAV1()
	taskAV2()

	taskB(0) // 正常节奏
	taskB(3) // 第 3 次 tick 处理了 250ms

	fmt.Println("=== 任务 C：三个 Timer + select ===")
	taskC(200*time.Millisecond, time.Second) // 预期：完成
	taskC(time.Second, 300*time.Millisecond) // 预期：被取消
	taskC(time.Second, time.Second)          // 预期：超时

	fmt.Println("=== 任务 C：context.WithTimeout + ctx.Err() ===")
	taskC2(200*time.Millisecond, time.Second)
	taskC2(time.Second, 300*time.Millisecond)
	taskC2(time.Second, time.Second)
}
