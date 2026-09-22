package main

import (
	"context"
	"time"
)

// Run wires client, policy service, and upstream in one process and returns the
// measurement, the policy service's body high-water mark, and the shared timeline.
func Run(ctx context.Context, ccfg ClientConfig, ucfg UpstreamConfig, h1 bool, delayFirstWrite time.Duration) (*Result, int64, *Timeline, error) {
	tl := NewTimeline()

	start := StartH2C
	if h1 {
		start = StartH1
	}

	upstreamAddr, stopUpstream, err := start(UpstreamHandler(ucfg, tl))
	if err != nil {
		return nil, 0, tl, err
	}
	defer stopUpstream()

	pcfg := DefaultPolicyConfig(upstreamAddr)
	pcfg.DelayFirstWrite = delayFirstWrite
	pcfg.H1 = h1
	policy := NewPolicy(pcfg)

	policyAddr, stopPolicy, err := start(policy.Handler(tl))
	if err != nil {
		return nil, 0, tl, err
	}
	defer stopPolicy()

	res, err := RunClient(ctx, policyAddr, ccfg, tl)
	if err != nil {
		return nil, policy.HighWater(), tl, err
	}
	return res, policy.HighWater(), tl, nil
}
