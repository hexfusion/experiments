#!/usr/bin/env python3
"""Synthetic Level-2 client for the real Go Coordinator delta path.

Drives the delta contract directly (x-session-id header + delta-only input),
without agentic-api, to validate the Coordinator's session cache + delta render
in isolation. Then does the drift check: reconstruct the full conversation from
the captured turns, send it as ONE full-history request (no session-id, so the
Coordinator full-renders), and compare its input_tokens to the delta path's
final turn. Equal => the delta-appended tokens match a full render (no drift).

Run in-cluster:  kubectl exec -i <pod> -- python3 - < level2-test.py
"""
import json, time, urllib.request

BASE = "http://coordinator-real:8080"
MODEL = "Qwen/Qwen2.5-1.5B-Instruct"
SID = "sess-level2-demo"


def post(body, headers=None):
    h = {"Content-Type": "application/json"}
    if headers:
        h.update(headers)
    req = urllib.request.Request(BASE + "/v1/responses", data=json.dumps(body).encode(), headers=h)
    r = urllib.request.urlopen(req, timeout=150)
    return json.loads(r.read())


def out_text(resp):
    for it in resp.get("output", []):
        for c in it.get("content", []):
            if c.get("type") == "output_text":
                return c.get("text")
    return ""


def in_tokens(resp):
    return resp.get("usage", {}).get("input_tokens")


def msg(role, text):
    return {"type": "message", "role": role, "content": [{"type": ("output_text" if role == "assistant" else "input_text"), "text": text}]}


turns = [
    "I'm planning a trip to Japan. Start with Tokyo for 3 days.",
    "Add Kyoto for 2 days after Tokyo.",
    "Now add Osaka for 2 days, focused on food.",
    "Insert a Nara day trip from Kyoto.",
    "Summarize the whole itinerary so far, day by day.",
]

print("=== DELTA path (x-session-id + delta-only input) ===")
history = []              # reconstructed full conversation (user+assistant)
prev = None
delta_final_intok = None
for i, t in enumerate(turns, 1):
    body = {"model": MODEL, "input": [msg("user", t)], "max_output_tokens": 80, "temperature": 0}
    if i == 1:
        body["instructions"] = "You are a concise travel planner."
    # No previous_response_id: the Coordinator keys the cache on x-session-id, and
    # stateless vLLM 404s on an unknown previous_response_id.
    r = post(body, headers={"x-session-id": SID})
    prev = r.get("id")
    a = out_text(r)
    history.append(msg("user", t)); history.append(msg("assistant", a))
    delta_final_intok = in_tokens(r)
    print(f"turn {i}: in_tokens={in_tokens(r)}  -> {a[:90]!r}")
    time.sleep(2)

print("\n=== DRIFT CHECK: full render of the reconstructed conversation (no session-id) ===")
# Send the full accumulated conversation as one request; the Coordinator has no
# session for a fresh id, so it full-renders. Compare its input_tokens to the
# delta path's final turn (same conversation state).
full_body = {"model": MODEL, "instructions": "You are a concise travel planner.",
             "input": history[:-1] + [history[-1]],  # full user+assistant history ending at last user turn is already included
             "max_output_tokens": 8, "temperature": 0}
# history ends with the last assistant turn; for an apples-to-apples prompt we drop the trailing assistant
# so both prompts end at "assistant generation prompt" over the same prior context:
full_body["input"] = history[:-1]
rf = post(full_body, headers={"x-session-id": "sess-fullrender-baseline"})
full_intok = in_tokens(rf)
print(f"delta-path final in_tokens : {delta_final_intok}")
print(f"full-render   in_tokens    : {full_intok}")
if delta_final_intok is not None and full_intok is not None:
    print("DRIFT CHECK:", "PASS (token counts match)" if delta_final_intok == full_intok
          else f"MISMATCH (delta={delta_final_intok} full={full_intok}) -> assistant re-tokenize drift")
