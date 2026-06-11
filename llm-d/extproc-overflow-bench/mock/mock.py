import asyncio, json
METRICS = (b"# TYPE vllm:num_requests_waiting gauge\nvllm:num_requests_waiting 0\n"
           b"# TYPE vllm:num_requests_running gauge\nvllm:num_requests_running 0\n"
           b"# TYPE vllm:kv_cache_usage_perc gauge\nvllm:kv_cache_usage_perc 0.0\n")
async def handle(reader, writer):
    try:
        data = b""
        while b"\r\n\r\n" not in data:
            chunk = await reader.read(4096)
            if not chunk: writer.close(); return
            data += chunk
        line = data.split(b"\r\n",1)[0]
        if b"/metrics" in line:  # EPP scrape path
            writer.write(b"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n" % len(METRICS) + METRICS)
            await writer.drain(); writer.close(); return
        # else: SSE chat completion, streamed slowly to HOLD the connection
        writer.write(b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n")
        await writer.drain()
        for _ in range(60):
            writer.write(b'data: {"choices":[{"delta":{"content":"x"},"index":0}]}\n\n')
            await writer.drain(); await asyncio.sleep(0.2)
        writer.write(b"data: [DONE]\n\n"); await writer.drain()
    except Exception: pass
    finally:
        try: writer.close()
        except Exception: pass
async def main():
    s = await asyncio.start_server(handle, "0.0.0.0", 8000, limit=2**16)
    async with s: await s.serve_forever()
asyncio.run(main())
