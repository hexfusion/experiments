"""A one-request mTLS client for /v1/site/signals, as a grid peer's poller calls it."""

import re
import socket
import ssl
import time
from dataclasses import dataclass, field
from email.utils import parsedate_to_datetime
from typing import Optional

from cryptography import x509

SIGNALS_PATH = "/v1/site/signals"
SITE_ID_PREFIX = "spiffe://grid.internal/site/"


@dataclass
class Result:
    """What one attempt produced: a refusal before HTTP, or an HTTP response."""

    outcome: str  # "http", "handshake_failed", "closed_no_response", "connect_failed"
    detail: str = ""
    status: int = 0
    headers: dict = field(default_factory=dict)
    body: str = ""
    server_spiffe: Optional[str] = None
    received_at: float = 0.0


def spiffe_ids(der: bytes) -> list:
    """URI SANs of a DER certificate."""
    cert = x509.load_der_x509_certificate(der)
    try:
        san = cert.extensions.get_extension_for_class(x509.SubjectAlternativeName).value
    except x509.ExtensionNotFound:
        return []
    return [u for u in san.get_values_for_type(x509.UniformResourceIdentifier) if u.startswith("spiffe://")]


def request(
    host: str,
    port: int,
    ca: Optional[str],
    cert: Optional[str] = None,
    key: Optional[str] = None,
    path: str = SIGNALS_PATH,
    method: str = "GET",
    timeout: float = 5.0,
    plaintext: bool = False,
) -> Result:
    """Send one request and classify what came back."""
    try:
        sock = socket.create_connection((host, port), timeout=timeout)
    except OSError as err:
        return Result("connect_failed", str(err))
    stream = sock
    server_spiffe = None
    if not plaintext:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        # The server is named by its SPIFFE ID, checked below, not by the dialed IP.
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_REQUIRED
        ctx.load_verify_locations(cafile=ca)
        if cert:
            ctx.load_cert_chain(cert, key)
        try:
            stream = ctx.wrap_socket(sock, server_hostname=host)
        except (ssl.SSLError, OSError) as err:
            sock.close()
            return Result("handshake_failed", str(err))
        der = stream.getpeercert(binary_form=True)
        ids = spiffe_ids(der) if der else []
        server_spiffe = ids[0] if ids else None
    try:
        stream.sendall(f"{method} {path} HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n".encode())
        chunks = []
        while True:
            chunk = stream.recv(65536)
            if not chunk:
                break
            chunks.append(chunk)
    except (ssl.SSLError, OSError) as err:
        # TLS 1.3 reports a client-certificate refusal on the first read, not in the handshake.
        stream.close()
        return Result("closed_no_response", str(err), server_spiffe=server_spiffe)
    stream.close()
    raw = b"".join(chunks)
    if not raw:
        return Result("closed_no_response", "connection closed with no HTTP response", server_spiffe=server_spiffe)
    head, _, body = raw.partition(b"\r\n\r\n")
    lines = head.decode("latin-1").split("\r\n")
    match = re.match(r"HTTP/1\.[01] (\d{3})", lines[0])
    if not match:
        return Result("closed_no_response", f"not HTTP: {lines[0][:60]!r}", server_spiffe=server_spiffe)
    headers = {}
    for line in lines[1:]:
        name, _, value = line.partition(":")
        headers[name.strip().lower()] = value.strip()
    if headers.get("transfer-encoding", "").lower() == "chunked":
        body = dechunk(body)
    return Result(
        "http",
        status=int(match.group(1)),
        headers=headers,
        body=body.decode("utf-8", "replace"),
        server_spiffe=server_spiffe,
        received_at=time.time(),
    )


def dechunk(data: bytes) -> bytes:
    """Decode an HTTP/1.1 chunked body."""
    out, rest = b"", data
    while rest:
        size_line, _, rest = rest.partition(b"\r\n")
        size = int(size_line.split(b";")[0] or b"0", 16)
        if size == 0:
            break
        out, rest = out + rest[:size], rest[size + 2 :]
    return out


SAMPLE = re.compile(r"^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})?\s+(\S+)(?:\s+(-?\d+))?$")
LABEL = re.compile(r'\s*([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"\s*,?')


@dataclass
class Sample:
    name: str
    labels: dict
    value: str
    timestamp_ms: Optional[int]
    line: str


def parse_exposition(body: str) -> tuple:
    """Samples, and lines that are neither samples nor blank."""
    samples, bad = [], []
    for line in body.splitlines():
        if not line.strip():
            continue
        match = SAMPLE.match(line)
        if not match:
            bad.append(line)
            continue
        labels, rest = {}, match.group(2) or ""
        while rest:
            lm = LABEL.match(rest)
            if not lm:
                bad.append(line)
                break
            labels[lm.group(1)] = lm.group(2)
            rest = rest[lm.end() :]
        ts = match.group(4)
        samples.append(Sample(match.group(1), labels, match.group(3), int(ts) if ts else None, line))
    return samples, bad


def date_epoch(headers: dict) -> Optional[float]:
    """The response Date header as epoch seconds."""
    try:
        return parsedate_to_datetime(headers["date"]).timestamp()
    except (KeyError, TypeError, ValueError):
        return None
