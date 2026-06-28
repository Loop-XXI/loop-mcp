import { describe, expect, it } from 'vitest';
import { LoopLedger } from '../src/ledger.js';
import { MockLightningProvider } from '../src/lightning/provider.js';
import { createL402Offer, mintMacaroon, openMacaroon, parseL402Header, verifyL402 } from '../src/rails/l402.js';
import { createX402Offer, parseUsdPrice, usdToUsdcBaseUnits } from '../src/rails/x402.js';
import { btcDiscountPct, usdToSats } from '../src/pricing/satsQuote.js';

const SECRET = 'test-secret';

describe('x402 rail helpers', () => {
  it('parses USD prices', () => {
    expect(parseUsdPrice('$0.01')).toBeCloseTo(0.01);
    expect(parseUsdPrice('1.50')).toBeCloseTo(1.5);
  });

  it('converts USD to USDC base units (6 decimals)', () => {
    expect(usdToUsdcBaseUnits(0.01)).toBe('10000');
    expect(usdToUsdcBaseUnits(1)).toBe('1000000');
  });

  it('builds an x402 offer with the right asset + amount', () => {
    const offer = createX402Offer({ tool: 'get-quote', price: '$0.01', payTo: '0xabc', network: 'base' });
    expect(offer.rail).toBe('x402');
    expect(offer.amount).toBe('10000');
    expect(offer.asset).toBe('0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913');
  });
});

describe('sats quote (21% BTC discount <= $100)', () => {
  it('discounts small amounts and not large ones', () => {
    expect(btcDiscountPct(50)).toBe(0.21);
    expect(btcDiscountPct(500)).toBe(0);
  });

  it('quotes discounted sats', () => {
    const { sats, discountPct } = usdToSats(1, 65000);
    expect(discountPct).toBe(0.21);
    // (1 * 0.79 / 65000) * 1e8 = 1215.38 -> ceil 1216
    expect(sats).toBe(1216);
  });
});

describe('L402 macaroon integrity', () => {
  it('round-trips and detects tampering', () => {
    const m = mintMacaroon(SECRET, { tool: 't', paymentHash: 'abcd', amountSats: 10, exp: 9_999_999_999 });
    expect(openMacaroon(SECRET, m)?.tool).toBe('t');
    expect(openMacaroon('wrong-secret', m)).toBeNull();
    const [p, s] = m.split('.');
    expect(openMacaroon(SECRET, `${p}x.${s}`)).toBeNull();
  });

  it('parses L402/LSAT auth headers', () => {
    expect(parseL402Header('L402 mac:deadbeef')).toEqual({ macaroon: 'mac', preimage: 'deadbeef' });
    expect(parseL402Header('LSAT mac:deadbeef')).toEqual({ macaroon: 'mac', preimage: 'deadbeef' });
    expect(parseL402Header(undefined)).toBeNull();
  });
});

describe('L402 payment verification (preimage proof)', () => {
  it('accepts a valid preimage and rejects a bad one', async () => {
    const provider = new MockLightningProvider();
    const offer = await createL402Offer({ secret: SECRET, provider, tool: 'list-todos', amountSats: 12 });
    const preimage = await provider.pay(offer.paymentHash);

    const ok = await verifyL402({ secret: SECRET, provider, tool: 'list-todos', macaroon: offer.macaroon, preimage });
    expect(ok.isValid).toBe(true);
    expect(ok.proof).toBe(preimage);

    const bad = await verifyL402({
      secret: SECRET,
      provider,
      tool: 'list-todos',
      macaroon: offer.macaroon,
      preimage: '00'.repeat(32),
    });
    expect(bad.isValid).toBe(false);
    expect(bad.invalidReason).toBe('preimage_does_not_match_payment_hash');
  });

  it('rejects a preimage replayed against a different tool', async () => {
    const provider = new MockLightningProvider();
    const offer = await createL402Offer({ secret: SECRET, provider, tool: 'add-todo', amountSats: 12 });
    const preimage = await provider.pay(offer.paymentHash);
    const res = await verifyL402({ secret: SECRET, provider, tool: 'delete-todo', macaroon: offer.macaroon, preimage });
    expect(res.isValid).toBe(false);
    expect(res.invalidReason).toBe('macaroon_tool_mismatch');
  });
});

describe('canonical rail-tagged ledger', () => {
  it('rolls up both rails and excludes internal:* rows', async () => {
    const ledger = new LoopLedger();
    await ledger.record({ rail: 'x402', tool: 'a', amount: '10000', unit: 'usdc-base-units', usd: 0.01, proof: '0xtx' });
    await ledger.record({ rail: 'l402', tool: 'b', amount: '1216', unit: 'sats', usd: 0.01, proof: 'preimg' });
    await ledger.record({
      rail: 'l402',
      tool: 'test',
      amount: '1',
      unit: 'sats',
      usd: 99,
      proof: 'x',
      tag: 'internal:test',
    });

    const totals = ledger.totals();
    expect(ledger.canonicalRows()).toHaveLength(2);
    expect(totals.usd).toBeCloseTo(0.02);
    expect(totals.byRail.x402.count).toBe(1);
    expect(totals.byRail.l402.count).toBe(1);
  });
});
