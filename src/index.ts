// Server-side exports
export { LoopDualRailServerTransport, makePaymentAwareServerTransport, type LightningConfig } from './server.js';

// Client-side exports
export { makePaymentAwareClientTransport, type DualRailClientOptions, type LightningPayer } from './client.js';

// Proxy exports
export { ApiKeyHook, createClientProxy, createServerProxy } from './proxy/index.js';

// [LOOP ADDITION] Dual-rail primitives, ledger and quoting
export { LoopLedger, InMemoryLedgerSink, type LedgerSink } from './ledger.js';
export {
  MockLightningProvider,
  PhoenixdProvider,
  type LightningProvider,
  type LightningInvoice,
} from './lightning/provider.js';
export { createL402Offer, verifyL402, mintMacaroon, openMacaroon, parseL402Header } from './rails/l402.js';
export { createX402Offer, parseUsdPrice, usdToUsdcBaseUnits } from './rails/x402.js';
export { usdToSats, btcDiscountPct } from './pricing/satsQuote.js';
export type { Rail, LedgerEntry, PaymentOffer, X402Offer, L402Offer, VerifyResult } from './types.js';
