package router

import (
	"errors"
	"math"
	"sync/atomic"

	"github.com/avidubey/kv-gateway/internal/registry"
)

var ErrNoWorkers = errors.New("no healthy workers available")

type Policy interface {
	Pick(workers []*registry.WorkerState, estTokens int) (*registry.WorkerState, error)
}

// RoundRobin rotates through healthy workers regardless of state.
type RoundRobin struct {
	counter atomic.Uint64
}

func (rr *RoundRobin) Pick(workers []*registry.WorkerState, _ int) (*registry.WorkerState, error) {
	if len(workers) == 0 {
		return nil, ErrNoWorkers
	}
	idx := rr.counter.Add(1) % uint64(len(workers))
	return workers[idx], nil
}

// LeastLoaded picks the worker with the fewest active requests.
type LeastLoaded struct{}

func (ll *LeastLoaded) Pick(workers []*registry.WorkerState, _ int) (*registry.WorkerState, error) {
	if len(workers) == 0 {
		return nil, ErrNoWorkers
	}
	best := workers[0]
	for _, w := range workers[1:] {
		if w.ActiveRequests < best.ActiveRequests {
			best = w
		}
	}
	return best, nil
}

// KVAware scores workers by projected KV-cache occupancy and load.
// Simplified from NVIDIA Dynamo's cost function (overlap_weight * prefill_blocks + decode_blocks).
// We use: score = w1*projected_occupancy + w2*(active_requests/maxConcurrent).
type KVAware struct {
	W1            float64
	W2            float64
	MaxConcurrent float64
}

func NewKVAware() *KVAware {
	return &KVAware{W1: 1.0, W2: 0.3, MaxConcurrent: 32}
}

func (kv *KVAware) Pick(workers []*registry.WorkerState, estTokens int) (*registry.WorkerState, error) {
	if len(workers) == 0 {
		return nil, ErrNoWorkers
	}

	var best *registry.WorkerState
	bestScore := math.MaxFloat64

	for _, w := range workers {
		if w.KVCapacity == 0 {
			continue
		}
		projected := float64(w.KVUsed+int64(estTokens)) / float64(w.KVCapacity)
		if projected > 0.95 {
			continue
		}
		loadFrac := float64(w.ActiveRequests) / kv.MaxConcurrent
		score := kv.W1*projected + kv.W2*loadFrac
		if score < bestScore {
			bestScore = score
			best = w
		}
	}

	if best != nil {
		return best, nil
	}

	// All workers over 0.95 — pick the least-bad
	for _, w := range workers {
		if w.KVCapacity == 0 {
			continue
		}
		projected := float64(w.KVUsed+int64(estTokens)) / float64(w.KVCapacity)
		if projected < bestScore {
			bestScore = projected
			best = w
		}
	}
	if best != nil {
		return best, nil
	}
	return nil, ErrNoWorkers
}

func NewPolicy(name string) Policy {
	switch name {
	case "round-robin":
		return &RoundRobin{}
	case "least-loaded":
		return &LeastLoaded{}
	case "kv-aware":
		return NewKVAware()
	default:
		return &RoundRobin{}
	}
}
