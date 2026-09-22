package main

import (
	"context"
	"fmt"
	"time"
)

// RunDelta measures incremental delta extraction against full extraction, over
// a multi-turn session, across a range of replica counts.
//
// Full extraction renders the whole history every turn, so a T-turn session
// costs O(T^2) of render work. Delta renders only what a replica has not seen.
func RunDelta(turns, replicas int, affinity bool, renderCfg RenderConfig, sessions int) (time.Duration, int64, int64, int64, int64, error) {
	svc, stop, err := StartRenderService(19201, renderCfg)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	defer stop()

	conv := DefaultConversation()
	conv.Turns = turns
	reqs := conv.Requests()

	mk := func() *Extractor {
		return NewExtractor(nil).WithRender(NewRenderClient("http://127.0.0.1:19201"))
	}
	rs := NewReplicaSet(replicas, affinity, mk)

	start := time.Now()
	for s := 0; s < sessions; s++ {
		sid := "sess-" + itoa(s)
		for turn, body := range reqs {
			if _, err := rs.Extract(context.Background(), sid, turn, body); err != nil {
				return 0, 0, 0, 0, 0, err
			}
		}
	}
	elapsed := time.Since(start)
	calls, bytes := svc.Calls()
	hits, misses := rs.Stats()
	return elapsed, calls, bytes, hits, misses, nil
}

func runDeltaMode(turns, sessions int, renderCfg RenderConfig) {
	conv := DefaultConversation()
	conv.Turns = turns
	fmt.Printf("workload   %s\n", conv.Stats())
	fmt.Printf("render     concurrency %d, %v per call\n", renderCfg.Concurrency, renderCfg.PerCallLatency)
	fmt.Printf("sessions   %d\n\n", sessions)

	// Baseline: no delta at all, whole history rendered every turn.
	base, baseCalls, baseBytes, _, _, err := RunDeltaFull(turns, renderCfg, sessions)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%-34s %10s %10s %12s %10s\n", "arm", "total", "calls", "render MB", "delta hit")
	fmt.Printf("%-34s %10s %10d %11.1f %10s\n",
		"full extract every turn", ms(base), baseCalls, float64(baseBytes)/(1<<20), "n/a")

	for _, r := range []int{1, 2, 4, 8} {
		for _, aff := range []bool{true, false} {
			if r == 1 && !aff {
				continue
			}
			el, calls, bytes, hits, misses, err := RunDelta(turns, r, aff, renderCfg, sessions)
			if err != nil {
				fmt.Println("error:", err)
				return
			}
			label := "delta, " + itoa(r) + " replica"
			if r > 1 {
				label += "s"
				if aff {
					label += ", affinity"
				} else {
					label += ", spread"
				}
			}
			rate := 0.0
			if hits+misses > 0 {
				rate = 100 * float64(hits) / float64(hits+misses)
			}
			fmt.Printf("%-34s %10s %10d %11.1f %9.0f%%\n",
				label, ms(el), calls, float64(bytes)/(1<<20), rate)
		}
	}
}

// RunDeltaFull is the control: every turn renders the whole history.
func RunDeltaFull(turns int, renderCfg RenderConfig, sessions int) (time.Duration, int64, int64, int64, int64, error) {
	svc, stop, err := StartRenderService(19202, renderCfg)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	defer stop()

	conv := DefaultConversation()
	conv.Turns = turns
	reqs := conv.Requests()
	e := NewExtractor(nil).WithRender(NewRenderClient("http://127.0.0.1:19202"))

	start := time.Now()
	for s := 0; s < sessions; s++ {
		for _, body := range reqs {
			if _, err := e.ExtractCtx(context.Background(), body); err != nil {
				return 0, 0, 0, 0, 0, err
			}
		}
	}
	elapsed := time.Since(start)
	calls, bytes := svc.Calls()
	return elapsed, calls, bytes, 0, 0, nil
}

func ms(d time.Duration) string { return fmt.Sprintf("%.0fms", float64(d.Milliseconds())) }
