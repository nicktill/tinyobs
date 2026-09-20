package series

import (
	"fmt"
	"sync"
)

// ID identifies a series within a TinyObs instance.
//
// It is derived from the label hash but is not equal to it: on the rare
// occasion that two distinct label sets share a hash, the second one is given a
// neighbouring free ID. Storage keys embed the ID, so the guarantee callers need
// is that one ID means exactly one label set, forever.
type ID uint64

type errCorrupt string

func (e errCorrupt) Error() string { return "series: corrupt encoding: " + string(e) }

// Index maps label sets to stable IDs and back.
//
// It exists because the previous storage engine wrote a full copy of the label
// set alongside every sample. The key already carried a series hash, so those
// bytes bought nothing. Interning label sets here means a series pays for its
// identity once rather than once per data point, which together with chunk
// encoding is what takes a sample from 55.6 bytes on disk to low single digits.
type Index struct {
	mu sync.RWMutex

	// byID is the authoritative store: every live series, keyed by its ID.
	byID map[ID]Labels

	// byHash maps a label hash to the IDs that share it. Almost always one
	// entry; more than one only under collision.
	byHash map[uint64][]ID
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{
		byID:   make(map[ID]Labels),
		byHash: make(map[uint64][]ID),
	}
}

// GetOrCreate returns the ID for a label set, assigning one if it is new.
// The returned bool reports whether the series was newly created.
//
// Labels are copied before being retained, so callers may reuse their slice.
func (ix *Index) GetOrCreate(ls Labels) (ID, bool) {
	hash := ls.Hash()

	// Fast path: an existing series, under a read lock.
	ix.mu.RLock()
	for _, id := range ix.byHash[hash] {
		if ix.byID[id].Equal(ls) {
			ix.mu.RUnlock()
			return id, false
		}
	}
	ix.mu.RUnlock()

	ix.mu.Lock()
	defer ix.mu.Unlock()

	// Re-check: another goroutine may have created it between the two locks.
	for _, id := range ix.byHash[hash] {
		if ix.byID[id].Equal(ls) {
			return id, false
		}
	}

	stored := make(Labels, len(ls))
	copy(stored, ls)

	id := ix.allocateLocked(hash)
	ix.byID[id] = stored
	ix.byHash[hash] = append(ix.byHash[hash], id)
	return id, true
}

// allocateLocked picks an unused ID, starting from the label hash.
//
// Using the hash as the ID keeps storage keys well distributed and means the ID
// is reproducible across restarts for the overwhelming majority of series. On
// collision we walk forward to the next free slot. With 64-bit hashes a
// collision needs roughly 5 billion series before it is even likely, so this
// path is effectively dead code, which is exactly why it is worth writing and
// testing rather than asserting it cannot happen: silently merging two series
// produces a graph that is wrong in a way nobody can debug from the outside.
func (ix *Index) allocateLocked(hash uint64) ID {
	id := ID(hash)
	for {
		if _, taken := ix.byID[id]; !taken {
			return id
		}
		id++
	}
}

// Get returns the ID for a label set if it is already known.
func (ix *Index) Get(ls Labels) (ID, bool) {
	hash := ls.Hash()

	ix.mu.RLock()
	defer ix.mu.RUnlock()

	for _, id := range ix.byHash[hash] {
		if ix.byID[id].Equal(ls) {
			return id, true
		}
	}
	return 0, false
}

// Labels returns the label set for an ID.
func (ix *Index) Labels(id ID) (Labels, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	ls, ok := ix.byID[id]
	return ls, ok
}

// Len returns the number of live series.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.byID)
}

// Delete removes a series. It is a no-op if the ID is unknown.
func (ix *Index) Delete(id ID) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	ls, ok := ix.byID[id]
	if !ok {
		return
	}
	delete(ix.byID, id)

	hash := ls.Hash()
	ids := ix.byHash[hash]
	for i, candidate := range ids {
		if candidate == id {
			ix.byHash[hash] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	if len(ix.byHash[hash]) == 0 {
		delete(ix.byHash, hash)
	}
}

// ForEach calls fn for every series. It holds a read lock throughout, so fn
// must not call back into the index.
func (ix *Index) ForEach(fn func(ID, Labels) bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	for id, ls := range ix.byID {
		if !fn(id, ls) {
			return
		}
	}
}

// Matches reports whether a label set satisfies every required pair. An empty
// matcher set matches everything.
func Matches(ls Labels, required Labels) bool {
	for _, want := range required {
		got, ok := ls.Get(want.Name)
		if !ok || got != want.Value {
			return false
		}
	}
	return true
}

// Entry pairs an ID with its labels, for bulk load and persistence.
type Entry struct {
	ID     ID
	Labels Labels
}

// Snapshot returns every entry, for writing the index to durable storage.
func (ix *Index) Snapshot() []Entry {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	out := make([]Entry, 0, len(ix.byID))
	for id, ls := range ix.byID {
		out = append(out, Entry{ID: id, Labels: ls})
	}
	return out
}

// Load restores entries produced by Snapshot, rebuilding the hash buckets.
//
// IDs are taken as given rather than recomputed, because storage keys already
// reference them. An ID that appears twice means the persisted index is
// inconsistent, and continuing would silently drop a series.
func (ix *Index) Load(entries []Entry) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	for _, e := range entries {
		if existing, ok := ix.byID[e.ID]; ok {
			return fmt.Errorf("series: duplicate id %d for %s and %s", e.ID, existing, e.Labels)
		}
		ls := make(Labels, len(e.Labels))
		copy(ls, e.Labels)
		ls.Sort()

		ix.byID[e.ID] = ls
		hash := ls.Hash()
		ix.byHash[hash] = append(ix.byHash[hash], e.ID)
	}
	return nil
}
