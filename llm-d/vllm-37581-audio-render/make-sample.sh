#!/bin/bash
# Generate an audio fixture (sine wave or real speech) and embed its base64 into audio-payload.json.
#
# SAMPLE_TYPE=sine   — 2 s 440 Hz tone. Exercises serialization path; not real speech.
# SAMPLE_TYPE=speech — espeak-ng-synthesized phrase. Real speech, for ASR-output retest.

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
AUDIO_FORMAT="${AUDIO_FORMAT:-mp3}"     # mp3 | wav
SAMPLE_TYPE="${SAMPLE_TYPE:-sine}"      # sine | speech
SPEECH_TEXT="${SPEECH_TEXT:-Hello, this is a vLLM audio render test.}"
AUDIO="$DIR/sample.$AUDIO_FORMAT"
B64="$DIR/sample.b64"
PAYLOAD="$DIR/audio-payload.json"

make_sine_wav() {
  ffmpeg -y -f lavfi -i "sine=frequency=440:duration=2" -ar 16000 -ac 1 "$1" 2>/dev/null
}

make_speech_wav() {
  command -v espeak-ng >/dev/null || { echo "espeak-ng not installed" >&2; exit 1; }
  espeak-ng -s 160 -v en-us -w "$1" "$SPEECH_TEXT"
  # normalize: 16kHz, mono — matches what ASR models expect
  ffmpeg -y -i "$1" -ar 16000 -ac 1 "${1}.tmp.wav" 2>/dev/null && mv "${1}.tmp.wav" "$1"
}

case "$SAMPLE_TYPE" in
  sine)   src_wav="$DIR/sample.src.wav"; make_sine_wav "$src_wav" ;;
  speech) src_wav="$DIR/sample.src.wav"; make_speech_wav "$src_wav" ;;
  *) echo "unsupported SAMPLE_TYPE: $SAMPLE_TYPE (use sine or speech)" >&2; exit 1 ;;
esac

case "$AUDIO_FORMAT" in
  wav) mv "$src_wav" "$AUDIO" ;;
  mp3) ffmpeg -y -i "$src_wav" -ar 16000 -ac 1 -codec:a libmp3lame -b:a 64k "$AUDIO" 2>/dev/null && rm -f "$src_wav" ;;
  *)   echo "unsupported AUDIO_FORMAT: $AUDIO_FORMAT (use mp3 or wav)" >&2; exit 1 ;;
esac

base64 -w0 "$AUDIO" > "$B64"

python3 - <<EOF
import json, os, pathlib

fmt = os.environ.get("AUDIO_FORMAT", "mp3")
b64 = pathlib.Path("$B64").read_text().strip()
payload = {
    "model": "Qwen/Qwen3-ASR-0.6B",
    "messages": [
        {
            "role": "user",
            "content": [
                {"type": "text", "text": "transcribe this audio"},
                {"type": "input_audio", "input_audio": {"data": b64, "format": fmt}},
            ],
        }
    ],
}
pathlib.Path("$PAYLOAD").write_text(json.dumps(payload, indent=2) + "\n")
EOF

echo "Generated $AUDIO ($(stat -c%s "$AUDIO") bytes, type=$SAMPLE_TYPE, format=$AUDIO_FORMAT) and embedded base64 into $PAYLOAD"
