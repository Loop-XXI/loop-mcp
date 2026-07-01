# Loop MCP — dual-rail payments for the Model Context Protocol

**Charge AI agents per tool call over either rail — Bitcoin/Lightning *or* USDC — and settle both into one canonical ledger.**

`@loop-xxi/loop-mcp` is a payment-aware transport for [MCP](https://modelcontextprotocol.io). Drop it in front of an MCP server and every paid tool returns `HTTP 402 Payment Required`; a paying client retries and the call goes through. Built and maintained by **Loop XXI LLC**.

It is API-compatible with the popular x402-only MCP transport, with one material addition:

> ![L402-paid](https://img.shields.io/badge/tools-L402%20paid-ff9900?logo=lightning&logoColor=white)
> [![Live](https://img.shields.io/badge/endpoint-live-success)](https://mcp.loopxxi.com/health)
> [![MCP Registry](https://img.shields.io/badge/registry-com.loopxxi%2Floop--mcp-blue)](https://registry.modelcontextprotocol.io/v0/servers?search=loop-mcp)
> [![L402 Playground](https://img.shields.io/badge/try-L402%20Playground-ff9900)](https://loop-xxi.github.io/loop-mcp/example/playground/)
> [![Reference clients](https://img.shields.io/badge/reference-Py%20%2B%20TS-blue)](https://github.com/Loop-XXI/loop-mcp/tree/main/example)


## Live deployment (v2.2.0)

A **live, hosted** deployment is running with **5 paid tools** on **two payment rails**: Lightning (L402) and fiat-funded credits via Stripe. No API keys, no signup — payment **is** the credential. A branded landing page and a free try endpoint are at `https://mcp.loopxxi.com/`.

**Endpoint:** `https://mcp.loopxxi.com/mcp` · **Health:** `https://mcp.loopxxi.com/health` · **Landing:** `https://mcp.loopxxi.com/`

| Tool | Sats | What it returns |
|---|---|---|
| `btc_price` | 10 | Current Bitcoin price in USD + major fiat currencies (mempool.space). |
| `btc_send_decision` | 15 | A SEND_NOW / WAIT / URGENT_ONLY verdict with fee rates (sat/vB), mempool pressure, and estimated savings — one decision call instead of parsing multiple mempool endpoints. |
| `lightning_address_resolve` | 10 | Resolve a Lightning Address (`user@domain.com`) to a payable BOLT11 for a given amount — the full LNURL-pay flow in one call. |
| `tx_decode_explain` | 25 | Decode a Bitcoin tx by txid into a structured agent summary: type, fee, fee rate, confirmation status, RBF/SegWit/Taproot flags, and a one-line `agent_summary` ready for LLM context. Saves 500–2,000 tokens vs raw JSON. |
| `optimal_send_window` | 25 | Bitcoin transaction timing intelligence: recommended send window, fee trajectory, congestion forecast, confirmation targets, and RBF viability from live mempool data. |

### Two payment rails

Agents pay per `tools/call` via either rail:

- **Lightning (L402)** — `Authorization: L402 <token>:<preimage>`. 10–25 sats/call across 5 tools. The default; no account needed.
- **Fiat credits (Stripe)** — `Authorization: Bearer loop_<credit_key>`. Buy a credit key at [`api.loopxxi.com/ai-credits`](https://api.loopxxi.com/ai-credits) ($10/$25/$50 packs → sats at the live BTC price). loop-mcp forwards the key to Loop Gateway's `POST /v1/credits/debit`, which atomically debits the prepaid sats ledger. Same 1:1 sats pricing as L402.

A request with no auth returns `HTTP 402` with a Lightning invoice (L402) and, in the body, a pointer to the fiat refill URL. Insufficient fiat balance returns `402` with `refill_url: https://api.loopxxi.com/ai-credits`.

### Free try (no wallet required)

`POST https://mcp.loopxxi.com/try/btc_price` returns the live Bitcoin price for free — a read-only lead-gen endpoint so you can see a tool's output before wiring up payment.

### How to call (L402 flow)

The agent flow is three HTTP calls: **challenge → pay → retry**. On the first `tools/call` with no payment, the server returns `HTTP 402` with a BOLT11 invoice and an L402 token. Pay the invoice over Lightning, then retry the same request with `Authorization: L402 <token>:<preimage>`. The server verifies the preimage statelessly and serves the result. No database, no session.

> **Parsing note:** the L402 token is colon-delimited (`<paymentHash>:<tool>:<ts>:<hmac>`). The final header is `L402 <token>:<preimage>` — split on the **last** colon, since the preimage is a colon-free 64-char hex string.

#### curl

```bash
ENDPOINT="https://mcp.loopxxi.com/mcp"
BODY='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"btc_price","arguments":{}}}'

# 1. Challenge — expect HTTP 402 with an invoice.
RESP=$(curl -s -w "\n%{http_code}" -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d "$BODY")
HTTP_CODE=$(echo "$RESP" | tail -1)
BODY_JSON=$(echo "$RESP" | sed '$d')

# 2. Extract the token + BOLT11 invoice from the 402 body.
TOKEN=$(echo "$BODY_JSON" | grep -o '"token":"[^"]*"' | head -1 | cut -d'"' -f4)
INVOICE=$(echo "$BODY_JSON" | grep -o '"payment_request":"[^"]*"' | head -1 | cut -d'"' -f4)

# 3. Pay the invoice with any Lightning wallet, then retry with the L402 header.
#    (replace <PREIMAGE> with the 64-char hex preimage your wallet returns)
curl -s -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: L402 ${TOKEN}:<PREIMAGE>" \
  -d "$BODY"
```

#### JavaScript / Node (fetch)

```js
const ENDPOINT = "https://mcp.loopxxi.com/mcp";
const callTool = {
  jsonrpc: "2.0", id: 1, method: "tools/call",
  params: { name: "btc_price", arguments: {} },
};

// Replace with your Lightning wallet — must return the 64-char hex preimage.
async function payBolt11(invoice) { throw new Error("implement payBolt11"); }

// 1. Challenge → 402.
const res1 = await fetch(ENDPOINT, {
  method: "POST",
  headers: { "Content-Type": "application/json", "Accept": "application/json, text/event-stream" },
  body: JSON.stringify(callTool),
});
const { token, payment_request } = (await res1.json()).error;

// 2. Pay the invoice, get the preimage.
const preimage = await payBolt11(payment_request);

// 3. Retry with Authorization: L402 <token>:<preimage> (split on the LAST colon).
const res2 = await fetch(ENDPOINT, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "Accept": "application/json, text/event-stream",
    "Authorization": "L402 " + token + ":" + preimage,
  },
  body: JSON.stringify(callTool),
});
console.log(await res2.json());
```

#### Fiat credits (Stripe) — no Lightning wallet needed

Buy a credit key at [`api.loopxxi.com/ai-credits`](https://api.loopxxi.com/ai-credits), then send it as a Bearer token. loop-mcp debits your prepaid sats balance on Loop Gateway and serves the result in a single call — no challenge/pay/retry round-trip.

```bash
# Buy a $10 pack → get a loop_<credit_key> (1000 credits → sats at live BTC price)
curl -s -X POST https://api.loopxxi.com/v1/credits/checkout \
  -H "Content-Type: application/json" -d '{"pack":"p10"}'
# → {"credit_key":"loop_...","checkout_url":"https://buy.stripe.com/..."}
# Complete payment on the Stripe page, then:

curl -s -X POST https://mcp.loopxxi.com/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer loop_<credit_key>" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"btc_price","arguments":{}}}'
```

The live deployment source is in [`cmd/loop-mcp/`](./cmd/loop-mcp) and [`tools/`](./tools) (Go, dual-rail: L402 + fiat credit_key). The dual-rail npm library below is the broader transport.


> ### The addition: a second rail — Lightning / L402
> Most agent-payment tooling speaks only **x402 / USDC on Base**. Loop MCP adds the **Lightning Network via [L402](https://github.com/lightninglabs/L402)** as a first-class second rail, negotiated in the *same* `402` response and reconciled into a single **rail-tagged canonical ledger**. As of this writing, **zero** L402-native MCP servers exist in the public MCP registry — this closes that gap.

```
                          ┌──────────────── 402 Payment Required ───────────────┐
   agent / MCP client ───▶│  accepts: [ x402 (USDC on Base),  l402 (Lightning) ] │
                          └──────────────────────────────────────────────────────┘
                                   │                              │
                pay USDC + retry   │                              │  pay invoice + retry
              (X-PAYMENT header)   ▼                              ▼  (Authorization: L402 mac:preimage)
                          ┌─────────────────────────────────────────────┐
                          │      LoopDualRailServerTransport             │
                          │  verify ─▶ run tool ─▶ settle ─▶ LoopLedger  │
                          └─────────────────────────────────────────────┘
                                   │  rail-tagged rows (x402 | l402)
                                   ▼
                            canonical revenue (net sats north-star)
```

## Why dual-rail

| | x402 / USDC | L402 / Lightning *(the addition)* |
|---|---|---|
| Settlement | USDC on Base/EVM | Bitcoin over Lightning |
| Proof | EVM tx hash via facilitator | `sha256(preimage) == payment_hash` |
| Header in | `X-PAYMENT` | `Authorization: L402 <macaroon>:<preimage>` |
| Best for | EVM-native agents, stablecoin treasuries | sub-cent micropayments, Bitcoin-native flows |
| Discount | face value | **21% Bitcoin-payment discount ≤ $100** |

Both rails resolve to one **`LoopLedger`** with a `rail` tag on every row, so x402 and L402 revenue roll up into a single number. Synthetic/test rows tagged `internal:*` are excluded from canonical revenue.

## Install

```bash
npm install @loop-xxi/loop-mcp
# peer dependency
npm install @modelcontextprotocol/sdk
```

## Server — make any MCP tool payable on both rails

```ts
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import {
  makePaymentAwareServerTransport,
  PhoenixdProvider,
  LoopLedger,
} from "@loop-xxi/loop-mcp";

const server = new McpServer({ name: "my-server", version: "1.0.0" });
server.tool("get-quote", "Premium market data", { /* schema */ }, async () => ({
  content: [{ type: "text", text: "..." }],
}));

const transport = makePaymentAwareServerTransport(
  "0xYourReceivingAddress",            // x402 / USDC payouts
  { "get-quote": "$0.01" },            // per-tool USD price
  {
    network: "base",
    ledger: new LoopLedger(),
    // The addition — enable the Lightning / L402 rail:
    lightning: {
      provider: new PhoenixdProvider(process.env.PHOENIXD_URL!, process.env.PHOENIXD_PASSWORD!),
      l402Secret: process.env.L402_SECRET!,
      btcUsdPrice: 65000,              // live spot, injected
    },
  }
);

await server.connect(transport);
```

If you omit `lightning`, you get the original single-rail (x402-only) behaviour.

## Client — pay automatically over your preferred rail

```ts
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { makePaymentAwareClientTransport } from "@loop-xxi/loop-mcp";

const transport = makePaymentAwareClientTransport(serverUrl, wallet, {
  preferredRail: "l402",               // or "x402"
  lightningPayer,                      // pays the invoice, returns the preimage
  paymentCallback: (proof, rail) => console.log(`paid via ${rail}: ${proof}`),
});

const client = new Client({ name: "agent", version: "1.0.0" }, { capabilities: {} });
await client.connect(transport);       // tool calls now pay automatically
```

## Proxies (no code changes)

- **Server proxy** — wrap an existing API-key-protected MCP server with dual-rail payments: `createServerProxy({ upstreamUrl, apiKey, paymentWallet, toolPricing, lightning })`.
- **Client proxy / CLI** — let a non-paying client (e.g. Claude Desktop) pay on your behalf:

```bash
TARGET_URL=https://server/mcp PRIVATE_KEY=0x... RAIL=l402 npx loop-mcp client-proxy
```

## How it works

1. **Challenge.** A priced `tools/call` with no payment returns `402` whose `accepts` array carries *both* an x402 requirement and an L402 offer (Lightning invoice + macaroon). The L402 leg is also mirrored in a standard `WWW-Authenticate: L402 ...` header.
2. **Pay.** The client pays either rail and retries: `X-PAYMENT` (x402) or `Authorization: L402 <macaroon>:<preimage>` (L402).
3. **Verify.** x402 is verified/settled through an x402 facilitator. L402 is verified by checking the macaroon HMAC and `sha256(preimage) == payment_hash` — the classic Lightning proof-of-payment.
4. **Settle + record.** On a successful tool result the payment is settled and written to `LoopLedger` with its `rail` tag and a USD-equivalent for cross-rail roll-up. A settlement receipt is attached to the tool result (`loopSettlement`) and `X-PAYMENT-RESPONSE`.

## Verify the core without any chain

The dual-rail payment core (L402 macaroon + preimage, x402 atomic conversion, 21% sats quote, canonical ledger) is dependency-free and runs anywhere:

```bash
node scripts/demo-core.mjs
# ✅ valid preimage verifies
# ✅ forged preimage rejected
# ✅ cross-tool replay rejected
# ✅ wrong-secret macaroon rejected
# ✅ x402 atomic amount = 2000
# ✅ canonical rows = 2 (internal excluded)
# ✅ canonical USD roll-up = 0.003
# === 7 passed, 0 failed ===
```

Unit tests: `npm test` (vitest).

## Layout

```
src/
  server.ts            LoopDualRailServerTransport (x402 port + L402 addition)
  client.ts            dual-rail client transport
  rails/x402.ts        USDC/x402 helpers (dependency-free)
  rails/l402.ts        L402 macaroon + preimage verification (dependency-free)
  lightning/provider.ts MockLightningProvider + PhoenixdProvider
  pricing/satsQuote.ts  USD→sats with 21% BTC discount ≤ $100
  ledger.ts            canonical rail-tagged ledger
  proxy/index.ts       client & server proxies
example/               dual-rail "todo" MCP server + client
scripts/demo-core.mjs  zero-dependency end-to-end proof of the payment core
test/core.test.ts      unit tests
```

## Security notes

- Never commit private keys, `L402_SECRET`, or phoenixd passwords. Use env vars.
- Use testnets (`base-sepolia`, regtest) during development.
- L402 macaroons bind the payment hash to the specific tool, amount and an expiry, so a cheap invoice's preimage cannot be replayed against a pricier tool.
- Treat ledger rows as canonical only after reconciliation; tag synthetic rows `internal:*`.

---

MIT © 2026 Loop XXI LLC · https://loopxxi.com

*Derived from the open x402-MCP transport design and extended with a Lightning/L402 rail and a unified rail-tagged ledger. "x402" is a Coinbase open standard; "L402" is a Lightning Labs standard.*
