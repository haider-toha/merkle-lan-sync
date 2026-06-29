package reconcile

import (
	"context"
	"time"
)

// debouncer coalesces a burst of raw watcher events for one path into a single
// "settled" emission once that path has been quiet for `window` (GR-10, ~150 ms): an
// editor's save that fires many Write events becomes exactly one hash/diff, never a
// partial-file hash on the first of N writes. It mirrors the discovery registry's
// clock/tick-injection shape so eviction-style timing is provable without a naked
// time.Sleep (GR-13): production wires a real clock + ticker; tests inject a manual
// clock + tick channel.
type debouncer struct {
	window time.Duration
	in     <-chan string // raw event paths (canonical keys)
	emit   func(string)  // called once per path after it goes quiet

	now  func() time.Time
	tick <-chan time.Time // sweep ticks; nil ⇒ a real ticker at window/2
}

// run owns the pending map exclusively (a GR-4 actor — not lock-guarded), so the
// debounce state never races the engine's RWMutex-guarded core. It exits on ctx.
func (d *debouncer) run(ctx context.Context) {
	pending := make(map[string]time.Time)

	tick := d.tick
	var ticker *time.Ticker
	if tick == nil {
		every := d.window / 2
		if every <= 0 {
			every = time.Millisecond
		}
		ticker = time.NewTicker(every)
		defer ticker.Stop()
		tick = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case p := <-d.in:
			pending[p] = d.now()
		case <-tick:
			now := d.now()
			for p, last := range pending {
				if now.Sub(last) >= d.window {
					d.emit(p)
					delete(pending, p)
				}
			}
		}
	}
}
