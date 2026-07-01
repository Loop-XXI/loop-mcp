# Installing loop-mcp in Cline

`loop-mcp` is a **remote HTTPS MCP server**. There is nothing to build, clone, or run locally — you point Cline at the hosted endpoint and start using it. Payment is per-tool-call over either Lightning (L402) or a prepaid fiat credit key.

## 1. Add the server to Cline

Open Cline → **MCP Servers** → **Configure MCP Servers** and add the following entry to `cline_mcp_settings.json`:

```json
{
  "mcpServers": {
    "loop-mcp": {
      "url": "https://mcp.loopxxi.com/mcp",
      "transport": "streamable-http"
    }
  }
}
```

Save the file. Cline will connect to the remote server and list the five available tools:

| Tool | Sats | What it returns |
|---|---|---|
| `btc_price` | 10 | Current BTC price (mempool.space) |
| `btc_send_decision` | 15 | SEND_NOW / WAIT / URGENT_ONLY verdict + fee rates |
| `lightning_address_resolve` | 10 | Resolve Lightning Address → BOLT11 invoice |
| `tx_decode_explain` | 25 | Structured, agent-ready TX summary |
| `optimal_send_window` | 25 | Timing intelligence + fee trajectory |

## 2. Try a free call (no payment needed)

Ask Cline: *"Call the free-try endpoint at `https://mcp.loopxxi.com/try/btc_price` and show me the response."*

This returns the current Bitcoin price with no wallet or credit key attached — a read-only preview so you can see a tool's shape before wiring up payment.

## 3. Pay per call — two options

**Option A: Lightning (L402).** Cline attempts a paid tool. The server returns `HTTP 402` with a BOLT11 invoice and an L402 token. Pay the invoice with any Lightning wallet (Phoenix, Zeus, Wallet of Satoshi, LNbits, etc.), then retry the same request with:

```
Authorization: L402 <token>:<preimage>
```

`<preimage>` is the payment preimage returned by your wallet after paying. The server verifies the preimage statelessly and serves the result. 10–25 sats per call.

**Option B: Prepaid fiat credit key.** If you'd rather pay by card once and forget it, buy a credit pack at [`api.loopxxi.com/ai-credits`](https://api.loopxxi.com/ai-credits) ($10 / $25 / $50 → sats at the live BTC price). You receive a key of the form `loop_...`. Configure Cline to send:

```
Authorization: Bearer loop_<your_key>
```

Same per-call sats price, deducted from your prepaid balance.

## 4. Verify it's working

```
curl -i https://mcp.loopxxi.com/l402/btc_price
```

This should return `HTTP 402` with a `WWW-Authenticate: L402 ...` header and a BOLT11 invoice — confirmation the paywall is live.

## Notes for Cline

- **No API keys to configure.** Payment *is* the credential; no signup, no dashboard.
- **Nothing to install locally.** Do not attempt to `git clone` or `npm install`. This is a hosted MCP server.
- **MIT license, source public** at [Loop-XXI/loop-mcp](https://github.com/Loop-XXI/loop-mcp) — reference implementation for anyone who wants to run their own L402 MCP proxy.
- **Contact:** [business@loopxxi.com](mailto:business@loopxxi.com) (Loop XXI LLC).
