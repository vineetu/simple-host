package papermetrics

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
)

var memberHashSeed = maphash.MakeSeed()

// Readers never lock or allocate. Only the first observation of a member in
// this slot copies its shard's immutable map. IDs live in memory until drain.
// Full string keys retain exact distinct counts, including hash collisions.
type hackMemberSet struct{ shards [32]hackMemberShard }
type hackMemberShard struct {
	mu     sync.Mutex
	values atomic.Pointer[map[string]struct{}]
}

func (s *hackMemberSet) add(member string) bool {
	shard := &s.shards[maphash.String(memberHashSeed, member)&31]
	if values := shard.values.Load(); values != nil {
		if _, ok := (*values)[member]; ok {
			return false
		}
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	old := shard.values.Load()
	if old != nil {
		if _, ok := (*old)[member]; ok {
			return false
		}
	}
	values := make(map[string]struct{})
	if old != nil {
		for key := range *old {
			values[key] = struct{}{}
		}
	}
	values[member] = struct{}{}
	shard.values.Store(&values)
	return true
}
func (s *hackMemberSet) contains(member string) bool {
	values := s.shards[maphash.String(memberHashSeed, member)&31].values.Load()
	if values == nil {
		return false
	}
	_, ok := (*values)[member]
	return ok
}
func (s *hackMemberSet) clear() {
	for i := range s.shards {
		s.shards[i].values.Store(nil)
	}
}
