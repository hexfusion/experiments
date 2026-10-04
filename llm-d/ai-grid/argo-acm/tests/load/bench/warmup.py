#!/usr/bin/env python3
"""Send one small chat request so the gateway's api-key cache is warm before load starts.
Retries up to 5 times: the MaaS front door fails a request closed about every 30s.

  warmup.py TARGET MODEL   (the key, when set, comes from MAAS_KEY)
"""

import json
import os
import ssl
import sys
import time
import urllib.error
import urllib.request

target, model = sys.argv[1], sys.argv[2]
body = json.dumps({"model": model, "messages": [{"role": "user", "content": "warm up"}], "max_tokens": 1}).encode()
request = urllib.request.Request(f"{target}/v1/chat/completions", data=body, headers={"Content-Type": "application/json"})
if os.environ.get("MAAS_KEY"):
    request.add_header("Authorization", f"Bearer {os.environ['MAAS_KEY']}")
context = ssl.create_default_context()
context.check_hostname = False
context.verify_mode = ssl.CERT_NONE
for attempt in range(5):
    try:
        with urllib.request.urlopen(request, context=context, timeout=60) as response:
            print(f"warm-up: HTTP {response.status}", flush=True)
            break
    except urllib.error.HTTPError as err:
        print(f"warm-up: HTTP {err.code}, retrying", flush=True)
        time.sleep(2)
else:
    sys.exit("warm-up: no success after 5 attempts")
