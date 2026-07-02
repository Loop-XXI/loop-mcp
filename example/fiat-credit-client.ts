/**
 * loop-mcp — fiat-credit reference client (Stripe-funded prepaid credits).
 *
 * Zero runtime dependencies. Uses the built-in `fetch` available in Node 18+,
 * Deno, Bun, and modern browsers.
 *
 * 1. Buy a credit pack at https://api.loopxxi.com/ai-credits
 *    → receive `loop_<credit_key>`.
 * 2. Set `LOOP_CREDIT_KEY` (or pass --credit-key) and call the hosted MCP
 *    endpoint. The server debits your prepaid sats balance and returns the
 *    tool result in a single round-trip — no Lightning wallet needed.
 *
 * MIT-licensed. Copy-paste and edit for your own MCP client.
 *
 * Author: Loop XXI LLC · business@loopxxi.com
 */

export const ENDPOINT = "https://mcp.loopxxi.com/mcp";

export interface CallOptions {
  endpoint?: string;
  creditKey?: string;
  tool?: string;
  arguments?: Record<string, unknown>;
}

export class FiatCreditError extends Error {
  status?: number;
  body?: string;
  constructor(message: string, status?: number, body?: string) {
    super(message);
    this.name = "FiatCreditError";
    this.status = status;
    this.body = body;
  }
}

export interface McpToolResult {
  content?: Array<{ type: string; text?: string }>;
  isError?: boolean;
  [key: string]: unknown;
}

export async function callToolWithCredit(
  opts: CallOptions = {},
): Promise<McpToolResult> {
  const endpoint = opts.endpoint ?? ENDPOINT;
  const creditKey = opts.creditKey ?? (globalThis as any).process?.env?.LOOP_CREDIT_KEY;
  const tool = opts.tool ?? "btc_price";
  const args = opts.arguments ?? {};

  if (!creditKey || typeof creditKey !== "string" || !creditKey.startsWith("loop_")) {
    throw new FiatCreditError(
      "Missing LOOP_CREDIT_KEY. Buy credits at https://api.loopxxi.com/ai-credits " +
        "and set a key with the `loop_` prefix, e.g. `export LOOP_CREDIT_KEY=loop_xxxxx`.",
    );
  }

  const res = await fetch(endpoint, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Accept": "application/json, text/event-stream",
      "Authorization": `Bearer ${creditKey}`,
    },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: { name: tool, arguments: args },
    }),
  });

  const text = await res.text();
  let body: any = text;
  try {
    body = JSON.parse(text);
  } catch {
    /* leave as text */
  }

  if (res.status === 402) {
    const refill = body?.refill_url ?? "https://api.loopxxi.com/ai-credits";
    throw new FiatCreditError(
      `Insufficient credit balance. Refill at ${refill}`,
      res.status,
      text,
    );
  }

  if (!res.ok) {
    throw new FiatCreditError(
      `Request failed (${res.status})`,
      res.status,
      text,
    );
  }

  if (body?.error) {
    throw new FiatCreditError(
      `MCP error: ${JSON.stringify(body.error)}`,
      res.status,
      text,
    );
  }

  return body?.result ?? body;
}

/* -------------------------------------------------------------------------- */
/*                                    CLI                                     */
/* -------------------------------------------------------------------------- */

declare const require: any;
declare const module: any;

const isMain =
  typeof require !== "undefined" &&
  typeof module !== "undefined" &&
  require.main === module;

if (isMain) {
  const args = (globalThis as any).process?.argv?.slice(2) ?? [];
  const get = (k: string): string | undefined => {
    const i = args.indexOf(k);
    return i >= 0 ? args[i + 1] : undefined;
  };

  (async () => {
    try {
      const result = await callToolWithCredit({
        endpoint: get("--endpoint"),
        creditKey: get("--credit-key"),
        tool: get("--tool") ?? "btc_price",
      });
      console.log(JSON.stringify(result, null, 2));
    } catch (e) {
      const err = e as FiatCreditError;
      console.error(`FiatCredit: ${err.message}`);
      (globalThis as any).process?.exit?.(2);
    }
  })();
}
