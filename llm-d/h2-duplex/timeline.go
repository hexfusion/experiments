package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Timeline records first-occurrence timestamps for named events, relative to a
// fixed origin, so the three roles can be compared on one clock.
type Timeline struct {
	mu     sync.Mutex
	origin time.Time
	marks  map[string]time.Duration
}

func NewTimeline() *Timeline {
	return &Timeline{origin: time.Now(), marks: map[string]time.Duration{}}
}

// Mark records elapsed time for label. First write wins.
func (t *Timeline) Mark(label string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if d, ok := t.marks[label]; ok {
		return d
	}
	d := time.Since(t.origin)
	t.marks[label] = d
	return d
}

// Set overwrites label unconditionally, for last-occurrence events.
func (t *Timeline) Set(label string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := time.Since(t.origin)
	t.marks[label] = d
	return d
}

func (t *Timeline) Get(label string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.marks[label]
	return d, ok
}

func (t *Timeline) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	type row struct {
		label string
		d     time.Duration
	}
	rows := make([]row, 0, len(t.marks))
	for k, v := range t.marks {
		rows = append(rows, row{k, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].d < rows[j].d })
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "  %8.1fms  %s\n", float64(r.d.Microseconds())/1000, r.label)
	}
	return b.String()
}
