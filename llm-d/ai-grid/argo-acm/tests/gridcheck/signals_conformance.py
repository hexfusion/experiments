#!/usr/bin/env python3
"""Check each site's /v1/site/signals against the signals contract, from outside the grid.

Every site is read twice: as a named peer (another site's identity) and as Local (its own
identity, the view its gateway gets). Refusals are checked with no certificate, a
certificate from a foreign CA, and optionally a grid-CA certificate that names no site.
Prints one line per rule and exits nonzero when any rule fails.
"""

import argparse
import datetime
import json
import math
import os
import sys
import tempfile
import time

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

import mtls

SITE_TTL_S = 30  # GRID_SIGNALS_TTL_SECS default: own samples expire after this.
PEER_TTL_S = 120  # GRID_SIGNALS_PEER_TTL_SECS default: relayed samples expire after this.
SKEW_S = 1  # Date has one-second resolution.
CONTENT_TYPE = "text/plain; version=0.0.4"
NO_SUCH = "gridcheck-no-such-target"


class Report:
    def __init__(self):
        self.rows = []

    def add(self, rule, site, caller, verdict, evidence):
        self.rows.append({"rule": rule, "site": site, "caller": caller, "verdict": verdict, "evidence": evidence})
        print(f"{verdict:4}  {rule:<28} {site:<10} {caller:<16} {evidence}", flush=True)

    def failed(self):
        return any(r["verdict"] == "FAIL" for r in self.rows)


def foreign_identity(directory, spiffe_id):
    """A self-signed client certificate claiming `spiffe_id`, from a CA the grid never trusted."""
    key = ec.generate_private_key(ec.SECP256R1())
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "gridcheck-foreign")])
    now = datetime.datetime.now(datetime.timezone.utc)
    cert = (
        x509.CertificateBuilder()
        .subject_name(name)
        .issuer_name(name)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - datetime.timedelta(minutes=1))
        .not_valid_after(now + datetime.timedelta(hours=1))
        .add_extension(x509.SubjectAlternativeName([x509.UniformResourceIdentifier(spiffe_id)]), critical=False)
        .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.CLIENT_AUTH]), critical=False)
        .sign(key, hashes.SHA256())
    )
    crt, pem_key = os.path.join(directory, "foreign.crt"), os.path.join(directory, "foreign.key")
    with open(crt, "wb") as f:
        f.write(cert.public_bytes(serialization.Encoding.PEM))
    with open(pem_key, "wb") as f:
        f.write(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    return crt, pem_key


def refused(result):
    return result.outcome in ("handshake_failed", "closed_no_response")


def describe(result):
    if result.outcome == "http":
        return f"HTTP {result.status}, {len(result.body)} bytes"
    return f"{result.outcome}: {result.detail[:90]}"


def check_refusals(rep, site, host, port, ca, peer_id, foreign, stranger):
    r = mtls.request(host, port, ca)
    rep.add("refuse.no-client-cert", site, "none", "PASS" if refused(r) else "FAIL", describe(r))
    r = mtls.request(host, port, ca, *foreign)
    rep.add("refuse.foreign-ca", site, f"foreign:{peer_id}", "PASS" if refused(r) else "FAIL", describe(r))
    if stranger:
        r = mtls.request(host, port, ca, *stranger)
        ok = refused(r) or (r.outcome == "http" and r.status == 403)
        rep.add("refuse.grid-ca-non-site", site, "stranger", "PASS" if ok else "FAIL", describe(r))
    else:
        rep.add("refuse.grid-ca-non-site", site, "stranger", "SKIP", "no --stranger certificate given")
    r = mtls.request(host, port, None, plaintext=True)
    ok = r.outcome != "http" or r.status != 200
    rep.add("refuse.plaintext", site, "none", "PASS" if ok else "FAIL", describe(r))


def check_view(rep, site, host, port, ca, ident, caller, local):
    """Rules for one read, as `caller`. Returns the samples served, or None."""
    r = mtls.request(host, port, ca, *ident)
    want = mtls.SITE_ID_PREFIX + site
    rep.add("server.identity", site, caller, "PASS" if r.server_spiffe == want else "FAIL",
            f"server SPIFFE {r.server_spiffe!r}, want {want!r}")
    ok = r.outcome == "http" and r.status == 200
    rep.add("read.status-200", site, caller, "PASS" if ok else "FAIL", describe(r))
    if not ok:
        return None
    ctype = r.headers.get("content-type", "")
    rep.add("read.content-type", site, caller, "PASS" if ctype.startswith(CONTENT_TYPE) else "FAIL", repr(ctype))
    date = mtls.date_epoch(r.headers)
    rep.add("read.date-header", site, caller, "PASS" if date else "FAIL", r.headers.get("date", "absent"))
    age = r.headers.get("age")
    ttl = PEER_TTL_S if local else SITE_TTL_S
    age_ok = age is not None and age.isdigit() and int(age) <= ttl
    rep.add("fresh.age-header", site, caller, "PASS" if age_ok else "FAIL", f"Age {age!r}, bound {ttl}s")

    samples, bad = mtls.parse_exposition(r.body)
    comments = [l for l in bad if l.startswith("#")]
    rep.add("format.no-metadata", site, caller, "FAIL" if comments else "PASS",
            f"{len(comments)} HELP/TYPE/comment lines" if comments else "no HELP or TYPE lines")
    rep.add("format.parses", site, caller, "FAIL" if len(bad) > len(comments) else "PASS",
            f"{len(samples)} samples, {len(bad) - len(comments)} unparsable lines")
    if not samples:
        neutral = r.body.strip() == ""
        rep.add("absence.neutral", site, caller, "PASS" if neutral else "FAIL",
                "200 with an empty body: no samples, no zero-filled placeholders" if neutral else "body without samples")
        for rule in ("format.timestamps", "labels.attribution", "scope.peer-own-site", "fresh.sample-age", "values.range"):
            rep.add(rule, site, caller, "SKIP", "no samples served")
        return samples
    rep.add("absence.neutral", site, caller, "SKIP", f"{len(samples)} samples present")
    no_ts = [s.line for s in samples if s.timestamp_ms is None]
    rep.add("format.timestamps", site, caller, "FAIL" if no_ts else "PASS",
            f"{len(no_ts)} without a timestamp" + (f", e.g. {no_ts[0][:80]}" if no_ts else ""))
    unlabelled = [s.line for s in samples if "grid_site" not in s.labels or "grid_provider" not in s.labels]
    rep.add("labels.attribution", site, caller, "FAIL" if unlabelled else "PASS",
            f"{len(unlabelled)} missing grid_site or grid_provider" + (f", e.g. {unlabelled[0][:80]}" if unlabelled else ""))
    sites = sorted({s.labels.get("grid_site", "") for s in samples})
    if local:
        rep.add("scope.peer-own-site", site, caller, "SKIP", f"Local sees {sites}")
    else:
        rep.add("scope.peer-own-site", site, caller, "PASS" if sites == [site] else "FAIL", f"grid_site values {sites}")
    worst = None
    for s in samples:
        if s.timestamp_ms is None or date is None:
            continue
        bound = SITE_TTL_S if s.labels.get("grid_site") == site else PEER_TTL_S
        sample_age = date - s.timestamp_ms / 1000
        if sample_age < -SKEW_S or sample_age > bound + SKEW_S:
            worst = worst or f"{s.name} age {sample_age:.1f}s outside [0, {bound}]"
    ages = [date - s.timestamp_ms / 1000 for s in samples if s.timestamp_ms and date]
    rep.add("fresh.sample-age", site, caller, "FAIL" if worst else "PASS",
            worst or f"ages {min(ages):.1f}s to {max(ages):.1f}s")
    bad_values = []
    for s in samples:
        try:
            v = float(s.value)
        except ValueError:
            bad_values.append(s.line)
            continue
        ratio = "usage" in s.name or "perc" in s.name or s.name.endswith("_ratio")
        if not math.isfinite(v) or v < 0 or (ratio and v > 1):
            bad_values.append(s.line)
    rep.add("values.range", site, caller, "FAIL" if bad_values else "PASS",
            f"{len(bad_values)} non-finite, negative, or ratio above 1" + (f", e.g. {bad_values[0][:80]}" if bad_values else ""))
    return samples


def check_narrowing(rep, site, host, port, ca, ident, caller, samples):
    for rule, path in (("narrow.unknown-target", f"?target={NO_SUCH}"), ("narrow.unknown-collect", f"?collect[]={NO_SUCH}")):
        r = mtls.request(host, port, ca, *ident, path=mtls.SIGNALS_PATH + path)
        ok = r.outcome == "http" and r.status == 200 and r.body.strip() == ""
        rep.add(rule, site, caller, "PASS" if ok else "FAIL", describe(r))
    if samples:
        target = samples[0].labels.get("grid_provider", "")
        r = mtls.request(host, port, ca, *ident, path=f"{mtls.SIGNALS_PATH}?target={target}")
        got, _ = mtls.parse_exposition(r.body) if r.outcome == "http" else ([], [])
        ok = got and {s.labels.get("grid_provider") for s in got} == {target}
        rep.add("narrow.known-target", site, caller, "PASS" if ok else "FAIL", f"target={target}: {len(got)} samples")
    else:
        rep.add("narrow.known-target", site, caller, "SKIP", "no samples served")
    r = mtls.request(host, port, ca, *ident, method="POST")
    rep.add("http.method-not-allowed", site, caller, "PASS" if r.outcome == "http" and r.status == 405 else "FAIL", describe(r))
    r = mtls.request(host, port, ca, *ident, path="/v1/site/nothing-here")
    rep.add("http.unknown-path", site, caller, "PASS" if r.outcome == "http" and r.status == 404 else "FAIL", describe(r))


def key_of(s):
    return (s.name, tuple(sorted(s.labels.items())))


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--site", action="append", required=True, help="NAME=HOST:PORT of a site's signals listener")
    p.add_argument("--identity", action="append", required=True, help="NAME=DIR holding that site's tls.crt and tls.key")
    p.add_argument("--ca", required=True, help="the Grid CA bundle")
    p.add_argument("--stranger", help="DIR with tls.crt and tls.key from the Grid CA that name no site")
    p.add_argument("--json", help="also write the rows here")
    a = p.parse_args()
    sites = {n: (h.rsplit(":", 1)[0], int(h.rsplit(":", 1)[1])) for n, h in (s.split("=", 1) for s in a.site)}
    idents = {n: (os.path.join(d, "tls.crt"), os.path.join(d, "tls.key")) for n, d in (i.split("=", 1) for i in a.identity)}
    stranger = (os.path.join(a.stranger, "tls.crt"), os.path.join(a.stranger, "tls.key")) if a.stranger else None
    rep = Report()
    with tempfile.TemporaryDirectory(prefix="gridcheck-") as scratch:
        os.chmod(scratch, 0o700)
        for site, (host, port) in sites.items():
            peers = [n for n in idents if n != site]
            peer = peers[0] if peers else None
            foreign = foreign_identity(scratch, mtls.SITE_ID_PREFIX + (peer or "nobody"))
            check_refusals(rep, site, host, port, a.ca, peer or "nobody", foreign, stranger)
            peer_samples = None
            if peer:
                peer_samples = check_view(rep, site, host, port, a.ca, idents[peer], f"peer:{peer}", local=False)
                if peer_samples is not None:
                    check_narrowing(rep, site, host, port, a.ca, idents[peer], f"peer:{peer}", peer_samples)
            else:
                rep.add("read.status-200", site, "peer", "SKIP", "no other site's identity given")
            if site in idents:
                local_samples = check_view(rep, site, host, port, a.ca, idents[site], "local", local=True)
                if peer_samples is not None and local_samples is not None:
                    extra = {key_of(s) for s in peer_samples} - {key_of(s) for s in local_samples}
                    rep.add("scope.peer-within-local", site, "local", "FAIL" if extra else "PASS",
                            f"{len(extra)} peer rows Local does not see")
                if local_samples:
                    time.sleep(6)
                    again = check_view(rep, site, host, port, a.ca, idents[site], "local+6s", local=True) or []
                    before = {key_of(s): s.timestamp_ms for s in local_samples}
                    moved = sum(1 for s in again if key_of(s) in before and s.timestamp_ms > before[key_of(s)])
                    rep.add("fresh.refreshes", site, "local", "PASS" if moved else "FAIL",
                            f"{moved} of {len(again)} samples restamped after 6s (scrape every 5s)")
                else:
                    rep.add("fresh.refreshes", site, "local", "SKIP", "no samples served")
            else:
                rep.add("read.status-200", site, "local", "SKIP", "no own identity given")
    if a.json:
        with open(a.json, "w") as f:
            json.dump(rep.rows, f, indent=2)
    total = {v: sum(1 for r in rep.rows if r["verdict"] == v) for v in ("PASS", "FAIL", "SKIP")}
    print(f"\n{total['PASS']} pass, {total['FAIL']} fail, {total['SKIP']} skip")
    sys.exit(1 if rep.failed() else 0)


if __name__ == "__main__":
    main()
