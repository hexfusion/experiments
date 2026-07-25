package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
)

type arm struct {
	label     string
	parseOnce bool
	build     func(p Plugin, consumers int) ([]Binding, error)
}

func arms() []arm {
	return []arm{
		{
			label:     "native (compiled in)",
			parseOnce: true,
			build: func(p Plugin, n int) ([]Binding, error) {
				bs := make([]Binding, n)
				for i := range bs {
					bs[i] = NewNativeBinding(p)
				}
				return bs, nil
			},
		},
		{
			label:     "adapter (metadata + tokens)",
			parseOnce: true,
			build:     extProcArm(PayloadMetadata),
		},
		{
			label:     "adapter (binary, with tokens)",
			parseOnce: true,
			build:     extProcArm(PayloadMetadataBinary),
		},
		{
			label:     "adapter (metadata, no tokens)",
			parseOnce: true,
			build:     extProcArm(PayloadMetadataNoTokens),
		},
		{
			label:     "body, no echo (mode fix only)",
			parseOnce: false,
			build:     extProcArm(PayloadFullBodyNoEcho),
		},
		{
			label:     "status quo (ext_proc full body)",
			parseOnce: false,
			build:     extProcArm(PayloadFullBody),
		},
	}
}

func extProcArm(mode PayloadMode) func(Plugin, int) ([]Binding, error) {
	return func(p Plugin, n int) ([]Binding, error) {
		bs := make([]Binding, 0, n)
		for i := 0; i < n; i++ {
			b, err := NewExtProcBinding(p, mode)
			if err != nil {
				return nil, err
			}
			bs = append(bs, b)
		}
		return bs, nil
	}
}

type result struct {
	label     string
	total     time.Duration
	perTurn   time.Duration
	wireKB    int64
	allocMB   uint64
}

func main() {
	turns := flag.Int("turns", 40, "conversation turns")
	consumers := flag.Int("consumers", 3, "body consumers in the chain")
	endpoints := flag.Int("endpoints", 100, "candidate endpoints the scorer ranks")
	repeat := flag.Int("repeat", 3, "sessions per arm")
	useEnvoy := flag.Bool("envoy", false, "run the real-Envoy arms under concurrency")
	concurrency := flag.Int("concurrency", 16, "concurrent sessions in flight (envoy mode)")
	sessions := flag.Int("sessions", 32, "total sessions per arm (envoy mode)")
	configDir := flag.String("config-dir", "plugin-binding-bench/envoy", "dir holding envoy.yaml")
	chunkDelay := flag.Duration("chunk-delay", 0, "inter-token delay in the streamed response; non-zero forces one boundary crossing per event")
	respChunks := flag.Int("resp-chunks", 64, "SSE content chunks in the streamed response")
	cacheDepth := flag.Int("cache-depth", 50, "percent of request blocks present in the indexer; sets producer cost")
	useRender := flag.Bool("render", false, "route tokenization through a render service, as real EPP does")
	renderConc := flag.Int("render-concurrency", 8, "concurrent calls the render service admits before queueing")
	renderLat := flag.Duration("render-latency", 2*time.Millisecond, "fixed per-call render latency")
	attempts := flag.Int("attempts", 1, "upstream attempts per client request (reentrance fan-out)")
	deltaMode := flag.Bool("delta", false, "measure incremental delta extraction across a session")
	deltaSessions := flag.Int("delta-sessions", 4, "concurrent sessions in delta mode")
	flag.Parse()

	cacheDepthPct = *cacheDepth
	if *deltaMode {
		runDeltaMode(*turns, *deltaSessions, RenderConfig{
			Concurrency: *renderConc, PerCallLatency: *renderLat,
		})
		return
	}

	if *useEnvoy {
		streamCfg = DefaultStreamConfig()
		streamCfg.ChunkDelay = *chunkDelay
		streamCfg.Chunks = *respChunks
		runEnvoyMode(*turns, *endpoints, *concurrency, *sessions, *configDir)
		return
	}

	conv := DefaultConversation()
	conv.Turns = *turns
	reqs := conv.Requests()

	fmt.Printf("workload   %s\n", conv.Stats())
	fmt.Printf("chain      %d body consumers, scorer over %d endpoints\n\n", *consumers, *endpoints)

	if err := SetupExtraction(reqs[len(reqs)/2], cacheDepthPct, serversPerBlock, *endpoints); err != nil {
		fmt.Fprintln(os.Stderr, "extraction setup:", err)
		os.Exit(1)
	}
	var renderSvc *RenderService
	if *useRender {
		svc, stop, err := StartRenderService(19200, RenderConfig{
			Concurrency: *renderConc, PerCallLatency: *renderLat,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "render service:", err)
			os.Exit(1)
		}
		defer stop()
		renderSvc = svc
		globalExtractor = globalExtractor.WithRender(NewRenderClient("http://127.0.0.1:19200"))
		fmt.Printf("render     service on :19200, concurrency %d, %v per call\n", *renderConc, *renderLat)
	}
	if *attempts > 1 {
		fmt.Printf("reentrance %d upstream attempts per client request\n", *attempts)
	}
	fmt.Println()

	plugin := NewScorer("prefix-scorer", *endpoints)
	var results []result

	for _, a := range arms() {
		bindings, err := a.build(plugin, *consumers)
		if err != nil {
			fmt.Fprintln(os.Stderr, "build:", err)
			os.Exit(1)
		}
		shim := NewShim(a.parseOnce, bindings...)

		// Warm the connections so setup is not charged to the measurement.
		if _, err := shim.Handle(context.Background(), reqs[0]); err != nil {
			fmt.Fprintln(os.Stderr, "warmup:", err)
			os.Exit(1)
		}

		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		startWire := shim.WireBytes()

		var renderBefore int64
		if renderSvc != nil {
			renderBefore, _ = renderSvc.Calls()
		}

		start := time.Now()
		for r := 0; r < *repeat; r++ {
			for _, body := range reqs {
				// Reentrance: one client request becomes N upstream attempts.
				// The shim extracts once and reuses; the status quo re-extracts
				// per attempt because no one owns the bytes.
				if _, err := shim.HandleAttempts(context.Background(), body, *attempts); err != nil {
					fmt.Fprintln(os.Stderr, "handle:", err)
					os.Exit(1)
				}
			}
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)

		n := *repeat * len(reqs)
		if renderSvc != nil {
			after, _ := renderSvc.Calls()
			fmt.Printf("  %-32s %d render calls for %d client requests\n",
				a.label, (after-renderBefore) / int64(*repeat), len(reqs))
		}
		results = append(results, result{
			label:   a.label,
			total:   elapsed,
			perTurn: elapsed / time.Duration(n),
			wireKB:  (shim.WireBytes() - startWire) / int64(*repeat) >> 10,
			allocMB: (after.TotalAlloc - before.TotalAlloc) / uint64(*repeat) >> 20,
		})
		shim.Close()
	}

	fmt.Printf("%-32s %12s %12s %14s %12s\n", "arm", "per turn", "per session", "wire/session", "alloc/session")
	for _, r := range results {
		fmt.Printf("%-32s %12s %12s %11dKB %11dMB\n",
			r.label,
			fmt.Sprintf("%.2fms", float64(r.perTurn.Microseconds())/1000),
			fmt.Sprintf("%.0fms", float64(r.total.Milliseconds())/float64(*repeat)),
			r.wireKB, r.allocMB)
	}

	if len(results) >= 2 {
		base := results[0].perTurn
		fmt.Printf("\ndelta vs native:\n")
		for _, r := range results[1:] {
			fmt.Printf("  %-32s %+.2fms/turn  (%.1fx)\n",
				r.label,
				float64((r.perTurn - base).Microseconds())/1000,
				float64(r.perTurn)/float64(base))
		}
	}
}

func runEnvoyMode(turns, endpoints, concurrency, sessions int, configDir string) {
	conv := DefaultConversation()
	conv.Turns = turns

	fmt.Printf("workload   %s\n", conv.Stats())
	fmt.Printf("load       %d concurrent sessions, %d sessions per arm, scorer over %d endpoints\n",
		concurrency, sessions, endpoints)
	fmt.Printf("response   %d SSE chunks, %v inter-token delay, usage chunk %v\n", streamCfg.Chunks, streamCfg.ChunkDelay, streamCfg.IncludeUsage)
	fmt.Printf("path       real Envoy %s on the host network\n\n", envoyImage)

	results, err := RunEnvoyArms(context.Background(), conv, endpoints, concurrency, sessions, configDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Printf("%-40s %9s %9s %9s %10s %10s %8s %7s\n",
		"arm", "p50", "p90", "p99", "ttft p50", "ttft p99", "rps", "errors")
	for _, r := range results {
		fmt.Printf("%-40s %9s %9s %9s %10s %10s %8.0f %7d\n",
			r.Label, msOf(r.P50), msOf(r.P90), msOf(r.P99),
			msOf(r.TTFTP50), msOf(r.TTFTP99), r.RPS, r.Errors)
	}

	if len(results) >= 2 {
		sq := results[1]
		fmt.Printf("\nvs status quo (p99):\n")
		for _, r := range results {
			if r.Label == sq.Label {
				continue
			}
			fmt.Printf("  %-40s %.2fx\n", r.Label, float64(r.P99)/float64(sq.P99))
		}
	}
}

func msOf(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}
