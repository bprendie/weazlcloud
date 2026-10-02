package photos

type jobEntry struct {
	pos       int
	job       MediaJob
	queue     *jobHeap
	heapIndex int
}

type jobHeap struct {
	kind  int
	items []*jobEntry
}

func (h jobHeap) Len() int { return len(h.items) }
func (h jobHeap) Less(i, j int) bool {
	a, b := h.items[i].job, h.items[j].job
	if h.kind == 2 && !a.LeaseUntil.Equal(b.LeaseUntil) {
		return a.LeaseUntil.Before(b.LeaseUntil)
	}
	if h.kind == 0 && a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if !a.NextAttemptAt.Equal(b.NextAttemptAt) {
		return a.NextAttemptAt.Before(b.NextAttemptAt)
	}
	return a.ID < b.ID
}
func (h jobHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].heapIndex = i
	h.items[j].heapIndex = j
}
func (h *jobHeap) Push(value any) {
	e := value.(*jobEntry)
	e.heapIndex = len(h.items)
	h.items = append(h.items, e)
}
func (h *jobHeap) Pop() any {
	n := len(h.items) - 1
	e := h.items[n]
	h.items[n] = nil
	h.items = h.items[:n]
	e.heapIndex = -1
	return e
}
