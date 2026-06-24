package registry

import (
	"log"
	"sync"
	"time"
)

type Status int

const (
	Alive Status = iota
	Dead
)

type WorkerState struct {
	ID              string
	GRPCAddr        string
	KVUsed          int64
	KVCapacity      int64
	ActiveRequests  int32
	TokensPerSec    float64
	LastBeat        time.Time
	Status          Status
}

type Registry struct {
	mu      sync.RWMutex
	workers map[string]*WorkerState
}

func New() *Registry {
	r := &Registry{workers: make(map[string]*WorkerState)}
	go r.janitor()
	return r
}

func (r *Registry) Upsert(id, addr string, kvUsed, kvCap int64, activeReqs int32, tps float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		w = &WorkerState{ID: id, GRPCAddr: addr}
		r.workers[id] = w
		log.Printf("registry: new worker %s at %s", id, addr)
	}
	w.KVUsed = kvUsed
	w.KVCapacity = kvCap
	w.ActiveRequests = activeReqs
	w.TokensPerSec = tps
	w.LastBeat = time.Now()
	w.Status = Alive
}

func (r *Registry) Healthy() []*WorkerState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*WorkerState
	for _, w := range r.workers {
		if w.Status == Alive {
			out = append(out, w)
		}
	}
	return out
}

func (r *Registry) HealthyExcluding(excludeIDs map[string]bool) []*WorkerState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*WorkerState
	for _, w := range r.workers {
		if w.Status == Alive && !excludeIDs[w.ID] {
			out = append(out, w)
		}
	}
	return out
}

func (r *Registry) Get(id string) *WorkerState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workers[id]
}

func (r *Registry) MarkDead(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[id]; ok {
		w.Status = Dead
		log.Printf("registry: worker %s marked dead", id)
	}
}

func (r *Registry) janitor() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		r.mu.Lock()
		for _, w := range r.workers {
			if w.Status == Alive && time.Since(w.LastBeat) > 300*time.Millisecond {
				w.Status = Dead
				log.Printf("registry: worker %s timed out (last beat %v ago)", w.ID, time.Since(w.LastBeat))
			}
		}
		r.mu.Unlock()
	}
}
