import { createHash, randomBytes } from 'node:crypto';

/**
 * Lightning rail provider abstraction.
 *
 * Loop XXI operational doctrine: phoenixd on Railway is the hot Lightning node;
 * treasury stays cold in Nunchuk and excess is swept. The transport never holds
 * a node directly — it talks to a `LightningProvider`, so phoenixd can be swapped
 * for LND/CLN/a custodial test rail without touching payment logic.
 */
export interface LightningInvoice {
  /** BOLT11 invoice string. */
  invoice: string;
  /** Hex-encoded payment hash = sha256(preimage). Bound into the L402 macaroon. */
  paymentHash: string;
  /** Amount in sats. */
  amountSats: number;
}

export interface LightningProvider {
  /** Create a Lightning invoice for `amountSats`, tagged with `memo`. */
  createInvoice(amountSats: number, memo: string): Promise<LightningInvoice>;
  /**
   * Confirm the invoice with this payment hash has been paid.
   * In L402, possession of a valid preimage already proves payment
   * (payment_hash = sha256(preimage)), but real deployments may also
   * reconcile against the node's settled-invoice list.
   */
  isSettled(paymentHash: string): Promise<boolean>;
}

/**
 * Deterministic in-memory provider for local dev, demos and tests.
 * Generates a real preimage/paymentHash pair (sha256) so the L402 preimage
 * check exercises the exact production code path — only the BOLT11 string is faked.
 */
export class MockLightningProvider implements LightningProvider {
  /** paymentHash -> preimage (so the mock "client" can learn the preimage by paying). */
  private readonly opened = new Map<string, string>();
  private readonly settled = new Set<string>();

  async createInvoice(amountSats: number, memo: string): Promise<LightningInvoice> {
    const preimage = randomBytes(32).toString('hex');
    const paymentHash = sha256Hex(Buffer.from(preimage, 'hex'));
    this.opened.set(paymentHash, preimage);
    const invoice = `lnbcrt${amountSats}n1mock${paymentHash.slice(0, 24)}`;
    return { invoice, paymentHash, amountSats };
  }

  /** Test/demo helper: simulate the caller paying the invoice and learning the preimage. */
  async pay(paymentHash: string): Promise<string> {
    const preimage = this.opened.get(paymentHash);
    if (!preimage) throw new Error('Unknown invoice');
    this.settled.add(paymentHash);
    return preimage;
  }

  async isSettled(paymentHash: string): Promise<boolean> {
    return this.settled.has(paymentHash);
  }
}

/**
 * Production adapter for phoenixd (Loop XXI's hot Lightning node on Railway).
 * Talks to the phoenixd REST API. Kept dependency-free (uses global fetch).
 *
 * @see https://phoenix.acinq.co/server/api
 */
export class PhoenixdProvider implements LightningProvider {
  constructor(
    private readonly baseUrl: string,
    private readonly password: string
  ) {}

  private auth(): string {
    return `Basic ${Buffer.from(`:${this.password}`).toString('base64')}`;
  }

  async createInvoice(amountSats: number, memo: string): Promise<LightningInvoice> {
    const body = new URLSearchParams({
      amountSat: String(amountSats),
      description: memo,
    });
    const res = await fetch(`${this.baseUrl}/createinvoice`, {
      method: 'POST',
      headers: { Authorization: this.auth(), 'Content-Type': 'application/x-www-form-urlencoded' },
      body,
    });
    if (!res.ok) throw new Error(`phoenixd createinvoice failed: ${res.status}`);
    const json = (await res.json()) as { serialized: string; paymentHash: string };
    return { invoice: json.serialized, paymentHash: json.paymentHash, amountSats };
  }

  async isSettled(paymentHash: string): Promise<boolean> {
    const res = await fetch(`${this.baseUrl}/payments/incoming/${paymentHash}`, {
      headers: { Authorization: this.auth() },
    });
    if (!res.ok) return false;
    const json = (await res.json()) as { isPaid?: boolean };
    return Boolean(json.isPaid);
  }
}

export function sha256Hex(data: Buffer): string {
  return createHash('sha256').update(data).digest('hex');
}
