// 三种常见的死锁演示。每次只在 main 里打开一个，其余注释掉。
// 运行：timeout 5 go run ./01-mutex/deadlock
// 预期：程序打印到"卡住"之前的那一行，然后报
// fatal error: all goroutines are asleep - deadlock!
// 报错信息里每个 goroutine 后面的方括号说明它在等什么，例如
// [sync.Mutex.Lock] 表示在等锁，[chan receive] 表示在等 channel。
package main

import (
	"fmt"
	"sync"
	"time"
)

// relock 演示重复加锁：Go 的 Mutex 不可重入，
// 同一个 goroutine 已经持有锁，再次 Lock 会等自己释放，永远等不到。
func relock() {
	m := sync.Mutex{}
	fmt.Println("relock: 第一次 Lock")
	m.Lock()
	fmt.Println("relock: 第一次 Lock 成功，准备第二次 Lock")
	m.Lock() // 卡在这里：锁被自己持有
	fmt.Println("relock: 第二次 Lock 成功（不会打印）")
	m.Unlock()
}

// lockAndChan 演示锁和 channel 互相等待：
//   - main goroutine 持有锁，等着从 ch 读数据；
//   - 子 goroutine 要先拿到锁，才会往 ch 写数据。
//
// 双方都在等对方先动，形成循环等待。
func lockAndChan() {
	m := sync.Mutex{}
	ch := make(chan int)
	m.Lock()
	fmt.Println("lockAndChan: main 拿到锁")
	go func() {
		fmt.Println("lockAndChan: 子 goroutine 准备 Lock")
		m.Lock() // 卡在这里：锁被 main 持有
		fmt.Println("lockAndChan: 子 goroutine 拿到锁（不会打印）")
		ch <- 1
	}()
	fmt.Println("lockAndChan: main 等待 ch")
	<-ch // 卡在这里：没人往 ch 写
	fmt.Println("lockAndChan: main 收到数据（不会打印）")
}

// upgrade 演示读锁升级成写锁：
// Lock 要等所有读锁释放，而这个读锁正是自己持有的，所以永远等不到。
// 正确做法：先 RUnlock，再 Lock，拿到写锁后重新检查条件。
func upgrade() {
	rwm := sync.RWMutex{}
	rwm.RLock()
	fmt.Println("upgrade: 拿到读锁，准备升级成写锁")
	rwm.Lock() // 卡在这里：等自己持有的读锁释放
	fmt.Println("upgrade: 拿到写锁（不会打印）")
}

// startHeartbeat 启动一个后台 goroutine，每秒打印一次。
// 只要它还活着，就不满足"所有 goroutine 都在等待"这个条件，
// 运行时不会报 deadlock，程序只会一直卡住、不停地打印心跳。
// 这和真实服务里的情况一样：总有后台 goroutine 在跑，死锁表现为"不动了"而不是崩溃。
func startHeartbeat() {
	go func() {
		start := time.Now()
		for {
			time.Sleep(time.Second)
			fmt.Printf("心跳：已运行 %v\n", time.Since(start).Round(time.Second))
		}
	}()
}

func main() {
	// 打开这一行：死锁不再被检测到，程序一直卡住；注释掉：立刻报 deadlock
	startHeartbeat()

	//relock()
	//lockAndChan()
	upgrade()
}
