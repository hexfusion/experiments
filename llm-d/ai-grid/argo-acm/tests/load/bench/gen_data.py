#!/usr/bin/env python3
"""Write a guidellm JSONL dataset near a target token count. Seeded, so reruns are identical.

Modes, from the environment:
  default            unique prompts; each starts with its own number, so no two share a prefix
  PREFIX_TOKENS=N    every prompt starts with the same N-token system prompt, then a unique body
  TURNS=T            conversations: turn k repeats the shared prefix (if any) and turns 1..k-1,
                     so later turns share a growing per-conversation prefix. Rows are ordered
                     round robin across CONVS conversations, so a conversation's turns are spaced.
Token counts are approximate (about 0.75 words per token); guidellm reports the counts the
server actually saw.

  gen_data.py OUT PROMPT_TOKENS OUTPUT_TOKENS [COUNT]
"""

import json
import os
import random
import sys

WORDS = (
    "river stone lantern harbor engine signal garden copper meadow winter orbit canvas "
    "ledger quarry violet thunder anchor bramble cinder falcon glacier hollow island jasper "
    "kernel lumber marble nectar oyster pepper quill ribbon saddle timber umber velvet "
    "willow yonder zephyr basalt cobalt delta ember fjord granite heron indigo juniper"
).split()

out, prompt_tokens, output_tokens = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
count = int(sys.argv[4]) if len(sys.argv) > 4 else 4000
prefix_tokens = int(os.environ.get("PREFIX_TOKENS", "0"))
turns = int(os.environ.get("TURNS", "0"))
convs = int(os.environ.get("CONVS", "64"))
rng = random.Random(7)


def words(tokens):
    return " ".join(rng.choice(WORDS) for _ in range(max(8, int(tokens * 0.75))))


prefix = f"System: you are the lab assistant. Reference notes: {words(prefix_tokens)}\n" if prefix_tokens else ""
rows = []
if turns:
    histories = [prefix + f"Conversation {c}.\n" for c in range(convs)]
    for t in range(turns):
        for c in range(convs):
            if len(rows) >= count:
                break
            histories[c] += f"User turn {t}: {words(prompt_tokens)}\n"
            rows.append(histories[c] + "Assistant:")
            histories[c] += f"Assistant turn {t}: {words(output_tokens)}\n"
else:
    for i in range(count):
        rows.append(f"{prefix}Request {i}. Summarize the following notes in detail: {words(prompt_tokens)}")

with open(out, "w") as f:
    for prompt in rows:
        f.write(json.dumps({"prompt": prompt, "output_tokens_count": output_tokens}) + "\n")
