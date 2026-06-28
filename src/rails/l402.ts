import { createHmac, timingSafeEqual } from 'node:crypto';
import type { LightningProvider } from '../lightning/provider.js';
import { sha256Hex } from '../lightning/provider.js';
import type { L402Offer, VerifyResult } from '../types.js';

/**
 * L402 (Lightning HTTP 402) rail.
 *
 * This is Loop MCP's headline addition over the x402-only original: the same
 * payment-aware MCP transport can be paid over the Lightning Network using the
 * L402 protocol (HTTP 402 + macaroon + Lightning preimage). The classic
 * L402 invariant does the heavy lifting:
 *
 *     payment_hash = sha256(preimage)
 *
 * Paying a Lightning invoice reveals the preimage, so presenting a valid
 * preimage for the macaroon-bound payment hash is itself proof of payment.
 *
 * The macaroon is an integrity-protected (HMAC-SHA256) token that binds the
 * payment hash to the tool, amount and expiry, so a caller cannot swap a cheap
 * invoice's preimage onto an expensive tool.
 */

export interface L402Caveats {
  tool: string;
  paymentHash: string;
  amountSats: number;
  /** Unix seconds. */
  exp: number;
}

/** Mint an integrity-protected macaroon. Format: base64url(JSON).sig */
export function mintMacaroon(secret: string, caveats: L402Caveats): string {
  const payload = base64url(Buffer.from(JSON.stringify(caveats)));
  const sig = createHmac('sha256', secret).update(payload).digest('hex');
  return `${payload}.${sig}`;
}

/** Verify macaroon integrity and return its caveats, or null if tampered. */
export function openMacaroon(secret: string, macaroon: string): L402Caveats | null {
  const [payload, sig] = macaroon.split('.');
  if (!payload || !sig) return null;
  const expected = createHmac('sha256', secret).update(payload).digest('hex');
  if (!safeEqualHex(sig, expected)) return null;
  try {
    return JSON.parse(Buffer.from(payload, 'base64url').toString('utf8')) as L402Caveats;
  } catch {
    return null;
  }
}

/**
 * Build the L402 leg of a 402 challenge for a tool: open a Lightning invoice and
 * mint a macaroon bound to its payment hash.
 */
export async function createL402Offer(args: {
  secret: string;
  provider: LightningProvider;
  tool: string;
  amountSats: number;
  ttlSeconds?: number;
}): Promise<L402Offer> {
  const { secret, provider, tool, amountSats, ttlSeconds = 60 } = args;
  const { invoice, paymentHash } = await provider.createInvoice(amountSats, `mcp://tool/${tool}`);
  const macaroon = mintMacaroon(secret, {
    tool,
    paymentHash,
    amountSats,
    exp: Math.floor(Date.now() / 1000) + ttlSeconds,
  });
  return {
    rail: 'l402',
    description: `Payment for MCP tool: ${tool}`,
    resource: `mcp://tool/${tool}`,
    amount: String(amountSats),
    unit: 'sats',
    invoice,
    macaroon,
    paymentHash,
  };
}

/**
 * Parse an inbound `Authorization: L402 <macaroon>:<preimage>` header.
 * Tolerates the legacy `LSAT` scheme name.
 */
export function parseL402Header(header: string | undefined): { macaroon: string; preimage: string } | null {
  if (!header) return null;
  const m = header.match(/^(?:L402|LSAT)\s+(.+):([0-9a-fA-F]+)\s*$/);
  if (!m) return null;
  return { macaroon: m[1], preimage: m[2] };
}

/**
 * Verify an L402 payment for `tool`. Checks, in order:
 *  1. macaroon integrity (HMAC) and that it is bound to this tool,
 *  2. macaroon not expired,
 *  3. sha256(preimage) === bound payment hash  (the core L402 proof),
 *  4. optionally, that the provider also reports the invoice settled.
 */
export async function verifyL402(args: {
  secret: string;
  provider: LightningProvider;
  tool: string;
  macaroon: string;
  preimage: string;
  /** When true, also require provider.isSettled (defence-in-depth). */
  requireProviderSettlement?: boolean;
}): Promise<VerifyResult> {
  const { secret, provider, tool, macaroon, preimage, requireProviderSettlement = false } = args;
  const caveats = openMacaroon(secret, macaroon);
  if (!caveats) return { isValid: false, rail: 'l402', invalidReason: 'invalid_or_tampered_macaroon' };
  if (caveats.tool !== tool) return { isValid: false, rail: 'l402', invalidReason: 'macaroon_tool_mismatch' };
  if (caveats.exp < Math.floor(Date.now() / 1000))
    return { isValid: false, rail: 'l402', invalidReason: 'macaroon_expired' };

  let preimageBuf: Buffer;
  try {
    preimageBuf = Buffer.from(preimage, 'hex');
    if (preimageBuf.length !== 32) throw new Error('bad length');
  } catch {
    return { isValid: false, rail: 'l402', invalidReason: 'malformed_preimage' };
  }

  const computed = sha256Hex(preimageBuf);
  if (!safeEqualHex(computed, caveats.paymentHash))
    return { isValid: false, rail: 'l402', invalidReason: 'preimage_does_not_match_payment_hash' };

  if (requireProviderSettlement && !(await provider.isSettled(caveats.paymentHash)))
    return { isValid: false, rail: 'l402', invalidReason: 'invoice_not_settled' };

  return { isValid: true, rail: 'l402', proof: preimage };
}

function base64url(buf: Buffer): string {
  return buf.toString('base64url');
}

function safeEqualHex(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  try {
    return timingSafeEqual(Buffer.from(a, 'hex'), Buffer.from(b, 'hex'));
  } catch {
    return false;
  }
}
