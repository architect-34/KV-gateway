package sim

import (
	"math"
	"sync"
	"time"
)

const (
	// Base prefill rate: α * promptTokens at occupancy=0 ≈ 20ms for 50 tokens.
	// α = 0.4ms/token
	Alpha = 0.4e-3

	// Base per-token rate at occupancy=0 ≈ 10ms.
	Beta = 10e-3

	// Scaling coefficients so the ^2/^3 exponents produce the right magnitude:
	//   At occ=0.9: 1 + 17.3 * 0.81 = ~15x → 20ms * 15 = 300ms TTFT
	//   At occ=0.9: 1 + 9.6 * 0.729 = ~8x → 10ms * 8 = 80ms/token
	KPrefill = 17.3
	KToken   = 9.6
)

type KVCache struct {
	mu       sync.Mutex
	used     int64
	capacity int64
}

func NewKVCache(capacity int64) *KVCache {
	return &KVCache{capacity: capacity}
}

func (kv *KVCache) Reserve(tokens int64) (occupancy float64, ok bool) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if kv.used+tokens > kv.capacity {
		return float64(kv.used) / float64(kv.capacity), false
	}
	occupancy = float64(kv.used) / float64(kv.capacity)
	kv.used += tokens
	return occupancy, true
}

func (kv *KVCache) Release(tokens int64) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.used -= tokens
	if kv.used < 0 {
		kv.used = 0
	}
}

func (kv *KVCache) Stats() (used, capacity int64) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	return kv.used, kv.capacity
}

func PrefillDelay(promptTokens int, occupancy float64) time.Duration {
	secs := Alpha * float64(promptTokens) * (1 + KPrefill*math.Pow(occupancy, 2))
	return time.Duration(secs * float64(time.Second))
}

func TokenDelay(occupancy float64) time.Duration {
	secs := Beta * (1 + KToken*math.Pow(occupancy, 3))
	return time.Duration(secs * float64(time.Second))
}

func EstimateTokens(prompt string) int {
	n := len(prompt) / 4
	if n < 1 {
		n = 1
	}
	return n
}
