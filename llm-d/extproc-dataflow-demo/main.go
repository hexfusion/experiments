// extproc-dataflow-demo: the IPP/EPP dependency on real Envoy ext_proc, with ONE IPP.
//
// The IPP brackets the EPP. Model selection must run before the EPP (it routes on the model);
// auth + translation must run after the EPP (they depend on the pick). So the IPP touches the
// request at two points around the EPP. Per Envoy issue 35317, Envoy opens one gRPC stream per
// HTTP request PER FILTER, so the two IPP hooks are two streams to the SAME IPP process; they
// correlate by x-request-id and share server-side state the pre hook computed.
//
//   IPP pre   (before EPP) : select model, stash {model, entitlement} by request id
//   EPP                    : pick destination -> x-gateway-destination-endpoint
//   IPP post  (after EPP)  : recover state by request id, inject Bearer token, translate body
//   echo                   : plain HTTP upstream that reflects the final request
//
// Each role is a separate process. The component logic lives in the epp and ipp packages; this
// file only wires roles to servers and runs the echo upstream.
//
// Roles (one binary, -role): ipp | epp | echo.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/hexfusion/experiments/llm-d/extproc-dataflow-demo/epp"
	"github.com/hexfusion/experiments/llm-d/extproc-dataflow-demo/extproc"
	"github.com/hexfusion/experiments/llm-d/extproc-dataflow-demo/ipp"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	role := flag.String("role", "", "ipp|epp|echo")
	port := flag.String("port", "", "listen port")
	flag.Parse()

	switch *role {
	case "ipp":
		return extproc.Serve("ipp", *port, ipp.New())
	case "epp":
		return extproc.Serve("epp", *port, epp.New())
	case "echo":
		return runEcho(*port)
	default:
		return fmt.Errorf("unknown -role %q (want ipp|epp|echo)", *role)
	}
}

// runEcho is a plain HTTP upstream that reflects the final request, so every mutation is visible.
func runEcho(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hdrs := map[string]string{}
		for k, v := range r.Header {
			hdrs[strings.ToLower(k)] = strings.Join(v, ",")
		}
		out := map[string]any{"method": r.Method, "path": r.URL.Path, "headers": hdrs, "body": string(body)}
		w.Header().Set("content-type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		log.Printf("[echo] %s %s body=%s", r.Method, r.URL.Path, string(body))
	})
	log.Printf("[echo] listening :%s", port)
	return http.ListenAndServe(":"+port, mux)
}
