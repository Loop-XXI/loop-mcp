# Comparison — original x402-MCP vs Loop MCP

This project is a faithful replica of the open-source **x402-MCP** transport
([`@civic/x402-mcp`](https://github.com/civicteam/x402-mcp)), rebranded for
Loop XXI and extended with one material addition: a **second payment rail**
(Lightning / L402) and a **unified rail-tagged canonical ledger**.

## What was replicated faithfully

| Surface | Original (`@civic/x402-mcp`) | Loop MCP |
|---|---|---|
| Server transport | `makePaymentAwareServerTransport(payTo, toolPricing, opts)` | identical signature + same `X402StreamableHTTPServerTransport` mechanics (subclass of `StreamableHTTPServerTransport`, `enableJsonResponse` default, 402 challenge, `X-PAYMENT` decode via `exact.evm.decodePayment`, facilitator verify/settle, `X-PAYMENT-RESPONSE` header injection) |
| Client transport | `makePaymentAwareClientTransport(url, wallet, cb)` | identical, using `x402-fetch` `wrapFetchWithPayment` + the `convertHeaders` workaround for the x402-fetch Headers bug |
| Client proxy CLI | `npx @civic/x402-mcp client-proxy` (stdio/http) | `npx loop-mcp client-proxy` (stdio/http) — same env (`TARGET_URL`, `PRIVATE_KEY`, `MODE`, `PORT`, `NETWORK`) |
| Server proxy | `createServerProxy({ upstreamUrl, apiKey, paymentWallet, toolPricing, port })` | identical surface, wraps an API-key-protected upstream behind per-tool payment |
| Client proxy | `createClientProxy({ targetUrl, wallet, mode, onPayment })` | identical surface |
| Networks | `base`, `baseSepolia`, `polygon`, `arbitrum`, … via viem chains | same |
| Facilitator | pluggable `FacilitatorConfig` (default Coinbase) | same |
| Testnet demo server | hosted fly.dev todo app | equivalent dual-rail todo example |

## The one addition: dual-rail + canonical ledger

Everything below is **new in Loop MCP** and is clearly marked `[LOOP ADDITION]`
in the source.

### 1. Lightning / L402 as a first-class second rail

A paid tool's `402` response now advertises **both** rails in `accepts`:

```jsonc
{
  "x402Version": 1,
  "error": "Payment required",
  "accepts": [
    { "rail": "x402", "scheme": "exact", "network": "base-sepolia",
      "maxAmountRequired": "1000", "asset": "0x036CbD…", "payTo": "0x…" },
    { "rail": "l402", "invoice": "lnbcrt2n1…", "macaroon": "…",
      "paymentHash": "…", "amount": "2", "unit": "sats" }
  ]
}
```

and mirrors the L402 leg in a standard `WWW-Authenticate: L402 macaroon="…", invoice="…"` header so Lightning-native clients can pay too.

**Verification** uses the classic L402 invariant — `sha256(preimage) == payment_hash` — where the payment hash is bound into an HMAC-signed macaroon alongside the tool name, amount, and expiry. This means a preimage from a cheap invoice *cannot* be replayed against a pricier tool.

### 2. A pluggable Lightning provider

```
LightningProvider  ──  MockLightningProvider   (deterministic, for dev/tests)
                  ──  PhoenixdProvider          (Loop XXI's hot node on Railway)
                  ──  (LND / CLN / custodial)  (drop-in)
```

The transport never holds a Lightning node directly, so phoenixd can be swapped for any implementation without touching payment logic.

### 3. 21% Bitcoin-payment discount

Loop XXI's real-world sats-quote doctrine is baked in: Lightning/L402 payments ≤ $100 get a **21% discount** (`pricing/satsQuote.ts`), matching the Loop Gateway production behaviour.

### 4. Canonical rail-tagged ledger

Every settled payment — regardless of rail — lands in one `LoopLedger` row tagged with its `rail`, `amount`, `unit`, `usd`, and `proof` (tx hash for x402, preimage for L402). This is the in-product mirror of Loop's production `loop_sats_ledger` ground truth:

- `canonicalRows()` excludes `internal:*`-tagged rows (synthetic/test), matching the gateway's first-revenue safeguards.
- `totals()` rolls up USD by rail and in aggregate, toward Loop's net-sats north-star.

### 5. Rail-tagged settlement receipts

Tool results carry a `loopSettlement` receipt instead of the x402-only `x402Settlement`, exposing `rail` so callers and operators can attribute revenue to the right rail.

## Why it fits Loop XXI

- Loop's durable doctrine is **dual-rail** (fiat/stablecoin + Bitcoin/Lightning) unified through one canonical, rail-tagged ledger. The original product is single-rail (x402/USDC only); the addition completes the doctrine.
- Loop's documented research found the agent market standardized on x402/USDC on Base, and that **zero L402-native MCP servers exist** — so `loop-mcp` (an L402-native MCP payment proxy) is already Loop's top-priority new build. This replica *is* that product, implemented by studying and extending the closest open reference.
- Branding is Loop XXI: package `@loop-xxi/loop-mcp`, repo `Loop-XXI/loop-mcp`, MIT © 2026 Loop XXI LLC, neutral parent voice, no accent color, the 21% sats discount as a product signature.

## Proven without a chain

The dual-rail payment core is dependency-free and was executed end-to-end (`scripts/demo-core.mjs`):

```
=== Loop MCP dual-rail core demo ===
✅ valid preimage verifies
✅ forged preimage rejected
✅ cross-tool replay rejected
✅ wrong-secret macaroon rejected
✅ x402 atomic amount = 2000
✅ canonical rows = 2 (internal excluded)
✅ canonical USD roll-up = 0.003
=== 7 passed, 0 failed ===
```

## Attribution

Derived from the open x402-MCP transport design (MIT, Civic). "x402" is a Coinbase open standard; "L402" is a Lightning Labs standard. Loop MCP is an independent implementation owned by Loop XXI LLC.
