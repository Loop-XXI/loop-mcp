/**
 * Cross-rail price quoting.
 *
 * Loop XXI ships a real-time sats quote with a 21% Bitcoin-payment discount for
 * amounts <= $100 (the "21" in Loop XXI). Loop MCP reuses that doctrine so a
 * caller paying the Lightning/L402 rail gets the discount automatically, while
 * the x402/USDC rail is charged at face value.
 */

export const BTC_DISCOUNT_PCT = 0.21;
export const BTC_DISCOUNT_MAX_USD = 100;

/** Discount applied to an L402 (Lightning) payment for a given USD amount. */
export function btcDiscountPct(usd: number): number {
  return usd <= BTC_DISCOUNT_MAX_USD ? BTC_DISCOUNT_PCT : 0;
}

/**
 * Convert a USD price to sats for the Lightning rail, applying the BTC discount.
 * @param usd        Face USD price of the tool call.
 * @param btcUsdPrice Spot BTC/USD (injected; Loop's gateway sources this live).
 */
export function usdToSats(usd: number, btcUsdPrice: number): { sats: number; discountPct: number } {
  if (btcUsdPrice <= 0) throw new Error('btcUsdPrice must be positive');
  const discountPct = btcDiscountPct(usd);
  const discountedUsd = usd * (1 - discountPct);
  const sats = Math.ceil((discountedUsd / btcUsdPrice) * 1e8);
  return { sats, discountPct };
}
