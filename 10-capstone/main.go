// 第 10 节：综合项目，简化的任务调度引擎。
//
// 第 1 步：Scheduler 的共享状态 + 监控 goroutine。
// 第 2 步：5 个 worker，每个内部是 source → process 两级 pipeline，source 拉数据前要拿信号量。
// 第 3 步：会失败的 worker（errgroup 取消所有）、慢 worker（占着信号量）、退出前检查 goroutine 泄漏。
//
// 运行：
//
//	go run -race ./10-capstone           // 正常跑 5 秒
//	go run ./10-capstone -limit 5        // 改信号量容量
//	go run ./10-capstone -fail 3         // worker 3 在 2 秒时失败
//	go run ./10-capstone -slow 2         // worker 2 每次拉取 500ms
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// Scheduler 保存所有 worker 共享的状态。
type Scheduler struct {
	mu       sync.RWMutex   // 保护 results：写用 Lock，读用 RLock
	results  map[string]int // 处理结果，懒初始化
	counter  atomic.Int64   // 总共处理了多少条数据，单个计数用 atomic 就够
	initOnce sync.Once      // 保证 results 只创建一次
	sem      chan struct{}  // 信号量：限制同时拉数据的 worker 数（第 2 步使用）
}

// NewScheduler 创建调度器，limit 是信号量容量。
// results 不在这里 make，而是交给 init 懒初始化，用来练习 sync.Once。
func NewScheduler(limit int) *Scheduler {
	return &Scheduler{sem: make(chan struct{}, limit)}
}

// init 用 sync.Once 创建 results。Set 和 Stats 开头都调用它，
// 不管谁先被调用、被多少个 goroutine 同时调用，map 都只创建一次。
// 已经初始化过之后，Do 只是一次原子读，开销很小。
func (s *Scheduler) init() {
	s.initOnce.Do(func() {
		s.results = make(map[string]int)
	})
}

// Set 写入一条结果，并让计数器加 1。
// counter 是 atomic，不需要锁保护，放在锁外面让临界区更短。
func (s *Scheduler) Set(key string, val int) {
	s.init()
	s.mu.Lock()
	s.results[key] = val
	s.mu.Unlock()
	s.counter.Add(1)
}

// Stats 返回 map 大小和计数器的值。
// 注意：两个值不是同一时刻的快照，counter 在锁外读，可能比 size 对应的时刻稍新，监控打印够用。
func (s *Scheduler) Stats() (size int, count int64) {
	s.init()
	s.mu.RLock()
	size = len(s.results) // len 对 map 是 O(1)，不用遍历
	s.mu.RUnlock()
	return size, s.counter.Load()
}

// monitor 每 500ms 打印一次状态，ctx 结束时打印最终状态后返回。
// 必须用 Ticker：Timer 只触发一次，第二轮 <-t.C 就永远等不到。
func (s *Scheduler) monitor(ctx context.Context) error {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// ctx 结束后 Done 会一直就绪，这里必须 return，否则 for 循环会疯狂空转
			size, count := s.Stats()
			// context.Cause 拿到取消的具体原因：到点是 DeadlineExceeded，
			// errgroup 因为某个 goroutine 出错而取消时，Cause 就是那个错误
			fmt.Printf("[最终] size=%d counter=%d（原因：%v）\n", size, count, context.Cause(ctx))
			return nil // 到点退出是正常结束，不当作错误
		case <-t.C:
			size, count := s.Stats()
			fmt.Printf("[监控] size=%d counter=%d\n", size, count)
		}
	}
}

// item 是 source 拉到的一条数据。
type item struct {
	key string
	val int
}

// fetch 模拟一次拉取，耗时 d。ctx 先结束就提前返回 false。
// 用 Timer + select 而不是 time.Sleep：Sleep 没法被 ctx 打断，退出时要多等一次拉取的时间。
func fetch(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// source 是 pipeline 的第一级：循环拉数据，发到 out。
// 它是 out 唯一的写入方，所以由它 close；close 后下游的 range 才会结束。
// cost 是每次拉取的耗时，慢 worker 传更大的值。
func (s *Scheduler) source(ctx context.Context, id int, cost time.Duration, out chan<- item) {
	defer close(out)
	for seq := 0; ; seq++ {
		// 1. 拿信号量。满了会阻塞，所以放进 select，ctx 结束时能退出
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}

		// 2. 拉数据，拉完立刻释放信号量。
		//    不用 defer：这是循环，defer 要等函数返回才执行，信号量会一直不释放。
		ok := fetch(ctx, cost)
		<-s.sem
		if !ok {
			return
		}

		// 3. 发给下游。下游慢的时候会阻塞，同样要能被 ctx 打断
		select {
		case out <- item{key: fmt.Sprintf("w%d-%d", id, seq), val: seq}:
		case <-ctx.Done():
			return
		}
	}
}

// worker 是 pipeline 的第二级：启动 source，然后处理它发来的数据。
// cost 是每次拉取的耗时；failAfter > 0 时，运行到这个时间就返回错误，模拟 worker 失败。
//
// worker 自己再派生一个 wctx 给 source：worker 不管因为什么返回，都先取消 wctx 让 source 退出，
// 再把 ch 读到关闭，确认 source 已经结束，然后才返回。
// 只靠 errgroup 也能让 source 退出（worker 返回错误后 gctx 会被取消），但那时 g.Wait() 可能已经返回，
// source 还在跑，它就成了一个比 errgroup 活得更久的 goroutine（第 3 步的泄漏检查会看到）。
func (s *Scheduler) worker(ctx context.Context, id int, cost, failAfter time.Duration) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan item)
	go s.source(wctx, id, cost, ch)

	// 不失败的 worker：failC 是 nil，nil channel 在 select 里永远不会被选中
	var failC <-chan time.Time
	if failAfter > 0 {
		t := time.NewTimer(failAfter)
		defer t.Stop()
		failC = t.C
	}

	n := 0 // 这个 worker 处理了多少条，只有它自己访问，不需要同步
	for {
		select {
		case it, ok := <-ch:
			if !ok { // source 因为 ctx 结束而退出，正常返回
				fmt.Printf("  worker %d 退出：处理 %d 条（%v）\n", id, n, context.Cause(ctx))
				return nil
			}
			s.Set(it.key, it.val)
			n++
		case <-failC:
			cancel()       // 通知 source 退出
			for range ch { // 等 source close(ch)，丢弃它退出前发来的数据
			}
			fmt.Printf("  worker %d 失败：处理 %d 条\n", id, n)
			return fmt.Errorf("worker %d 失败", id)
		}
	}
}

func main() {
	limit := flag.Int("limit", 3, "信号量容量：同时最多几个 worker 在拉数据")
	fail := flag.Int("fail", 0, "哪个 worker 在 2 秒时失败，0 表示不失败")
	slow := flag.Int("slow", 0, "哪个 worker 每次拉取 500ms，0 表示没有慢 worker")
	flag.Parse()

	before := runtime.NumGoroutine() // 启动前的 goroutine 数，退出时对比
	start := time.Now()

	// 根 ctx：5 秒后自动取消，所有 goroutine 都派生于它
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// errgroup 派生的 gctx：5 秒到了，或者任意一个 goroutine 返回错误，都会被取消
	g, gctx := errgroup.WithContext(ctx)

	s := NewScheduler(*limit)
	fmt.Printf("5 个 worker，信号量容量 %d，失败 worker=%d，慢 worker=%d\n", *limit, *fail, *slow)

	g.Go(func() error { return s.monitor(gctx) })
	for id := 1; id <= 5; id++ { // Go 1.22 起循环变量每轮是新的，闭包里可以直接用 id
		cost := 50 * time.Millisecond
		if id == *slow {
			cost = 500 * time.Millisecond
		}
		var failAfter time.Duration
		if id == *fail {
			failAfter = 2 * time.Second
		}
		g.Go(func() error { return s.worker(gctx, id, cost, failAfter) })
	}

	// 到点退出时所有 goroutine 都返回 nil，Wait 返回 nil；
	// 如果有 goroutine 返回 ctx.Err()，就会拿到 DeadlineExceeded，要用 errors.Is 区分正常到点和真正失败
	if err := g.Wait(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		fmt.Println("调度器出错:", err)
	}
	fmt.Printf("退出，耗时 %v，goroutine 数：启动前 %d，退出后 %d\n",
		time.Since(start).Round(time.Millisecond), before, runtime.NumGoroutine())
}
