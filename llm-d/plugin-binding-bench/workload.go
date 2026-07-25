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
