package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const envoyImage = "docker.io/envoyproxy/envoy:v1.36-latest"

// StartEnvoy runs the static config on the host network so it can reach the
// consumer and upstream servers on loopback.
func StartEnvoy(configDir string) (func(), error) {
	abs, err := filepath.Abs(configDir)
	if err != nil {
		return nil, err
	}
	name := "plugin-binding-bench-envoy"
	_ = exec.Command("podman", "rm", "-f", name).Run()

	cmd := exec.Command("podman", "run", "--rm", "--name", name,
		"--network=host",
		"--entrypoint", "/usr/local/bin/envoy",
		"-v", abs+":/cfg:ro,Z",
		envoyImage,
		"-c", "/cfg/envoy.yaml",
		"--log-level", "warn",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := func() {
		_ = exec.Command("podman", "rm", "-f", name).Run()
		_ = cmd.Wait()
	}

	// Wait for every listener before measuring anything.
	for _, port := range []int{10000, 10001, 10002, 10003} {
		if err := waitPort(port, 45*time.Second); err != nil {
			stop()
			return nil, fmt.Errorf("envoy listener %d: %w", port, err)
		}
	}
	return stop, nil
}

func waitPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort("127.0.0.1", itoa(port))
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

// RunEnvoyArms stands up the upstream, the consumers, the shim, the frontend,
// and Envoy, then drives every arm through the same concurrent load.
func RunEnvoyArms(ctx context.Context, conv Conversation, endpoints, concurrency, sessions int, configDir string) ([]LoadResult, error) {
	reqs := conv.Requests()
	plugin := NewScorer("prefix-scorer", endpoints, 64)

	stopUp, err := StartUpstream(19100)
	if err != nil {
		return nil, err
	}
	defer stopUp()

	// Three standalone consumers, each parsing the body for itself. Echo is a
	// property of the listener's processing mode, and both modes reach these
	// same servers, so echo is enabled only where FULL_DUPLEX_STREAMED needs it.
	var stops []func()
	for i, port := range []int{19001, 19002, 19003} {
		_, stop, err := StartEnvoyConsumer(port, NewScorer("c"+itoa(i), endpoints, 64), RoleStandalone, true, nil)
		if err != nil {
			return nil, err
		}
		stops = append(stops, stop)
	}
	// BUFFERED consumers are separate servers, because a StreamedResponse body
	// mutation is only valid under FULL_DUPLEX_STREAMED and corrupts the body
	// for downstream filters if returned in BUFFERED mode.
	for i, port := range []int{19004, 19005, 19006} {
		_, stop, err := StartEnvoyConsumer(port, NewScorer("b"+itoa(i), endpoints, 64), RoleStandalone, false, nil)
		if err != nil {
			return nil, err
		}
		stops = append(stops, stop)
	}

	// The shim: one consumer that parses once and hosts three plugins natively.
	hosted := []Binding{
		NewNativeBinding(plugin),
		NewNativeBinding(plugin),
		NewNativeBinding(plugin),
	}
	_, stopShim, err := StartEnvoyConsumer(19010, plugin, RoleShim, false, hosted)
	if err != nil {
		return nil, err
	}
	stops = append(stops, stopShim)

	// Etai's shape: the shim as a plain HTTP service, no Envoy in the path.
	stopFE, err := StartFrontend(10004, "http://127.0.0.1:19100", hosted)
	if err != nil {
		return nil, err
	}
	stops = append(stops, stopFE)

	defer func() {
		for _, s := range stops {
			s()
		}
	}()

	stopEnvoy, err := StartEnvoy(configDir)
	if err != nil {
		return nil, err
	}
	defer stopEnvoy()

	arms := []struct {
		label  string
		target string
		h2c    bool
	}{
		{"envoy bare (no ext_proc)", "http://127.0.0.1:10002", false},
		{"envoy + 3 ext_proc, echo (status quo)", "http://127.0.0.1:10000", false},
		{"envoy + 3 ext_proc, buffered (no echo)", "http://127.0.0.1:10003", false},
		{"envoy + 1 shim, parse once", "http://127.0.0.1:10001", false},
		{"h2c service, no envoy", "http://127.0.0.1:10004", true},
	}

	var out []LoadResult
	for _, a := range arms {
		// Warm connections and Envoy's per-listener state.
		warm := Load{Target: a.target, Concurrency: concurrency, Sessions: concurrency, Requests: reqs[:min(3, len(reqs))], H2C: a.h2c}
		if _, err := RunLoad(ctx, "warm", warm); err != nil {
			return nil, fmt.Errorf("warmup %s: %w", a.label, err)
		}
		res, err := RunLoad(ctx, a.label, Load{
			Target: a.target, Concurrency: concurrency, Sessions: sessions, Requests: reqs, H2C: a.h2c,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}
