package app

import (
	"sync"
	"time"
)

const liveUpdateInterval = 100 * time.Millisecond

func appendTail[T any](items, more []T, limit int) []T {
	if len(more) >= limit {
		items, more = items[:0], more[len(more)-limit:]
	} else if drop := len(items) + len(more) - limit; drop > 0 {
		copy(items, items[drop:])
		items = items[:len(items)-drop]
	}
	return append(items, more...)
}

// Stop an overloaded live feed instead of spending an entire CPU core reading
// and discarding messages. Closing it also releases the server's output buffer.
type liveBudget struct {
	start        time.Time
	count, bytes int
}

func (b *liveBudget) allow(size int, now time.Time) bool {
	if b.start.IsZero() || now.Sub(b.start) >= time.Second {
		b.start, b.count, b.bytes = now, 0, 0
	}
	b.count++
	b.bytes += size
	return b.count <= 4096 && b.bytes <= 4<<20
}

// A bounded tail of live events, with at most one pending UI callback.
// Producers never wait for the UI and never create a callback per message.
type eventQueue[T any] struct {
	mu        sync.Mutex
	items     []T
	limit     int
	next      int
	dropped   int
	scheduled bool
	closed    bool
	timer     *time.Timer
	post      func(func())
	apply     func([]T, int)
}

func (q *eventQueue[T]) push(item T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if len(q.items) < q.limit {
		q.items = append(q.items, item)
	} else {
		q.items[q.next] = item
		q.next = (q.next + 1) % q.limit
		q.dropped++
	}
	if !q.scheduled {
		q.scheduled = true
		q.timer = time.AfterFunc(liveUpdateInterval, q.flush)
	}
}

func (q *eventQueue[T]) flush() {
	q.post(func() {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return
		}
		items := append(append([]T(nil), q.items[q.next:]...), q.items[:q.next]...)
		dropped := q.dropped
		q.items, q.next, q.dropped = nil, 0, 0
		q.mu.Unlock()
		q.apply(items, dropped)
		q.mu.Lock()
		defer q.mu.Unlock()
		if !q.closed && len(q.items) > 0 {
			q.timer = time.AfterFunc(liveUpdateInterval, q.flush)
		} else {
			q.scheduled = false
		}
	})
}

func (q *eventQueue[T]) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.items = nil
	if q.timer != nil {
		q.timer.Stop()
	}
}
