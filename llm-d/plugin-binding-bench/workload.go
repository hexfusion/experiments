package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Conversation models a real multi-turn session. Each turn resends the whole
// history plus one new exchange, so request sizes grow monotonically and the
// total bytes a session pushes through the gateway is quadratic in turn count.
// That growth, not any single request, is what makes the per-consumer body cost
// matter.
type Conversation struct {
	Turns          int
	UserWords      int
	AssistantWords int
	Model          string
}

func DefaultConversation() Conversation {
	// 40 turns, ~1200 words of assistant output per turn: a long agentic session
	// that ends around a megabyte of request body.
	return Conversation{Turns: 40, UserWords: 60, AssistantWords: 1200, Model: "llama-3.1-8b"}
}

// Requests returns one request body per turn, each containing the full history.
func (c Conversation) Requests() [][]byte {
	userTurn := strings.TrimSpace(strings.Repeat("please summarise the preceding context carefully ", c.UserWords/7+1))
	asstTurn := strings.TrimSpace(strings.Repeat("here is a detailed answer with supporting reasoning and citations ", c.AssistantWords/10+1))

	var history []Message
	out := make([][]byte, 0, c.Turns)
	for i := 0; i < c.Turns; i++ {
		history = append(history, Message{Role: "user", Content: userTurn})
		req := ChatRequest{Model: c.Model, Stream: true, Messages: history}
		b, err := json.Marshal(&req)
		if err != nil {
			panic(err)
		}
		out = append(out, b)
		// The assistant reply becomes history for the next turn.
		history = append(history, Message{Role: "assistant", Content: asstTurn})
	}
	return out
}

// Stats describes the shape of a generated session.
type Stats struct {
	Turns     int
	TotalKB   int
	FirstKB   int
	LastKB    int
	MeanKB    int
}

func (c Conversation) Stats() Stats {
	reqs := c.Requests()
	total := 0
	for _, r := range reqs {
		total += len(r)
	}
	return Stats{
		Turns:   len(reqs),
		TotalKB: total >> 10,
		FirstKB: len(reqs[0]) >> 10,
		LastKB:  len(reqs[len(reqs)-1]) >> 10,
		MeanKB:  (total / len(reqs)) >> 10,
	}
}

func (s Stats) String() string {
	return fmt.Sprintf("%d turns, first %dKB, last %dKB, mean %dKB, %dKB total per session",
		s.Turns, s.FirstKB, s.LastKB, s.MeanKB, s.TotalKB)
}

// Agentic models the shape a per-byte argument should struggle with: a long
// session of many small requests rather than one growing body. Real agent loops
// compact context, so the body stays bounded while request count climbs, and
// tool results are short.
//
// This is the adversarial case for this design. If single ownership only wins
// on bytes, it should lose here.
type Agentic struct {
	Requests  int
	BodyBytes int
	Model     string
}

func DefaultAgentic() Agentic {
	return Agentic{Requests: 200, BodyBytes: 3 << 10, Model: "llama-3.1-8b"}
}

// Requests returns one bounded request per agent step. Content varies per step
// so nothing is trivially cacheable, but size does not grow.
func (a Agentic) Bodies() [][]byte {
	out := make([][]byte, 0, a.Requests)
	unit := "call the tool and summarise the result "
	perMsg := a.BodyBytes / 4
	words := perMsg / len(unit)
	if words < 1 {
		words = 1
	}
	for i := 0; i < a.Requests; i++ {
		tag := itoa(i) + " "
		req := ChatRequest{Model: a.Model, Stream: true, Messages: []Message{
			{Role: "system", Content: strings.TrimSpace(strings.Repeat(unit, words))},
			{Role: "user", Content: tag + strings.TrimSpace(strings.Repeat(unit, words))},
			{Role: "assistant", Content: tag + strings.TrimSpace(strings.Repeat(unit, words))},
			{Role: "user", Content: tag + strings.TrimSpace(strings.Repeat(unit, words))},
		}}
		b, err := json.Marshal(&req)
		if err != nil {
			panic(err)
		}
		out = append(out, b)
	}
	return out
}

func (a Agentic) Stats() string {
	bodies := a.Bodies()
	total := 0
	for _, b := range bodies {
		total += len(b)
	}
	return fmt.Sprintf("%d requests, %dKB each, %dKB total per session (flat, no growth)",
		len(bodies), len(bodies[0])>>10, total>>10)
}
