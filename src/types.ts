/**
 * Shared types for Loop MCP dual-rail payments.
 *
 * Loop XXI dual-rail doctrine: every product speaks both fiat/stablecoin (x402/USDC)
 * and Bitcoin/Lightning (L402), unified through ONE canonical rail-tagged ledger.
 * Payment rails are never bolted on per-product; they are negotiated here and
 * reconciled into `LoopLedger`.
 */

/** The two payment rails Loop MCP negotiates per tool call. */
export type Rail = 'x402' | 'l402';

/** A single price expressed in USD (e.g. "$0.01"). Sats are derived per the live quote. */
export type Price = string;

/** A payment offer presented to the caller in the 402 `accepts` array. */
export interface PaymentOffer {
  rail: Rail;
  /** Human description, e.g. "Payment for MCP tool: get-quote". */
  description: string;
  /** Resource URI, e.g. "mcp://tool/get-quote". */
  resource: string;
  /** Amount in the rail's atomic unit (USDC base units for x402, millisats/sats for l402). */
  amount: string;
  /** Atomic unit label: "usdc-base-units" | "sats". */
  unit: string;
}

/** x402 (EVM/USDC) leg of an offer. Mirrors the x402 PaymentRequirements shape. */
export interface X402Offer extends PaymentOffer {
  rail: 'x402';
  scheme: 'exact';
  network: string;
  payTo: string;
  asset: string;
  maxTimeoutSeconds: number;
  extra?: unknown;
}

/** L402 (Lightning) leg of an offer. */
export interface L402Offer extends PaymentOffer {
  rail: 'l402';
  /** BOLT11 invoice the caller pays over Lightning. */
  invoice: string;
  /** Opaque, integrity-protected macaroon binding the payment hash to this tool. */
  macaroon: string;
  /** Hex-encoded payment hash = sha256(preimage). */
  paymentHash: string;
}

/** Result of verifying an inbound payment on either rail. */
export interface VerifyResult {
  isValid: boolean;
  rail: Rail;
  invalidReason?: string;
  /** Identifier proving settlement: EVM tx hash (x402) or payment preimage (l402). */
  proof?: string;
}

/** A settled, canonical ledger row. */
export interface LedgerEntry {
  id: string;
  ts: string;
  rail: Rail;
  tool: string;
  /** Amount in the rail's atomic unit. */
  amount: string;
  unit: string;
  /** USD-equivalent for cross-rail roll-up (north-star is net sats; USD kept for fiat books). */
  usd: number;
  /** EVM tx hash (x402) or payment preimage (l402). */
  proof: string;
  /** Tag used to exclude synthetic/test rows from canonical revenue, e.g. "internal:test". */
  tag?: string;
}
