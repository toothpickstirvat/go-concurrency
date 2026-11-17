// 第 2 节任务 B：用 atomic.Int32 + CompareAndSwap 手写自旋锁，并和 sync.Mutex 对比。
// 仅用于学习原理，真实代码请用 sync.Mutex。
//
// 运行（先编译再加 timeout，超时退出码是 124）：
//
//	go run -race ./02-atomic/spinlock
//	go build -o /tmp/spinlock ./02-atomic/spinlock
//	time (GOMAXPROCS=1 timeout 120 /tmp/spinlock; echo "exit=$?")
//
// 实测（临界区里都调用了 runtime.Gosched()，模拟持锁的 goroutine 被换下 CPU）：
//
//	SpinLock，自旋时不让出 CPU      单核 / 4 核：超过 2 分钟（超时），CPU 全部满负荷空转
//	SpinLock，自旋时 Gosched 让出   单核约 264ms，4 核约 505ms
//	sync.Mutex                      单核 / 4 核：约 22ms，CPU 时间也只有约 23ms
//
// 切换方式：修改 main 开头 sl 的定义；对比"自旋时不让出 CPU"时，注释掉 Lock 里的 runtime.Gosched()。
package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// SpinLock 是用 CAS 实现的自旋锁。
type SpinLock struct {
	state atomic.Int32 // 0=未锁，1=已锁；小写不导出，外部只能通过 Lock/Unlock 修改
}

// TryLock 只尝试一次：锁空闲（0）就改成 1 并返回 true，否则立即返回 false。
// 对应 sync.Mutex.TryLock。
// 注意 CAS 的返回值不能丢：丢掉就无法知道有没有拿到锁，锁形同虚设（第一版的 bug）。
func (s *SpinLock) TryLock() bool {
	return s.state.CompareAndSwap(0, 1)
}

// Lock 循环调用 TryLock，直到拿到锁才返回。
// CAS 失败后调用 runtime.Gosched() 主动让出 CPU，让持锁的 goroutine 能尽快运行并解锁。
// 如果不让出，等锁的 goroutine 会一直空转，直到调度器约 10ms 后强制切换；
// 持锁者被换下 CPU 时，它要排在一堆空转的 goroutine 后面才能拿回 CPU 去解锁，程序几乎停滞。
// 即使让出了，等锁的 goroutine 仍在运行队列里，会被反复调度、反复失败，所以还是比 Mutex 慢很多。
func (s *SpinLock) Lock() {
	for !s.TryLock() {
		// CAS 失败说明锁被别人持有
		runtime.Gosched()
	}
}

// Unlock 释放锁。Store 和 CAS 都是 atomic 操作，二者之间存在 happens-before 关系：
// 解锁前对 counter 的写入，对下一个 CAS 成功的 goroutine 可见，所以普通 int 也能被正确保护。
// 这个简单实现不检查调用者是否持有锁；sync.Mutex 遇到对未加锁的锁 Unlock 会直接 fatal error。
func (s *SpinLock) Unlock() {
	s.state.Store(0)
}

func main() {
	// sync.Mutex 抢不到锁时：先短暂自旋（仅多核且锁可能很快释放时），然后把 goroutine 挂起到
	// 锁自己的等待队列，不在运行队列里，调度器不会选到它；解锁时只唤醒一个等待者。
	// 所以持锁者让出 CPU 后几乎马上被调度回来，多出来的核也不会被空转浪费。
	//sl := &SpinLock{}
	sl := sync.Mutex{}
	start := time.Now()
	counter := 0 // 普通 int，只靠锁保护
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				sl.Lock() // SpinLock 和 Mutex 的 Lock 都会等到拿到锁才返回
				counter++
				runtime.Gosched() // 持锁时主动让出 CPU，模拟持锁者被调度器换下（真实代码里不要这样写）
				sl.Unlock()
			}
		}()
	}
	wg.Wait()
	fmt.Printf("counter: %d, elapsed: %s\n", counter, time.Since(start))
}
