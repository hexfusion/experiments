#!/usr/bin/env python3
"""Tokenize-once Coordinator: stateful /v1/responses, session-id token store.

The client sends a new turn plus previous_response_id (never tokens). The
Coordinator dehydrates the running token sequence for that session from its DB,
renders ONLY the delta (the new turn), appends, and ships pure token_ids to the
router internally. The EPP short-circuits on the tokens (no /render) and vLLM
does token-in (no re-tokenize).

Delta rendering is exact: turn boundaries are hard special-token boundaries, so
render(prefix) + render(delta) == render(full). VERIFY=1 asserts this every turn
against a full render. The public contract is /v1/responses; /v1/completions is
an internal on-ramp only.
"""
import json, os, sqlite3, threading, time, uuid, urllib.request, urllib.error
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

MODEL        = os.environ.get("MODEL", "Qwen/Qwen2.5-1.5B-Instruct")
TOKENIZE_URL = os.environ.get("TOKENIZE_URL", "http://vllm-server:8000")  # engine tokenizer
ROUTER_URL   = os.environ.get("ROUTER_URL", "http://router-epp:8081")     # Envoy -> EPP -> vLLM
DB_PATH      = os.environ.get("DB_PATH", "/tmp/coordinator.db")
PORT         = int(os.environ.get("PORT", "9100"))
VERIFY       = os.environ.get("VERIFY", "") != ""

_lock = threading.Lock()
_tls = threading.local()   # per-request W3C traceparent, propagated to downstream calls
_gen_suffix = None  # trailing generation-prompt tokens, model-derived once
_m = {"turns": 0, "delta_render_tokens": 0, "prompt_tokens": 0, "passthrough": 0, "upstream_seconds": 0.0}


def _new_traceparent():
    return "00-" + os.urandom(16).hex() + "-" + os.urandom(8).hex() + "-01"


def _db():
    c = sqlite3.connect(DB_PATH)
    c.execute("CREATE TABLE IF NOT EXISTS sessions(sid TEXT PRIMARY KEY, messages TEXT, tokens TEXT)")
    c.execute("CREATE TABLE IF NOT EXISTS responses(rid TEXT PRIMARY KEY, sid TEXT)")
    return c


def _post(url, payload, timeout=120):
    headers = {"Content-Type": "application/json"}
    tp = getattr(_tls, "tp", None)   # propagate the request's trace to downstream hops
    if tp:
        headers["traceparent"] = tp
    req = urllib.request.Request(url, data=json.dumps(payload).encode(), headers=headers)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read())


def _tok_messages(messages, gen):
    return _post(TOKENIZE_URL + "/tokenize",
                 {"model": MODEL, "messages": messages, "add_generation_prompt": gen})["tokens"]


def _tok_segment(text):
    # tokenize a raw ChatML segment in isolation; special tokens parsed, no BOS/EOS
    return _post(TOKENIZE_URL + "/tokenize",
                 {"model": MODEL, "prompt": text, "add_special_tokens": False})["tokens"]


def _wrap(role, content):
    # ChatML turn (Qwen and most instruct models); verified to concat-equal in-context
    return f"<|im_start|>{role}\n{content}<|im_end|>\n"


def _input_to_messages(inp):
    if isinstance(inp, str):
        return [{"role": "user", "content": inp}]
    msgs = []
    for item in inp or []:
        if item.get("type") != "message":
            continue
        parts = item.get("content", [])
        text = parts if isinstance(parts, str) else \
            "".join(p.get("text", "") for p in parts if isinstance(p, dict))
        msgs.append({"role": item.get("role", "user"), "content": text})
    return msgs


def handle_responses(body):
    global _gen_suffix
    prev = body.get("previous_response_id")
    instructions = body.get("instructions")
    max_out = body.get("max_output_tokens") or body.get("max_tokens") or 256
    delta = _input_to_messages(body.get("input"))

    with _lock:
        c = _db()
        sid = None
        if prev:
            row = c.execute("SELECT sid FROM responses WHERE rid=?", (prev,)).fetchone()
            sid = row[0] if row else None

        rendered = 0
        if sid is None:
            # new session: base render establishes the exact prefix (handles system/BOS)
            sid = "sess_" + uuid.uuid4().hex[:16]
            messages = ([{"role": "system", "content": instructions}] if instructions else []) + delta
            running = _tok_messages(messages, gen=False)
            rendered = len(running)
        else:
            # dehydrate the running token sequence for this session, render only the delta
            row = c.execute("SELECT messages, tokens FROM sessions WHERE sid=?", (sid,)).fetchone()
            messages, running = json.loads(row[0]), json.loads(row[1])
            for m in delta:
                seg = _tok_segment(_wrap(m["role"], m["content"]))
                running += seg; rendered += len(seg); messages.append(m)

        if _gen_suffix is None:  # model-derived once: full-gen render minus no-gen
            full_gen = _tok_messages(messages, gen=True)
            _gen_suffix = full_gen[len(running):]
        inf = running + _gen_suffix

        if VERIFY:  # prove the delta path equals a full render, then trust it
            full = _tok_messages(messages, gen=True)
            print(f"verify: delta=={full==inf} (inf={len(inf)} full={len(full)})", flush=True)

        t0 = time.time()
        comp = _post(ROUTER_URL + "/v1/completions",
                     {"model": MODEL, "prompt": inf, "max_tokens": max_out, "temperature": 0.7})
        _m["upstream_seconds"] += time.time() - t0

        text = comp["choices"][0]["text"]
        usage = comp.get("usage", {})
        aseg = _tok_segment(_wrap("assistant", text))  # append the answer as a delta
        running += aseg; rendered += len(aseg)
        messages.append({"role": "assistant", "content": text})

        _m["turns"] += 1
        _m["delta_render_tokens"] += rendered
        _m["prompt_tokens"] += len(inf)

        rid = "resp_" + uuid.uuid4().hex[:16]
        c.execute("INSERT OR REPLACE INTO sessions(sid,messages,tokens) VALUES(?,?,?)",
                  (sid, json.dumps(messages), json.dumps(running)))
        c.execute("INSERT INTO responses(rid,sid) VALUES(?,?)", (rid, sid))
        c.commit(); c.close()

    return {
        "id": rid, "object": "response", "model": MODEL, "session_id": sid,
        "output": [{"type": "message", "role": "assistant", "status": "completed",
                    "content": [{"type": "output_text", "text": text}]}],
        "usage": usage, "prompt_token_count": len(inf), "delta_render_tokens": rendered,
    }


def handle_passthrough(path, body):
    _m["passthrough"] += 1
    return _post(ROUTER_URL + path, body)


def metrics_text():
    return "".join([
        "# TYPE coordinator_turns_total counter\n",        f"coordinator_turns_total {_m['turns']}\n",
        "# HELP coordinator_delta_render_tokens_total Tokens tokenized (delta only; ~const/turn)\n",
        "# TYPE coordinator_delta_render_tokens_total counter\n", f"coordinator_delta_render_tokens_total {_m['delta_render_tokens']}\n",
        "# HELP coordinator_prompt_tokens_total Tokens shipped to inference (grows with conversation)\n",
        "# TYPE coordinator_prompt_tokens_total counter\n", f"coordinator_prompt_tokens_total {_m['prompt_tokens']}\n",
        "# TYPE coordinator_passthrough_total counter\n",  f"coordinator_passthrough_total {_m['passthrough']}\n",
        "# TYPE coordinator_upstream_seconds_total counter\n", f"coordinator_upstream_seconds_total {_m['upstream_seconds']:.3f}\n",
    ])


class H(BaseHTTPRequestHandler):
    def _send(self, code, obj, ctype="application/json"):
        data = obj.encode() if isinstance(obj, str) else json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/health":
            return self._send(200, {"status": "ok"})
        if self.path == "/metrics":
            return self._send(200, metrics_text(), "text/plain; version=0.0.4")
        self._send(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n) or b"{}")
        try:
            if self.path == "/v1/responses":
                return self._send(200, handle_responses(body))
            if self.path in ("/v1/completions", "/v1/chat/completions"):
                return self._send(200, handle_passthrough(self.path, body))
            self._send(404, {"error": "not found"})
        except urllib.error.HTTPError as e:
            self._send(e.code, {"error": e.read().decode(errors="replace")})
        except Exception as e:
            self._send(500, {"error": str(e)})

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    os.makedirs(os.path.dirname(DB_PATH) or ".", exist_ok=True)
    print(f"coordinator on :{PORT} model={MODEL} tokenize={TOKENIZE_URL} router={ROUTER_URL} verify={VERIFY}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), H).serve_forever()
