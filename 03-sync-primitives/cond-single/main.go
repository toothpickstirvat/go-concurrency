// 第 3 节加分实验：只用一个 Cond，2 个生产者 + 2 个消费者，演示 Signal 的"丢失唤醒"。
//
// 运行（反复跑才容易遇到，测死锁不要带 -race）：
//
//	go build -o /tmp/cond-single ./03-sync-primitives/cond-single
//	for i in $(seq 200); do timeout 5 /tmp/cond-single >/dev/null || echo "第 $i 次卡住"; done
//
// 实测（容量 1，跑 200 次）：
//
//	Signal     49/200 次死锁（all goroutines are asleep）
//	Broadcast  0/200 次
//
// 死锁过程（容量 1，生产者 P1、P2，消费者 C1、C2，都在同一个 cond 上等）：
//  1. 队列满，P1、P2 都在 Wait（等"不满"）
//  2. C1 取走一个，Signal 叫醒了 P1；C1 接着再取，队列空了，C1 Wait（等"不空"）
//  3. C2 也来取，队列空，C2 Wait
//  4. P1 放入一个，Signal——本该叫醒消费者，却叫醒了 P2
//  5. P2 醒来发现队列是满的，又回去 Wait。这次唤醒被"浪费"了
//  6. 现在 P1 下一次 Put 发现队列满也 Wait：P1、P2、C1、C2 全在等，没人再 Signal → 死锁
//
// 根本原因：生产者和消费者等的是两种不同的条件，却挂在同一个等待队列上，
// Signal 只叫醒一个，叫醒的可能是"条件对它没变化"的同类。
// 修法：① 改用 Broadcast，全部叫醒、各自重新检查条件（本文件现在的写法，代价是无谓的唤醒）；
// ② 用两个 Cond（notFull / notEmpty）分开等待，Signal 就够了（cond/ 的写法，推荐）。
package main

import (
	"fmt"
	"sync"
)

type Queue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	items    []int
	capacity int
}

// NewQueue 创建队列。生产者和消费者共用这一个 cond。
func NewQueue(capacity int) *Queue {
	q := &Queue{capacity: capacity}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *Queue) Put(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.items) == q.capacity { // ① 满了就等
		q.cond.Wait() // ② 释放锁 → 挂起 → 被唤醒后重新加锁
	}
	q.items = append(q.items, v) // ③ 到这里一定持有锁，而且队列不满
	// ④ 用 Signal 会丢失唤醒：它可能叫醒另一个生产者而不是消费者
	//q.cond.Signal()
	q.cond.Broadcast() // 全部叫醒，各自在 for 循环里重新检查条件

}

func (q *Queue) Get() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.items) == 0 {
		q.cond.Wait()
	}
	v := q.items[0]
	q.items = q.items[1:]
	// 同理，Signal 可能叫醒另一个消费者而不是生产者
	//q.cond.Signal()
	q.cond.Broadcast()
	return v
}

func main() {
	q := NewQueue(1) // 容量 1 时队列最容易在"满"和"空"之间切换，最容易复现丢失唤醒
	var wg sync.WaitGroup
	wg.Add(4) // 4 个 goroutine 就 Add(4)；写成 Add(2) 会提前退出或 Done 过多 panic
	// 生产者1
	go func() {
		defer wg.Done()
		for i := 1; i <= 10; i++ {
			q.Put(i)
		}
	}()
	// 生产者2
	go func() {
		defer wg.Done()
		for i := 11; i <= 20; i++ {
			q.Put(i)
		}
	}()
	// 消费者1
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			fmt.Println(q.Get())
		}
	}()
	// 消费者2
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			fmt.Println(q.Get())
		}
	}()
	wg.Wait()
	fmt.Println("队列剩余:", len(q.items))
}
