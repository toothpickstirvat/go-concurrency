// 第 3 节任务 B：用 sync.Cond + sync.Mutex 实现有界队列（生产者-消费者），不用 channel。
// 1 个生产者放入 1–20，1 个消费者取 20 次，队列容量 3。
//
// 运行：
//
//	go run -race ./03-sync-primitives/cond
//
// 用两个 Cond 分别表示两种条件：
// 生产者只在 notFull 上等，消费者只在 notEmpty 上等。
// 这样 Signal 叫醒的一定是"关心这个条件"的一方，不会出现丢失唤醒（对比 cond-single/）。
package main

import (
	"fmt"
	"sync"
)

// Queue 是用 slice 实现的有界队列，所有字段都由 mu 保护。
type Queue struct {
	mu       sync.Mutex
	notEmpty *sync.Cond // 消费者在这里等"队列不空"
	notFull  *sync.Cond // 生产者在这里等"队列不满"
	items    []int
	capacity int
}

// NewQueue 创建队列。两个 Cond 必须绑定同一把锁：条件（len(items)）由这把锁保护。
func NewQueue(capacity int) *Queue {
	q := &Queue{capacity: capacity}
	q.notEmpty = sync.NewCond(&q.mu)
	q.notFull = sync.NewCond(&q.mu)
	return q
}

// Put 放入一个元素，队列满了就等。
//
// Wait 的三步：① 释放锁 ② 挂起，等 Signal/Broadcast ③ 被唤醒后重新加锁再返回。
// 必须先释放锁，否则消费者拿不到锁去取数据，队列永远不会变"不满"。
// 必须用 for 而不是 if：被唤醒到重新拿到锁之间，条件可能又被别人改了（比如另一个生产者先放满了），
// 而且 Go 文档也不保证被唤醒时条件一定成立，所以醒来后必须重新检查。
func (q *Queue) Put(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.items) == q.capacity { // ① 满了就等
		q.notFull.Wait() // ② 释放锁 → 挂起 → 被唤醒后重新加锁
	}
	q.items = append(q.items, v) // ③ 到这里一定持有锁，而且队列不满
	q.notEmpty.Signal()          // ④ 队列里有数据了，叫醒一个在等的消费者
}

// Get 取出一个元素，队列空了就等。和 Put 对称。
func (q *Queue) Get() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.items) == 0 { // 空了就等"不空"
		q.notEmpty.Wait()
	}
	v := q.items[0]
	// 注意：items[1:] 会让底层数组前面的空间无法再用，append 时不断重新分配。
	// 学习够用；真实代码可以用环形缓冲区（固定数组 + 头尾下标）。
	q.items = q.items[1:]
	q.notFull.Signal() // 腾出了一个位置，叫醒一个在等的生产者

	return v
}

func main() {
	q := NewQueue(3)
	var wg sync.WaitGroup
	wg.Add(2)
	// 生产者：放入 1 到 20
	go func() {
		defer wg.Done()
		for i := 1; i <= 20; i++ {
			q.Put(i)
		}
	}()

	// 消费者：取 20 次，每次打印取到的数
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			fmt.Println(q.Get())
		}
	}()
	wg.Wait()
	// 两个 goroutine 都已退出，wg.Wait 之后读 items 不需要加锁（happens-before）
	fmt.Println("队列剩余:", len(q.items))
}
