import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import type { Wallet } from 'x402/types';
import { wrapFetchWithPayment } from 'x402-fetch';
import { convertHeaders } from './util.js';

/**
 * Client-side Lightning payer for the L402 rail.
 *
 * Implementations pay a BOLT11 invoice and return the 32-byte preimage (hex),
 * which is the proof of payment presented back to the server.
 */
export interface LightningPayer {
  payInvoice(invoice: string, paymentHash: string): Promise<{ preimage: string }>;
}

export interface DualRailClientOptions {
  /** Which rail to prefer when the server advertises both. Default: 'x402'. */
  preferredRail?: 'x402' | 'l402';
  /** Required to pay over the L402/Lightning rail. */
  lightningPayer?: LightningPayer;
  /** Called with the settlement proof: EVM tx hash (x402) or preimage (l402). */
  paymentCallback?: (proof: string, rail: 'x402' | 'l402') => void;
}

interface L402AcceptEntry {
  rail: 'l402';
  invoice: string;
  macaroon: string;
  paymentHash: string;
}

/**
 * Creates a payment-aware MCP client transport.
 *
 * - x402 rail: identical to the original — `wrapFetchWithPayment` auto-handles
 *   402 -> pay USDC -> retry, using the supplied viem wallet.
 * - l402 rail [LOOP ADDITION]: when `preferredRail: 'l402'` and a `lightningPayer`
 *   is provided, a 402 is satisfied by paying the advertised Lightning invoice and
 *   retrying with `Authorization: L402 <macaroon>:<preimage>`.
 *
 * @param serverUrl MCP server URL.
 * @param wallet    viem WalletClient (used for the x402 rail).
 * @param options   Dual-rail behaviour and callbacks.
 */
export function makePaymentAwareClientTransport(
  serverUrl: string | URL,
  wallet: Wallet,
  options: DualRailClientOptions = {}
): StreamableHTTPClientTransport {
  const preferredRail = options.preferredRail ?? 'x402';
  const x402Fetch = wrapFetchWithPayment(fetch, wallet);

  const baseHeaders = (init?: RequestInit) => ({
    ...convertHeaders(init?.headers),
    Accept: 'application/json, text/event-stream',
  });

  const fetchWithPayment = async (input: RequestInfo, init: RequestInit): Promise<Response> => {
    const headers = baseHeaders(init);

    // [LOOP ADDITION] Lightning-first path.
    if (preferredRail === 'l402' && options.lightningPayer) {
      const first = await fetch(input, { ...init, headers });
      if (first.status !== 402) return first;

      const offer = await extractL402Offer(first);
      if (!offer) {
        // Server didn't advertise L402; fall back to x402.
        return x402Fetch(input, { ...init, headers: baseHeaders(init) });
      }

      const { preimage } = await options.lightningPayer.payInvoice(offer.invoice, offer.paymentHash);
      const retry = await fetch(input, {
        ...init,
        headers: { ...baseHeaders(init), Authorization: `L402 ${offer.macaroon}:${preimage}` },
      });
      options.paymentCallback?.(preimage, 'l402');
      return retry;
    }

    // Default: x402 rail (faithful original behaviour).
    const response = await x402Fetch(input, { ...init, headers });
    const paymentResponse = response.headers.get('X-PAYMENT-RESPONSE');
    if (paymentResponse) {
      try {
        const decoded = JSON.parse(atob(paymentResponse));
        if (decoded.txHash) options.paymentCallback?.(decoded.txHash, 'x402');
      } catch (e) {
        console.error('❌ Failed to decode payment response:', e);
      }
    }
    return response;
  };

  return new StreamableHTTPClientTransport(new URL(serverUrl), {
    fetch: fetchWithPayment as typeof fetch,
  });
}

/** Pull the L402 leg from a 402 response (body `accepts` first, then WWW-Authenticate). */
async function extractL402Offer(res: Response): Promise<L402AcceptEntry | null> {
  try {
    const cloned = res.clone();
    const body = (await cloned.json()) as { accepts?: L402AcceptEntry[] };
    const fromBody = body.accepts?.find((a) => a.rail === 'l402');
    if (fromBody?.invoice && fromBody.macaroon && fromBody.paymentHash) return fromBody;
  } catch {
    /* fall through to header parsing */
  }
  const header = res.headers.get('WWW-Authenticate');
  if (header) {
    const macaroon = header.match(/macaroon="([^"]+)"/)?.[1];
    const invoice = header.match(/invoice="([^"]+)"/)?.[1];
    if (macaroon && invoice) return { rail: 'l402', macaroon, invoice, paymentHash: '' };
  }
  return null;
}
