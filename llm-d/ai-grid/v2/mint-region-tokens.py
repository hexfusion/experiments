#!/usr/bin/env python3
"""Mint the four region-scoped consumer JWTs the geo demo needs.

    ./mint-region-tokens.py            # prints a table of all four tokens
    ./mint-region-tokens.py --env      # prints US=... EU=... UK=... NONE=... for eval

Each is an HS256 JWT the grid frontdoor (v2) and the pure gateway (v3) both
accept via their `user-jwt` identity plugin. It carries `grid_region` (the
residency claim both fences on), plus `tier`/`grid_rate`/`grid_burst` so the v3
gateway's quota plugin can also meter it. The signing secret and issuer match
mint-tokens.py, so the same frontdoor config accepts both.

    US   -> grid_region us-east-1   (routes to site-a or site-b)
    EU   -> grid_region eu-west-1   (routes to site-d)
    UK   -> grid_region eu-west-2   (routes to site-c, OpenRouter)
    NONE -> no grid_region          (fails closed under a residency gate)

Pure stdlib; no PyJWT dependency.
"""
import base64, hashlib, hmac, json, os, sys, time

SECRET = os.environ.get("GRID_JWT_SECRET", "ai-grid-demo-secret-change-me")
ISSUER = os.environ.get("GRID_JWT_ISSUER", "https://grid.internal/idp")
AUDIENCE = os.environ.get("GRID_JWT_AUDIENCE", "ai-grid")
TTL = int(os.environ.get("GRID_JWT_TTL", "86400"))

# name -> (subject, grid_region or None). Budget is uniform so the quota plugin
# has a bucket to meter; geo routing does not depend on it.
TOKENS = {
    "US": ("us-user", "us-east-1"),
    "EU": ("eu-user", "eu-west-1"),
    "UK": ("uk-user", "eu-west-2"),
    "NONE": ("noregion-user", None),
}
TIER, RATE, BURST = "gold", 50, 2000


def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def mint(sub: str, region: str | None) -> str:
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {"iss": ISSUER, "aud": AUDIENCE, "sub": sub, "tier": TIER,
               "grid_rate": RATE, "grid_burst": BURST, "iat": now, "exp": now + TTL}
    if region is not None:
        payload["grid_region"] = region
    signing_input = "{}.{}".format(
        _b64url(json.dumps(header, separators=(",", ":")).encode()),
        _b64url(json.dumps(payload, separators=(",", ":")).encode()))
    sig = hmac.new(SECRET.encode(), signing_input.encode(), hashlib.sha256).digest()
    return "{}.{}".format(signing_input, _b64url(sig))


def main() -> None:
    env = "--env" in sys.argv[1:]
    if env:
        for name, (sub, region) in TOKENS.items():
            print(f"{name}={mint(sub, region)}")
        return
    print(f"{'NAME':5} {'SUBJECT':14} {'grid_region':12}   TOKEN")
    print("-" * 96)
    for name, (sub, region) in TOKENS.items():
        print(f"{name:5} {sub:14} {region or '(none)':12}   {mint(sub, region)}")


if __name__ == "__main__":
    main()
