package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// How routing data reaches the decider.
const (
	CarrierBody    = "body"    // forward the raw request, decider parses it
	CarrierJSON    = "json"    // derived attributes as a document
	CarrierHeaders = "headers" // derived attributes as headers, no entity
)

// headerBytes approximates what a header set costs on the wire before HPACK.
// HPACK indexes repeated names, so this over-counts steady state on a persistent
// connection, which keeps the comparison conservative.
func headerBytes(h http.Header) int64 {
	var n int64
	for k, vs := range h {
		for _, v := range vs {
			n += int64(len(k) + len(v) + 4)
		}
	}
	return n
}

// IPP is the HTTP front door. It keeps nothing between requests, so which
// replica serves a request is a load-balancing question and nothing more.
type IPP struct {
	EPPs []Replica
	Pods map[string]*Pod

	Client *http.Client
	Work   time.Duration
	next   atomic.Uint64

	// Carrier selects how routing data reaches the decider: CarrierBody forwards
	// the raw request, CarrierJSON sends derived attributes as a document, and
	// CarrierHeaders sends them as headers with no entity at all. IPP has already
	// parsed by this point, so the latter two are the read-once composition.
	Carrier string

	// ReportToDecider sends the completion back to the replica that decided.
	// Required when decisions are not broadcast: that replica did the local
	// increment and is the only one that can undo it. Broadcast is what removes
	// this constraint, and with it the last bit of affinity.
	ReportToDecider bool

	sentBytes atomic.Int64
}

type Replica struct {
	Name string
	URL  string
}

func (i *IPP) rr() Replica { return i.EPPs[int(i.next.Add(1)-1)%len(i.EPPs)] }

// Handle runs one client request: ask a replica, use the pod it named, report.
func (i *IPP) Handle(reqID, model string, body []byte) (string, error) {
	target := i.rr()

	hreq, err := http.NewRequest("POST", target.URL+"/v1/schedule", nil)
	if err != nil {
		return "", err
	}

	switch i.Carrier {
	case CarrierHeaders:
		if err := EncodeAttributes(hreq.Header, reqID, model, extract(body)); err != nil {
			return "", err
		}
		i.sentBytes.Add(headerBytes(hreq.Header))
	default:
		req := ScheduleRequest{RequestID: reqID, Model: model}
		if i.Carrier == CarrierJSON {
			req.Attributes = extract(body)
		} else {
			req.Body = body
		}
		payload, err := json.Marshal(req)
		if err != nil {
			return "", err
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Body = io.NopCloser(bytes.NewReader(payload))
		hreq.ContentLength = int64(len(payload))
		i.sentBytes.Add(int64(len(payload)) + headerBytes(hreq.Header))
	}

	resp, err := i.Client.Do(hreq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var dec ScheduleResponse
	if err := json.NewDecoder(resp.Body).Decode(&dec); err != nil {
		return "", err
	}
	if dec.Deny != nil {
		return "", fmt.Errorf("denied: %s", dec.Deny.Reason)
	}

	pod, ok := i.Pods[dec.Endpoint]
	if !ok {
		return "", fmt.Errorf("unknown endpoint %q", dec.Endpoint)
	}
	pod.Serve(i.Work)

	back := target
	if !i.ReportToDecider {
		back = i.rr()
	}
	rb, _ := json.Marshal(Report{RequestID: reqID, Endpoint: dec.Endpoint})
	rr, err := i.Client.Post(back.URL+"/v1/report", "application/json", bytes.NewReader(rb))
	if err != nil {
		return "", err
	}
	rr.Body.Close()

	return dec.Endpoint, nil
}

func (i *IPP) SentBytes() int64 { return i.sentBytes.Load() }

// extract is the parse IPP already performs for its own policy work. Publishing
// the result is what stops it happening again downstream.
func extract(body []byte) *Attributes {
	h := fnv.New64a()
	if n := len(body); n > 512 {
		h.Write(body[:512])
	} else {
		h.Write(body)
	}
	return &Attributes{PromptTokens: len(body) / 4, PrefixHash: h.Sum64()}
}
