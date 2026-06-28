#!/usr/bin/env node
/**
 * Dependency-free end-to-end demo of the Loop MCP dual-rail PAYMENT CORE.
 *
 * Runs the exact algorithms used by the transport (L402 macaroon + preimage,
 * x402 atomic conversion, 21% sats quote, canonical rail-tagged ledger) without
 * the MCP SDK, viem or the x402 package, so it runs anywhere with just Node.
 *
 *   node scripts/demo-core.mjs
 */
import { createHash, createHmac, randomBytes, timingSafeEqual } from 'node:crypto';

const sha256 = (buf) => createHash('sha256').update(buf).digest('hex');
const log = (...a) => console.log(...a);

// ---- L402 ----
function mintMacaroon(secret, caveats) {
  const payload = Buffer.from(JSON.stringify(caveats)).toString('base64url');
  const sig = createHmac('sha256', secret).update(payload).digest('hex');
  return `${payload}.${sig}`;
}
function openMacaroon(secret, mac) {
  const [payload, sig] = mac.split('.');
  const expected = createHmac('sha256', secret).update(payload).digest('hex');
  if (sig.length !== expected.length || !timingSafeEqual(Buffer.from(sig, 'hex'), Buffer.from(expected, 'hex')))
    return null;
  return JSON.parse(Buffer.from(payload, 'base64url').toString());
}
function mockInvoice(tool, amountSats) {
  const preimage = randomBytes(32).toString('hex');
  const paymentHash = sha256(Buffer.from(preimage, 'hex'));
  return { invoice: `lnbcrt${amountSats}n1mock${paymentHash.slice(0, 16)}`, preimage, paymentHash };
}
function verifyL402(secret, tool, macaroon, preimage) {
  const c = openMacaroon(secret, macaroon);
  if (!c) return { isValid: false, reason: 'invalid_macaroon' };
  if (c.tool !== tool) return { isValid: false, reason: 'macaroon_tool_mismatch' };
  if (sha256(Buffer.from(preimage, 'hex')) !== c.paymentHash)
    return { isValid: false, reason: 'preimage_does_not_match_payment_hash' };
  return { isValid: true, proof: preimage };
}

// ---- x402 / pricing ----
const usdToUsdcBaseUnits = (usd) => BigInt(Math.round(usd * 1e6)).toString();
function usdToSats(usd, btcUsd) {
  const discountPct = usd <= 100 ? 0.21 : 0;
  return { sats: Math.ceil((usd * (1 - discountPct) / btcUsd) * 1e8), discountPct };
}

// ---- canonical ledger ----
const ledger = [];
const record = (e) => ledger.push({ ...e, ts: new Date().toISOString() });
const canonical = () => ledger.filter((r) => !String(r.tag ?? '').startsWith('internal:'));

// ====================== RUN ======================
const SECRET = 'demo-secret';
const BTC_USD = 65000;
let pass = 0;
let fail = 0;
const check = (name, cond) => {
  (cond ? pass++ : fail++);
  log(`${cond ? '✅' : '❌'} ${name}`);
};

log('\n=== Loop MCP dual-rail core demo ===\n');

// Scenario 1: caller pays the L402 (Lightning) rail for list-todos ($0.001)
const price1 = 0.001;
const q1 = usdToSats(price1, BTC_USD);
const inv = mockInvoice('list-todos', q1.sats);
const mac = mintMacaroon(SECRET, {
  tool: 'list-todos',
  paymentHash: inv.paymentHash,
  amountSats: q1.sats,
  exp: Math.floor(Date.now() / 1000) + 60,
});
log(`L402 offer: ${q1.sats} sats (21% BTC discount=${q1.discountPct}) invoice=${inv.invoice}`);
const learnedPreimage = inv.preimage; // caller learns this by paying the invoice
const v1 = verifyL402(SECRET, 'list-todos', mac, learnedPreimage);
check('valid preimage verifies', v1.isValid);
if (v1.isValid) record({ rail: 'l402', tool: 'list-todos', amount: String(q1.sats), unit: 'sats', usd: price1, proof: v1.proof });

// Scenario 2: forged preimage is rejected
const v2 = verifyL402(SECRET, 'list-todos', mac, '00'.repeat(32));
check('forged preimage rejected', !v2.isValid && v2.reason === 'preimage_does_not_match_payment_hash');

// Scenario 3: preimage replay onto a different (pricier) tool rejected
const v3 = verifyL402(SECRET, 'delete-todo', mac, learnedPreimage);
check('cross-tool replay rejected', !v3.isValid && v3.reason === 'macaroon_tool_mismatch');

// Scenario 4: tampered macaroon rejected
const v4 = verifyL402('attacker-secret', 'list-todos', mac, learnedPreimage);
check('tampered/wrong-secret macaroon rejected', !v4.isValid);

// Scenario 5: caller pays the x402 (USDC) rail for add-todo ($0.002)
const price2 = 0.002;
const baseUnits = usdToUsdcBaseUnits(price2);
check('x402 atomic amount correct', baseUnits === '2000');
record({ rail: 'x402', tool: 'add-todo', amount: baseUnits, unit: 'usdc-base-units', usd: price2, proof: '0xTXHASH' });

// Scenario 6: internal:* test row excluded from canonical revenue
record({ rail: 'l402', tool: 'synthetic', amount: '1', unit: 'sats', usd: 9.99, proof: 'x', tag: 'internal:test' });

const usdTotal = canonical().reduce((s, r) => s + r.usd, 0);
check('canonical rows = 2 (internal excluded)', canonical().length === 2);
check('canonical USD roll-up = 0.003', Math.abs(usdTotal - 0.003) < 1e-9);

log('\n--- canonical ledger ---');
for (const r of canonical()) log(`  [${r.rail}] ${r.tool}  ${r.amount} ${r.unit}  ($${r.usd})  proof=${r.proof.slice(0, 12)}`);
log(`\nRevenue by rail: x402=$${canonical().filter(r=>r.rail==='x402').reduce((s,r)=>s+r.usd,0)}  l402=$${canonical().filter(r=>r.rail==='l402').reduce((s,r)=>s+r.usd,0)}`);

log(`\n=== ${pass} passed, ${fail} failed ===`);
process.exit(fail ? 1 : 0);
