package network

import (
	"time"
)

// Rate is a throughput sample in bytes per second.
type Rate struct {
	At   time.Time
	Down float64
	Up   float64
}

// Meter converts cumulative counters into rates and keeps a bounded history.
// It is not safe for concurrent use; the caller owns it.
type Meter struct {
	capacity int
	history  []Rate // ring buffer, oldest first after Samples()
	start    int
	prev     Counters
	prevAt   time.Time
	havePrev bool
	// Totals since the meter was (re)started.
	TotalDown, TotalUp uint64
}

// NewMeter returns a meter keeping capacity samples.
func NewMeter(capacity int) *Meter {
	if capacity < 2 {
		capacity = 2
	}
	return &Meter{capacity: capacity, history: make([]Rate, 0, capacity)}
}

// Reset clears history, e.g. when the VPN interface changes.
func (m *Meter) Reset() {
	m.history = m.history[:0]
	m.start = 0
	m.havePrev = false
	m.TotalDown, m.TotalUp = 0, 0
}

// Add records a counter reading taken at t and returns the derived rate.
// The first reading only primes the meter. A counter that goes backwards
// (interface recreated) is treated as a reset and yields a zero rate.
func (m *Meter) Add(c Counters, t time.Time) (Rate, bool) {
	if !m.havePrev {
		m.prev, m.prevAt, m.havePrev = c, t, true
		return Rate{}, false
	}
	dt := t.Sub(m.prevAt).Seconds()
	if dt <= 0 {
		return Rate{}, false
	}
	r := Rate{At: t}
	if c.RxBytes >= m.prev.RxBytes && c.TxBytes >= m.prev.TxBytes {
		dRx := c.RxBytes - m.prev.RxBytes
		dTx := c.TxBytes - m.prev.TxBytes
		r.Down = float64(dRx) / dt
		r.Up = float64(dTx) / dt
		m.TotalDown += dRx
		m.TotalUp += dTx
	}
	m.prev, m.prevAt = c, t
	m.push(r)
	return r, true
}

func (m *Meter) push(r Rate) {
	if len(m.history) < m.capacity {
		m.history = append(m.history, r)
		return
	}
	m.history[m.start] = r
	m.start = (m.start + 1) % m.capacity
}

// Samples returns the history, oldest first.
func (m *Meter) Samples() []Rate {
	out := make([]Rate, 0, len(m.history))
	out = append(out, m.history[m.start:]...)
	out = append(out, m.history[:m.start]...)
	return out
}

// Last returns the most recent sample.
func (m *Meter) Last() (Rate, bool) {
	if len(m.history) == 0 {
		return Rate{}, false
	}
	idx := (m.start + len(m.history) - 1) % len(m.history)
	return m.history[idx], true
}

// Peak returns the highest down and up rate in the history.
func (m *Meter) Peak() (down, up float64) {
	for _, r := range m.history {
		down = max(down, r.Down)
		up = max(up, r.Up)
	}
	return down, up
}

// Capacity returns the history size.
func (m *Meter) Capacity() int { return m.capacity }
