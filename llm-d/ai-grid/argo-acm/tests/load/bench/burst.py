#!/usr/bin/env python3
"""Open-loop bursts through the front door, timing the first token of each streamed reply.

Steady RATE req/s, BURST_RATE req/s for the first BURST_SECS of every PERIOD seconds, for
DURATION seconds, at scenario 1's long shape (about 512 prompt and 256 output tokens).
Prints "# t0 EPOCH", then one CSV line per request: start (epoch), id, code, ttft_s (first content
chunk), total_s. A request starts inside a burst when (start - t0) % PERIOD < BURST_SECS.
Settings come from the environment; MAAS_KEY is the bearer.
"""

import asyncio
import json
import os
import time

import httpx

URL = os.environ.get("URL", "https://maas.acme.lab/v1/chat/completions")
MODEL = os.environ.get("MODEL", "qwen3-coder-30b-a3b")
RATE = float(os.environ.get("RATE", "5"))
BURST_RATE = float(os.environ.get("BURST_RATE", "10"))
BURST_SECS = float(os.environ.get("BURST_SECS", "3"))
PERIOD = float(os.environ.get("PERIOD", "30"))
DURATION = float(os.environ.get("DURATION", "300"))
WORDS = ("river stone lantern harbor engine signal garden copper meadow winter orbit canvas ledger quarry "
         "violet thunder anchor bramble cinder falcon glacier hollow island jasper kernel lumber marble nectar "
         "oyster pepper quill ribbon saddle timber umber velvet willow yonder zephyr basalt cobalt delta ember "
         "fjord granite heron indigo juniper ").split() * 8
BODY = " ".join(WORDS)


async def one(client, i, t0):
    start = time.time()
    payload = {"model": MODEL, "stream": True, "max_tokens": 256, "ignore_eos": True,
               "messages": [{"role": "user", "content": f"Request {i} at {start}. Summarize the following notes in detail: {BODY}"}]}
    ttft, code = None, 0
    try:
        async with client.stream("POST", URL, json=payload, headers={"Authorization": f"Bearer {os.environ['MAAS_KEY']}"}) as r:
            code = r.status_code
            async for line in r.aiter_lines():
                if ttft is None and line.startswith("data: ") and line != "data: [DONE]":
                    choices = json.loads(line[6:]).get("choices") or [{}]
                    if (choices[0].get("delta") or {}).get("content"):
                        ttft = time.time() - start
    except httpx.HTTPError:
        code = code or -1
    end = time.time()
    print(f"{start:.3f},{i},{code},{'' if ttft is None else f'{ttft:.3f}'},{end - start:.3f}", flush=True)


async def main():
    print("start,id,code,ttft_s,total_s", flush=True)
    limits = httpx.Limits(max_connections=None, max_keepalive_connections=64)
    async with httpx.AsyncClient(verify=False, timeout=300, limits=limits, http2=False) as client:
        t0, tasks, i, next_at = time.time(), [], 0, 0.0
        print(f"# t0 {t0:.3f}", flush=True)
        while next_at < DURATION:
            await asyncio.sleep(max(0.0, t0 + next_at - time.time()))
            i += 1
            tasks.append(asyncio.create_task(one(client, i, t0)))
            rate = BURST_RATE if (next_at % PERIOD) < BURST_SECS else RATE
            next_at += 1.0 / rate
        await asyncio.gather(*tasks)
    print("BURST DONE", flush=True)


asyncio.run(main())
