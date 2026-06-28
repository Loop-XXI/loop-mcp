import type { X402Offer } from '../types.js';

/**
 * x402 (EVM/USDC) rail helpers.
 *
 * This rail is a faithful port of the original x402-only MCP transport: a paid
 * tool returns HTTP 402 with payment requirements, the caller pays USDC on an
 * EVM network and retries with an `X-PAYMENT` header, and the server verifies
 * and settles through an x402 facilitator.
 *
 * These helpers are dependency-free so the rail can be unit-tested without the
 * `x402` package. The transport layer additionally maps `X402Offer` onto the
 * x402 library's `PaymentRequirements` for real facilitator verify/settle.
 */

/** USDC has 6 decimals on Base/EVM. */
export const USDC_DECIMALS = 6;

/** Known USDC contract addresses by network. */
export const USDC_ADDRESS: Record<string, string> = {
  'base-sepolia': '0x036CbD53842c5426634e7929541eC2318f3dCF7e',
  base: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913',
};

/** Parse a "$0.01" style price into a number of USD. */
export function parseUsdPrice(price: string): number {
  const n = Number(String(price).replace(/[^0-9.]/g, ''));
  if (!Number.isFinite(n) || n < 0) throw new Error(`Invalid price: ${price}`);
  return n;
}

/** Convert USD to USDC atomic (base) units. */
export function usdToUsdcBaseUnits(usd: number): string {
  return BigInt(Math.round(usd * 10 ** USDC_DECIMALS)).toString();
}

/** Build the x402 leg of a 402 challenge for a tool. */
export function createX402Offer(args: {
  tool: string;
  price: string;
  payTo: string;
  network?: string;
  maxTimeoutSeconds?: number;
}): X402Offer {
  const { tool, price, payTo, network = 'base-sepolia', maxTimeoutSeconds = 60 } = args;
  const usd = parseUsdPrice(price);
  const asset = USDC_ADDRESS[network];
  if (!asset) throw new Error(`No USDC address known for network: ${network}`);
  return {
    rail: 'x402',
    scheme: 'exact',
    network,
    description: `Payment for MCP tool: ${tool}`,
    resource: `mcp://tool/${tool}`,
    amount: usdToUsdcBaseUnits(usd),
    unit: 'usdc-base-units',
    payTo,
    asset,
    maxTimeoutSeconds,
  };
}
