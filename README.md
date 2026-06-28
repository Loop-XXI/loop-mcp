# Loop MCP — dual-rail payments for the Model Context Protocol

**Charge AI agents per tool call over either rail — Bitcoin/Lightning *or* USDC — and settle both into one canonical ledger.**

`@loop-xxi/loop-mcp` is a payment-aware transport for [MCP](https://modelcontextprotocol.io). Drop it in front of an MCP server and every paid tool returns `HTTP 402 Payment Required`; a paying client retries and the call goes through. Built and maintained by **Loop XXI LLC**.

It is API-compatible with the popular x402-only MCP transport, with one material addition:

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
