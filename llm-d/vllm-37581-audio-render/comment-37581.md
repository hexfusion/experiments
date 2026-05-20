# Draft comment for vllm-project/vllm#37581

Paste verbatim into the issue. Do not include this heading.

---

Tested against `vllm/vllm-openai:latest` (vLLM 0.21.0, `vllm-0.21.0-7a4b5ca9`) with `Qwen/Qwen3-ASR-0.6B` and can't reproduce either symptom:

- `/v1/chat/completions/render` returns 200 — `kwargs_data` is base64-encoded msgpack, no JSON-serialization crash.
- `/v1/chat/completions` with real speech returns a clean transcription (`language English<asr_text>...`).

Believe this is resolved. @peregilk — could you retest on current 0.21.x / main on your DGX Spark setup? If clean there too, this can be closed.
