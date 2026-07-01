/**
 * loop-mcp — minimal TypeScript reference client for the L402 flow.
 *
 * Zero runtime dependencies. Uses the built-in `fetch` available in Node 18+,
 * Deno, Bun, and modern browsers.
 *
 * Two payment paths:
 *   1) External wallet: caller pays the invoice and supplies the preimage.
 *   2) Alby: if ALBY_TOKEN is set, we pay via the Alby hub API and extract
 *      the preimage automatically.
 *
 * MIT-licensed. Copy-paste and edit for your own L402 server.
 *
 * Author: Loop XXI LLC · business@loopxxi.com
 */

export const BASE = "https://mcp.loopxxi.com";
export const DEFAULT_TOOL_URL = `${BASE}/l402/btc_price`;

export interface Challenge {
  token: string;
  payment_request: string;
  sats: number;
  message?: string;
  wwwAuthenticate?: string | null;
}

export class L402Error extends Error {
  constructor(message: string, public readonly status?: number, public readonly body?: string) {
    super(message);
    this.name = "L402Error";
  }
}

const HEX64 = /^[0-9a-fA-F]{64}$/;

export async function requestChallenge(url = DEFAULT_TOOL_URL): Promise<Challenge> {
  const res = await fetch(url, { method: "GET" });
  const text = await res.text();
  if (res.status !== 402) {
    throw new L402Error(`expected 402, got ${res.status}`, res.status, text);
  }
  let body: any;
  try { body = JSON.parse(text); }
  catch (e) { throw new L402Error(`invalid 402 body: ${(e as Error).message}`, 402, text); }
  if (!body.token || !body.payment_request) {
    throw new L402Error("402 missing token/payment_request", 402, text);
  }
  return {
    token: body.token,
    payment_request: body.payment_request,
    sats: body.sats,
    message: body.message,
    wwwAuthenticate: res.headers.get("www-authenticate"),
  };
}

export async function payWithAlby(invoice: string, albyToken: string): Promise<string> {
  const res = await fetch("https://api.getalby.com/payments/bolt11", {
    method: "POST",
    headers: {
      "Authorization": `Bearer ${albyToken}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ invoice }),
  });
  const payload: any = await res.json();
  const pre = payload.payment_preimage || payload.preimage;
  if (!pre || !HEX64.test(pre)) {
    throw new L402Error(`alby returned no valid preimage: ${JSON.stringify(payload)}`);
  }
  return pre.toLowerCase();
}

export async function replayWithPreimage(
  url: string,
  token: string,
  preimage: string,
): Promise<{ status: number; body: any }> {
  if (!HEX64.test(preimage)) {
    throw new L402Error("preimage must be exactly 64 hex chars");
  }
  const res = await fetch(url, {
    method: "GET",
    headers: { "Authorization": `L402 ${token}:${preimage}` },
  });
  const text = await res.text();
  let body: any = text;
  try { body = JSON.parse(text); } catch { /* leave as text */ }
  return { status: res.status, body };
}

export interface CallOptions {
  url?: string;
  preimage?: string;
  albyToken?: string;
}

/**
 * One-shot: challenge → (pay | supplied preimage) → replay.
 * Returns the tool response body.
 */
export async function callPaidTool(opts: CallOptions = {}): Promise<any> {
  const url = opts.url ?? DEFAULT_TOOL_URL;
  const ch = await requestChallenge(url);

  let preimage = opts.preimage;
  if (!preimage) {
    const alby = opts.albyToken ?? (globalThis as any).process?.env?.ALBY_TOKEN;
    if (alby) {
      preimage = await payWithAlby(ch.payment_request, alby);
    } else {
      throw new L402Error(
        `payment required: ${ch.sats} sats. Pay this BOLT11 and supply --preimage:\n${ch.payment_request}`,
      );
    }
  }

  const { status, body } = await replayWithPreimage(url, ch.token, preimage);
  if (status !== 200) {
    throw new L402Error(`replay failed (${status})`, status, JSON.stringify(body));
  }
  return body;
}

/* -------------------------------------------------------------------------- */
/*                                    CLI                                     */
/* -------------------------------------------------------------------------- */

// Only runs when executed directly (Node/Bun/Deno).
declare const require: any;
declare const module: any;

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const isMain = (typeof require !== "undefined" && typeof module !== "undefined" && require.main === module);

if (isMain) {
  const args = (globalThis as any).process?.argv?.slice(2) ?? [];
  const get = (k: string): string | undefined => {
    const i = args.indexOf(k);
    return i >= 0 ? args[i + 1] : undefined;
  };
  const has = (k: string) => args.includes(k);

  (async () => {
    const url = get("--url") ?? DEFAULT_TOOL_URL;
    try {
      if (has("--challenge-only")) {
        const ch = await requestChallenge(url);
        console.log(JSON.stringify(ch, null, 2));
        return;
      }
      const result = await callPaidTool({
        url,
        preimage: get("--preimage"),
        albyToken: get("--alby"),
      });
      console.log(JSON.stringify(result, null, 2));
    } catch (e) {
      const err = e as L402Error;
      console.error(`L402: ${err.message}`);
      (globalThis as any).process?.exit?.(2);
    }
  })();
}
