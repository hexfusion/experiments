#!/usr/bin/env python3
"""Mint AI Grid consumer JWTs from their plan in plans.yaml.

    ./mint-tokens.py                              # alice=gold  bob=copper (default)
    ./mint-tokens.py alice=silver carol=gold      # user=plan pairs

Each token is an HS256 JWT the grid frontdoor accepts (user-jwt filter). It
carries the plan's rate budget (grid_rate/grid_burst), which the rate_limit
filter meters per identity, plus the tier. plans.yaml stays the single source
of truth. Pure stdlib -- no PyJWT/yaml dependency.

Signing secret comes from GRID_JWT_SECRET (must match the frontdoor policy's
decoding_key); falls back to the demo default when unset.
"""
import base64, hashlib, hmac, json, os, sys, time
from pathlib import Path

HERE = Path(__file__).resolve().parent
SECRET = os.environ.get("GRID_JWT_SECRET", "ai-grid-demo-secret-change-me")
ISSUER = os.environ.get("GRID_JWT_ISSUER", "https://grid.internal/idp")
AUDIENCE = os.environ.get("GRID_JWT_AUDIENCE", "ai-grid")
TTL = int(os.environ.get("GRID_JWT_TTL", "86400"))


def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def mint(sub: str, tier: str, rate: int, burst: int, ttl: int = TTL) -> str:
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {"iss": ISSUER, "aud": AUDIENCE, "sub": sub, "tier": tier,
               "grid_rate": rate, "grid_burst": burst, "iat": now, "exp": now + ttl}
    signing_input = "{}.{}".format(
        _b64url(json.dumps(header, separators=(",", ":")).encode()),
        _b64url(json.dumps(payload, separators=(",", ":")).encode()))
    sig = hmac.new(SECRET.encode(), signing_input.encode(), hashlib.sha256).digest()
    return "{}.{}".format(signing_input, _b64url(sig))


def load_plans(path: Path) -> dict:
    """Minimal plans.yaml reader (top-level `plans:` -> plan -> scalar fields)."""
    plans, cur, key = {}, None, None
    for raw in path.read_text().splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip())
        line = raw.strip()
        if indent == 2 and line.endswith(":"):            # plan name (under `plans:`)
            key = line[:-1]; plans[key] = {}; cur = plans[key]
        elif indent == 4 and ":" in line and key is not None:  # a plan's scalar field
            k, _, v = line.partition(":")
            v = v.split("#", 1)[0].strip()
            if v:
                try: cur[k.strip()] = int(v)
                except ValueError: cur[k.strip()] = v.strip('"')
    return plans


def main() -> None:
    plans = load_plans(HERE.parent / "plans.yaml")
    assign = dict(a.split("=", 1) for a in (sys.argv[1:] or ["alice=gold", "bob=copper"]))
    print(f"{'USER':8} {'PLAN':7} {'rate/s':>7} {'burst':>7}   TOKEN")
    print("-" * 92)
    for user, level in assign.items():
        if level not in plans:
            sys.exit(f"unknown plan {level!r}; known: {', '.join(plans)}")
        p = plans[level]
        tok = mint(user, level, p["rate"], p["burst"])
        print(f"{user:8} {level:7} {p['rate']:>7} {p['burst']:>7}   {tok}")


if __name__ == "__main__":
    main()
