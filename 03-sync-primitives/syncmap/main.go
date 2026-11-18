// 第 3 节任务 C：三种并发安全 map 的性能对比，Mutex+map、RWMutex+map、sync.Map。
// 两个场景：写多（100 个 goroutine 各写 100 个新 key）、读多（预写 1000 个 key，100 个 goroutine 各读 1 万次）。
//
// 运行（先用 -race 查正确性，测耗时不要带 -race，多跑几次看波动）：
//
//	go run -race ./03-sync-primitives/syncmap
//	go run ./03-sync-primitives/syncmap
//
// 实测（4 核，Go 1.27）：读多场景 sync.Map 约 5–12ms，Mutex 约 25–30ms，RWMutex 约 37–43ms；
// 写多场景三者都在 1–5ms。临界区很短时 RWMutex 的读者计数本身成为竞争点，反而比 Mutex 慢。
package main

import (
	"fmt"
	"sync"
	"time"
)

// Store 是三种实现共用的接口，测试函数只依赖它，一份测试代码就能跑三种实现。
type Store interface {
	Set(key string, val int)
	Get(key string) (int, bool)
}

// Map1：sync.Mutex + map，读写都用 Lock。
type Map1 struct {
	mu sync.Mutex
	m  map[string]int
}

// NewMap1 负责初始化 map：nil map 可以读，但写入会 panic。
func NewMap1() *Map1 {
	return &Map1{m: make(map[string]int)}
}

// Set 和 Get 自己管理加锁解锁，调用方不用接触 mu。
func (s *Map1) Set(key string, val int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = val
}

func (s *Map1) Get(key string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}

// Map2：sync.RWMutex + map，第二种组合。
type Map2 struct {
	mu sync.RWMutex
	m  map[string]int
}

func NewMap2() *Map2 {
	return &Map2{m: make(map[string]int)}
}

func (s *Map2) Set(key string, val int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = val
}

func (s *Map2) Get(key string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	return v, ok
}

// Map3：sync.Map，第三种组合。
type Map3 struct {
	m sync.Map
}

func NewMap3() *Map3 {
	return &Map3{}
}

func (s *Map3) Set(key string, val int) {
	s.m.Store(key, val)
}

func (s *Map3) Get(key string) (int, bool) {
	v, ok := s.m.Load(key)
	if !ok {
		return 0, false
	}
	return v.(int), true
}

// genKeys 生成 n 个 key。make 的第二个参数是长度、第三个是容量；
// 长度为 0 时不能用下标赋值，所以这里长度直接设成 n。
func genKeys(id int, n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("%d-%03d", id, i)
	}
	return keys
}

// benchWrite 场景 1（写多）：100 个 goroutine，每个写入 100 个不同的新 key。
// keys 在计时前生成好，避免把 Sprintf 的耗时算进去。
func benchWrite(s Store, keys [][]string) time.Duration {
	start := time.Now()
	wg := sync.WaitGroup{}
	for i := 0; i < len(keys); i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j, k := range keys[id] {
				s.Set(k, j)
			}
		}(i)
	}
	wg.Wait()
	return time.Since(start)
}

// benchRead 场景 2（读多）：100 个 goroutine 各读 10000 次，key 在 keys 里轮流取。
// 调用前 keys 已经全部写入 s。
func benchRead(s Store, keys []string) time.Duration {
	start := time.Now()
	wg := sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				s.Get(keys[j%len(keys)]) // j 到 9999，取模映射到 0–999，避免越界
			}
		}()
	}
	wg.Wait()
	return time.Since(start)
}

func main() {
	// 计时前生成所有 key
	writeKeys := make([][]string, 100)
	for i := range writeKeys {
		writeKeys[i] = genKeys(i, 100)
	}
	readKeys := genKeys(0, 1000)

	// NewMap1 等返回的是具体类型指针，不是 Store，所以包一层匿名函数才能放进同一个切片。
	impls := []struct {
		name string
		new  func() Store
	}{
		{"Mutex+map", func() Store { return NewMap1() }},
		{"RWMutex+map", func() Store { return NewMap2() }},
		{"sync.Map", func() Store { return NewMap3() }},
	}

	for _, impl := range impls {
		// 场景 1：写多，每次用一个新建的空 map
		writeDur := benchWrite(impl.new(), writeKeys)

		// 场景 2：读多，先单线程写入 1000 个 key（不计时）
		m := impl.new()
		for i, k := range readKeys {
			m.Set(k, i)
		}
		readDur := benchRead(m, readKeys)

		fmt.Printf("%-16s 写多: %-12v 读多: %v\n", impl.name, writeDur, readDur)
	}
}
