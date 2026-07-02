# loop-mcp — reference clients

Minimal, zero-runtime-dependency reference clients for the live `mcp.loopxxi.com` deployment.

- `loop_mcp_l402_client.py` — Python 3.9+ (uses only the standard library)
- `loop_mcp_l402_client.ts` — TypeScript / Node 18+ (uses built-in `fetch`)
- `fiat-credit-client.ts` — TypeScript / Node 18+ (uses built-in `fetch`)

## L402 clients (Lightning)

Both L402 clients implement the same three steps:

```
1. GET /l402/btc_price                       →  HTTP 402 + BOLT11 invoice + token
2. Pay the invoice (any Lightning wallet)    →  preimage (64-hex)
3. GET /l402/btc_price
   Authorization: L402 <token...age>    →  HTTP 200 + tool result
```

### Python — quickstart

```bash
# Step 1 only — inspect the 402 challenge
python loop_mcp_l402_client.py --challenge-only

# Step 3 — supply a preimage from your wallet
python loop_mcp_l402_client.py --preimage <64-hex-from-wallet>

# Fully automatic via Alby (payments:send scope)
export ALBY_TOKEN=…
python loop_mcp_l402_client.py
```

### TypeScript — quickstart

```bash
npx tsx loop_mcp_l402_client.ts --challenge-only
npx tsx loop_mcp_l402_client.ts --preimage <64-hex-from-wallet>
ALBY_TOKEN=… npx tsx loop_mcp_l402_client.ts
```

Or import as a library:

```ts
import { callPaidTool } from "./loop_mcp_l402_client";

const result = await callPaidTool({ preimage: "…64hex…" });
console.log(result); // → { usd: 68941.23, … }
```

### Notes

- The token has the shape `<paymentHash>:<tool>:<expiry>:<hmac>`. Split the final header on the **last** colon — the preimage is a colon-free 64-char hex, but the token itself contains colons.
- Verification is stateless: the server SHA-256s your preimage and compares against the payment hash embedded (and HMAC-signed) inside the token. No sessions, no accounts.
- The same paywall protects the streamable-HTTP MCP endpoint at `/mcp`. Agents wired to `mcp.loopxxi.com/mcp` get the exact same 402 → pay → retry loop on every `tools/call`.

## Fiat-credit client (Stripe)

No Lightning wallet required. Buy a credit key at https://api.loopxxi.com/ai-credits, then call the MCP endpoint with `Authorization: Bearer loop_<credit_key>`. The server debits your prepaid sats balance and returns the tool result in one request.

```bash
# Buy credits at https://api.loopxxi.com/ai-credits
export LOOP_CREDIT_KEY=loop_<credit_key>
npx tsx example/fiat-credit-client.ts
```

With a depleted or invalid key the client prints a refill message pointing back to the credit top-up URL.

Or import as a library:

```ts
import { callToolWithCredit } from "./fiat-credit-client";

const result = await callToolWithCredit({ creditKey: "loop_..." });
console.log(result);
```

MIT-licensed. Maintained by Loop XXI LLC · business@loopxxi.com.
