// 第 5 节：select 进阶，三个小程序顺序运行。
//
//	任务 A：超时控制。worker 500ms 内没收到数据就退出，生产者每 1 秒发一个。
//	任务 B：优先级 channel。两层 select，high 有数据先读 high，空了才读 low。
//	任务 C：多路复用（fan-in）。两个生产者写同一个 channel，WaitGroup 等写完后再 close。
//
// 运行：
//
//	go run -race ./05-select
package main

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// producer 每隔 1 秒往 ch 写一个随机数，收到 exit 信号后关闭 ch 并退出。
// 只有写入方才能 close channel；这里 producer 是唯一的写入方，所以由它 close。
//
// time.After 在循环里每轮都会新建一个定时器。Go 1.23 起没被引用的定时器会被 GC 回收，不会泄漏，
// 但每轮仍有一次分配；高频循环里可以改成在循环外 time.NewTimer，每轮 Reset。
//
// 注意：ch <- 这一步本身是阻塞的，没有放进 select。这里 worker 在第 500ms 就退出，
// producer 第一次发送在第 1 秒，所以不会卡住；但如果 worker 先退出而 producer 正卡在发送上，
// producer 会永远阻塞。更稳妥的写法是发送也放进 select：
//
//	select { case ch <- v: case <-exit: close(ch); return }
func producer(ch chan int, exit chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-time.After(1 * time.Second):
			ch <- rand.IntN(101)
		case <-exit:
			close(ch) // 通知 worker：没有更多数据了
			return
		}
	}
}

// worker 从 ch 读数据并打印；500ms 内没有数据就打印"超时"然后退出。
// 退出时关闭 exit，通知 producer 停下来。
//
// time.After 每进入一次 select 就重新计时，所以这里的语义是"距离上一条数据超过 500ms"，
// 而不是"总共只等 500ms"。要限制总时长，应该在循环外创建一个定时器（或用 context.WithTimeout）。
func worker(ch chan int, exit chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-time.After(500 * time.Millisecond):
			fmt.Println("worker 超时")
			close(exit) // 通知 producer 停下
			return      // 超时就退出，不能只打印再循环
		case data, ok := <-ch:
			// 必须检查 ok：ch 被 close 后，case 会立刻以零值反复触发，
			// 不检查 ok 会变成一个读零值的死循环。
			if !ok {
				return
			}
			fmt.Printf("worker 收到 %d\n", data)
		}
	}
}

// taskB 消费者：只要 high 有数据就优先读 high，high 空了才读 low。
//
// 为什么要两层：单个 select 里多个 case 同时就绪时，Go 随机选一个，case 的书写顺序不代表优先级。
//   - 外层带 default，是非阻塞的：high 有数据就取走、continue；没有就走 default，落到内层。
//   - 内层不带 default，是阻塞的：等 high 或 low 任意一个。加了 default 就成了忙等待，CPU 100%。
//
// 内层里 high 和 low 同时就绪时仍然是随机的，所以这是"尽量优先"，不是严格优先。
// 另外这是绝对优先：high 一直有数据，low 就永远读不到（饥饿）。
//
// 退出方式：两个 channel 关闭后各自置 nil。nil channel 在 select 里永远不会被选中，
// 等于把这个 case 注销；两个都是 nil 时退出。
// close 由写入方（main）负责，读取方不能 close。
func taskB(high <-chan int, low <-chan int, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		// 两个 channel 都读完（置 nil）才退出
		if high == nil && low == nil {
			return
		}
		// 外层：非阻塞试 high，有数据立刻取
		select {
		case v, ok := <-high:
			if !ok {
				high = nil // nil channel 在 select 里永远阻塞，相当于"注销"这个 case
				continue
			}
			fmt.Printf("[B] high: %d\n", v)
			continue
		default:
		}

		// 走到这里说明 high 当前是空的，阻塞等任意一个（不加 default，否则变成忙等待）
		select {
		case v, ok := <-high:
			if !ok {
				high = nil
				continue
			}
			fmt.Printf("[B] high: %d\n", v)
		case v, ok := <-low:
			if !ok {
				low = nil
				continue
			}
			fmt.Printf("[B] low:  %d\n", v)
		}
	}
}

func main() {
	// ── 任务 A：超时控制 ──
	fmt.Println("=== 任务 A ===")
	ch := make(chan int)
	exit := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go producer(ch, exit, &wg)
	go worker(ch, exit, &wg)
	wg.Wait()

	// ── 任务 B：优先级 channel ──
	fmt.Println("=== 任务 B ===")
	// 用带缓冲 channel 预先填好数据并关闭，taskB 启动时 high 和 low 都已就绪，
	// 正好检验优先级：high 的 5 个数应该全部出现在 low 之前。
	// 关闭的 channel 里剩余的数据仍然能读出来，读空之后才返回 ok=false。
	high := make(chan int, 5)
	low := make(chan int, 5)

	for i := range 5 {
		high <- (i + 1) * 10 // 10, 20, 30, 40, 50
	}
	for i := range 5 {
		low <- i + 1 // 1, 2, 3, 4, 5
	}
	close(high)
	close(low)
	var wg2 sync.WaitGroup
	wg2.Add(1)
	go taskB(high, low, &wg2)
	wg2.Wait()

	// ── 任务 C：多路复用（fan-in） ──
	fmt.Println("=== 任务 C ===")
	ch3 := make(chan int)

	// 两个生产者各自写完后通知 wgProd；wgProd 归零后由专门的 goroutine close ch3。
	// 不能在每个生产者里直接 close：第一个关完，第二个再关会 panic（close of closed channel）。
	var wgProd sync.WaitGroup
	wgProd.Add(2)
	go func() { // 生产者 1：写 0–4
		defer wgProd.Done()
		for i := range 5 {
			ch3 <- i
		}
	}()
	go func() { // 生产者 2：写 100–104，用不同的值方便区分来源
		defer wgProd.Done()
		for i := range 5 {
			ch3 <- 100 + i
		}
	}()
	// 等两个生产者都写完，再 close；close 之后消费者的 range 才会结束。
	go func() {
		wgProd.Wait()
		close(ch3)
	}()

	// 消费者：range 在 ch3 关闭且读空后自动退出
	var wgCons sync.WaitGroup
	wgCons.Add(1)
	go func() {
		defer wgCons.Done()
		for v := range ch3 {
			fmt.Printf("[C] %d\n", v)
		}
	}()
	wgCons.Wait()
}
