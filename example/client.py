"""
loop-mcp — minimal Python reference client for the L402 flow.

Zero-dependency in the stdlib path (urllib). Two optional payment paths:
  1) External wallet: you supply the preimage after paying the BOLT11 yourself.
  2) Alby: if ALBY_TOKEN is set, the client pays the invoice via the Alby
     hub API and extracts the preimage automatically.

MIT-licensed. Copy-paste and edit for your own L402 server.

Author: Loop XXI LLC · business@loopxxi.com
"""
from __future__ import annotations
import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request
from typing import Optional, Tuple

BASE = "https://mcp.loopxxi.com"
# The directory-friendly REST endpoint; the same paywall guards the MCP endpoint.
DEFAULT_TOOL_URL = f"{BASE}/l402/btc_price"


class L402Error(Exception):
    pass


def _http(url: str, headers: Optional[dict] = None, method: str = "GET",
          data: Optional[bytes] = None, timeout: int = 20) -> Tuple[int, dict, bytes]:
    req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
    try:
        resp = urllib.request.urlopen(req, timeout=timeout)
        return resp.status, dict(resp.headers.items()), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers.items()), e.read()


def request_challenge(url: str) -> dict:
    """
    Step 1 — hit the paywalled endpoint with no Authorization.
    Expect HTTP 402 + a JSON body with `token`, `payment_request`, `sats`.
    """
    status, headers, body = _http(url)
    if status != 402:
        raise L402Error(f"expected 402, got {status}: {body!r}")
    try:
        payload = json.loads(body.decode("utf-8"))
    except Exception as e:
        raise L402Error(f"invalid 402 body: {e}: {body!r}") from e
    if "token" not in payload or "payment_request" not in payload:
        raise L402Error(f"402 missing token/payment_request: {payload}")
    payload["_www_authenticate"] = headers.get("Www-Authenticate") or headers.get("WWW-Authenticate")
    return payload


def pay_with_alby(invoice: str, alby_token: str) -> str:
    """
    Optional — pay a BOLT11 via Alby's hub API. Requires ALBY_TOKEN with
    `payments:send` scope. Returns the 64-hex preimage.
    """
    req = urllib.request.Request(
        "https://api.getalby.com/payments/bolt11",
        method="POST",
        data=json.dumps({"invoice": invoice}).encode(),
        headers={
            "Authorization": f"Bearer {alby_token}",
            "Content-Type": "application/json",
        },
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        payload = json.loads(resp.read())
    pre = payload.get("payment_preimage") or payload.get("preimage")
    if not pre or not re.fullmatch(r"[0-9a-fA-F]{64}", pre):
        raise L402Error(f"alby returned no valid preimage: {payload}")
    return pre.lower()


def replay_with_preimage(url: str, token: str, preimage: str) -> Tuple[int, bytes]:
    """
    Step 3 — retry the same request with `Authorization: L402 <token>:<preimage>`.
    """
    if not re.fullmatch(r"[0-9a-fA-F]{64}", preimage):
        raise L402Error("preimage must be exactly 64 hex chars")
    status, _headers, body = _http(url, headers={
        "Authorization": f"L402 {token}:{preimage}",
    })
    return status, body


def call_paid_tool(url: str = DEFAULT_TOOL_URL,
                   preimage: Optional[str] = None,
                   alby_token: Optional[str] = None) -> dict:
    """
    One-shot: challenge → (pay | supplied preimage) → replay.

    - If `preimage` is supplied, it's used directly (external wallet flow).
    - Else if `alby_token` (or env ALBY_TOKEN) is set, the invoice is paid
      via Alby and the preimage is extracted.
    - Otherwise raises with the invoice so a human/agent can pay it.
    """
    challenge = request_challenge(url)
    token = challenge["token"]
    invoice = challenge["payment_request"]
    sats = challenge.get("sats")

    if preimage is None:
        alby = alby_token or os.environ.get("ALBY_TOKEN")
        if alby:
            preimage = pay_with_alby(invoice, alby)
        else:
            raise L402Error(
                f"payment required: {sats} sats. Pay this BOLT11 and rerun with --preimage:\n{invoice}"
            )

    status, body = replay_with_preimage(url, token, preimage)
    if status != 200:
        raise L402Error(f"replay failed ({status}): {body!r}")
    return json.loads(body.decode("utf-8"))


def _cli() -> None:
    p = argparse.ArgumentParser(description="loop-mcp L402 reference client")
    p.add_argument("--url", default=DEFAULT_TOOL_URL, help="paid endpoint (default: %(default)s)")
    p.add_argument("--preimage", help="preimage from your wallet after paying")
    p.add_argument("--alby", help="Alby token (overrides ALBY_TOKEN env)")
    p.add_argument("--challenge-only", action="store_true", help="just print the 402 body and exit")
    args = p.parse_args()

    if args.challenge_only:
        ch = request_challenge(args.url)
        print(json.dumps(ch, indent=2))
        return

    try:
        result = call_paid_tool(args.url, preimage=args.preimage, alby_token=args.alby)
        print(json.dumps(result, indent=2))
    except L402Error as e:
        print(f"L402: {e}", file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    _cli()
