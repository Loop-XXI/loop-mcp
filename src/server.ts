import type { OutgoingHttpHeader, OutgoingHttpHeaders } from 'node:http';
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { AuthInfo } from '@modelcontextprotocol/sdk/server/auth/types.js';
import {
  StreamableHTTPServerTransport,
  type StreamableHTTPServerTransportOptions,
} from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import {
  type CallToolRequest,
  isJSONRPCError,
  isJSONRPCRequest,
  isJSONRPCResponse,
  type JSONRPCError,
  type JSONRPCMessage,
  type JSONRPCRequest,
  type JSONRPCResponse,
  type MessageExtraInfo,
  type RequestId,
} from '@modelcontextprotocol/sdk/types.js';
import { type Address, getAddress } from 'viem';
import { exact } from 'x402/schemes';
import { findMatchingPaymentRequirements, processPriceToAtomicAmount } from 'x402/shared';
import {
  type ERC20TokenAmount,
  type FacilitatorConfig,
  type PaymentPayload,
  type PaymentRequirements,
  type Price,
  type SPLTokenAmount,
  settleResponseHeader,
} from 'x402/types';
import { useFacilitator } from 'x402/verify';
import { LoopLedger } from './ledger.js';
import type { LightningProvider } from './lightning/provider.js';
import { createL402Offer, parseL402Header, verifyL402 } from './rails/l402.js';
import { usdToSats } from './pricing/satsQuote.js';
import { parseUsdPrice } from './rails/x402.js';
import type { Rail } from './types.js';

/**
 * Loop MCP dual-rail server transport.
 *
 * Faithful port of the x402-only MCP transport, extended with Loop XXI's
 * Lightning/L402 rail. Blocks marked `[LOOP ADDITION]` are the new dual-rail
 * behaviour; everything else mirrors the original x402 flow.
 */

// [LOOP ADDITION] Lightning configuration for the second rail.
export interface LightningConfig {
  provider: LightningProvider;
  /** HMAC secret used to mint/verify L402 macaroons. */
  l402Secret: string;
  /** Spot BTC/USD used to quote sats. Loop's gateway sources this live. */
  btcUsdPrice: number;
  /** Also require the node to report the invoice settled (defence-in-depth). */
  requireProviderSettlement?: boolean;
}

interface DualRailTransportOptions {
  payTo: Address;
  facilitator?: FacilitatorConfig;
  toolPricing?: Record<string, string>;
  network?: string;
  // [LOOP ADDITION]
  lightning?: LightningConfig;
  ledger?: LoopLedger;
}

interface SettlementInfo {
  rail?: Rail;
  transactionHash?: string;
  preimage?: string;
  error?: string;
}

interface ToolCallParams {
  name: string;
  arguments?: CallToolRequest['params']['arguments'];
}

function isToolCallParams(params: unknown): params is ToolCallParams {
  return (
    params !== null &&
    typeof params === 'object' &&
    'name' in params &&
    typeof (params as { name: unknown }).name === 'string'
  );
}

interface PaymentInfo {
  rail: Rail;
  toolName: string;
  toolPrice: Price;
  // x402 leg
  payment?: PaymentPayload;
  // [LOOP ADDITION] l402 leg
  l402?: { macaroon: string; preimage: string };
  request?: JSONRPCRequest;
  req?: IncomingMessage;
}

const payloadIsTransaction = (
  payload: PaymentPayload['payload']
): payload is PaymentPayload['payload'] & { transaction: string } => Object.hasOwn(payload, 'transaction');

const assetIsErc20 = (asset: ERC20TokenAmount['asset'] | SPLTokenAmount['asset']): asset is ERC20TokenAmount['asset'] =>
  Object.hasOwn(asset, 'eip712');

export class LoopDualRailServerTransport extends StreamableHTTPServerTransport {
  private payTo: Address;
  private network: string;
  private facilitator?: FacilitatorConfig;
  private settlementMap: Map<string | number, SettlementInfo> = new Map();
  private requestPaymentMap: Map<string | number, PaymentInfo> = new Map();
  private pendingPayment: PaymentInfo | null = null;
  private toolPricing: Record<string, Price>;
  private currentResponse: ServerResponse | null = null;
  private responsePaymentHeaders: Map<ServerResponse, string> = new Map();
  // [LOOP ADDITION]
  private lightning?: LightningConfig;
  private ledger: LoopLedger;

  constructor(options: DualRailTransportOptions & StreamableHTTPServerTransportOptions) {
    super({
      ...options,
      enableJsonResponse: options.enableJsonResponse ?? true,
    });

    this.payTo = options.payTo;
    this.network = options.network ?? 'base-sepolia';
    this.facilitator = options.facilitator;
    this.toolPricing = options.toolPricing || {};
    this.lightning = options.lightning;
    this.ledger = options.ledger ?? new LoopLedger();

    console.log('🔧 [LoopMCP] Dual-rail transport created. payTo:', this.payTo);
    console.log('   Rails:', this.lightning ? 'x402 + l402(lightning)' : 'x402 only');
    console.log('   Tool pricing:', this.toolPricing);

    this.setupMessageInterception();
  }

  /** Expose the canonical ledger so the host app can reconcile/report. */
  getLedger(): LoopLedger {
    return this.ledger;
  }

  async send(message: JSONRPCMessage, options?: { relatedRequestId?: RequestId }) {
    if ((isJSONRPCResponse(message) || isJSONRPCError(message)) && message.id !== undefined) {
      const paymentInfo = this.requestPaymentMap.get(message.id);
      if (paymentInfo && !this.settlementMap.has(message.id)) {
        const settlementInfo = await this.settlePayment(paymentInfo, message);
        this.settlementMap.set(message.id, settlementInfo);

        if (isJSONRPCResponse(message) && (settlementInfo.transactionHash || settlementInfo.preimage)) {
          message = {
            ...message,
            result: {
              ...message.result,
              // [LOOP ADDITION] rail-tagged settlement receipt in the tool result
              loopSettlement: {
                rail: settlementInfo.rail,
                transactionHash: settlementInfo.transactionHash,
                preimage: settlementInfo.preimage,
                settled: true,
              },
            },
          };
        }
      }
    }
    return super.send(message, options);
  }

  async handleRequest(
    req: IncomingMessage & { auth?: AuthInfo },
    res: ServerResponse,
    parsedBody?: unknown
  ): Promise<void> {
    this.currentResponse = res;

    const originalWriteHead = res.writeHead.bind(res);
    res.writeHead = ((statusCode: number, headers?: OutgoingHttpHeaders | OutgoingHttpHeader[] | undefined) => {
      const paymentHeader = this.responsePaymentHeaders.get(res);
      if (paymentHeader && headers && !Array.isArray(headers)) {
        headers['X-PAYMENT-RESPONSE'] = paymentHeader;
        this.responsePaymentHeaders.delete(res);
      }
      return originalWriteHead.call(res, statusCode, headers);
    }) as typeof res.writeHead;

    if (req.method !== 'POST' || !parsedBody) {
      return super.handleRequest(req, res, parsedBody);
    }

    const messages = Array.isArray(parsedBody) ? parsedBody : [parsedBody];
    const toolCall = messages.find(
      (msg): msg is JSONRPCRequest & { params: ToolCallParams } =>
        msg.method === 'tools/call' &&
        msg.params &&
        typeof msg.params === 'object' &&
        'name' in msg.params &&
        typeof msg.params.name === 'string' &&
        this.toolPricing[msg.params.name] !== undefined
    );

    if (!toolCall) {
      return super.handleRequest(req, res, parsedBody);
    }

    const toolName = toolCall.params.name;
    const toolPrice = this.toolPricing[toolName];

    // [LOOP ADDITION] Inspect both rails' inbound headers.
    const xPaymentHeader = req.headers['x-payment'];
    const l402 = parseL402Header(
      Array.isArray(req.headers.authorization) ? req.headers.authorization[0] : req.headers.authorization
    );

    // No payment on either rail -> issue a dual-rail 402 challenge.
    if ((!xPaymentHeader || Array.isArray(xPaymentHeader)) && !l402) {
      return this.send402(res, toolName, toolPrice);
    }

    // ----- L402 (Lightning) rail -----
    if (l402 && this.lightning) {
      const result = await verifyL402({
        secret: this.lightning.l402Secret,
        provider: this.lightning.provider,
        tool: toolName,
        macaroon: l402.macaroon,
        preimage: l402.preimage,
        requireProviderSettlement: this.lightning.requireProviderSettlement,
      });
      if (!result.isValid) {
        return this.send402(res, toolName, toolPrice, result.invalidReason);
      }
      this.pendingPayment = {
        rail: 'l402',
        toolName,
        toolPrice,
        l402,
        req,
      };
      return super.handleRequest(req, res, parsedBody);
    }

    // ----- x402 (EVM/USDC) rail (faithful original flow) -----
    if (!xPaymentHeader || Array.isArray(xPaymentHeader)) {
      return this.send402(res, toolName, toolPrice);
    }

    let decodedPayment: PaymentPayload;
    try {
      decodedPayment = exact.evm.decodePayment(xPaymentHeader);
      decodedPayment.x402Version = 1;
      if (payloadIsTransaction(decodedPayment.payload)) {
        throw new Error('Payment missing authorization payload');
      }
    } catch {
      return this.send402(res, toolName, toolPrice, 'Invalid payment header');
    }

    try {
      const { verify } = useFacilitator(this.facilitator);
      const paymentRequirements = this.getX402RequirementsForTool(toolName, toolPrice);
      const selected = findMatchingPaymentRequirements(paymentRequirements, decodedPayment);
      if (!selected) {
        return this.send402(res, toolName, toolPrice, 'Unable to find matching payment requirements');
      }
      const verifyResponse = await verify(decodedPayment, selected);
      if (!verifyResponse.isValid) {
        return this.send402(res, toolName, toolPrice, verifyResponse.invalidReason || 'Payment verification failed');
      }
    } catch (error) {
      const msg = error instanceof Error ? error.message : String(error);
      return this.send402(res, toolName, toolPrice, msg);
    }

    this.pendingPayment = { rail: 'x402', payment: decodedPayment, toolName, toolPrice, req };
    return super.handleRequest(req, res, parsedBody);
  }

  /** [LOOP ADDITION] Emit a 402 advertising every available rail. */
  private async send402(res: ServerResponse, toolName: string, toolPrice: Price, error?: string): Promise<void> {
    const accepts: unknown[] = this.getX402RequirementsForTool(toolName, toolPrice);
    let wwwAuth: string | undefined;

    if (this.lightning) {
      const usd = parseUsdPrice(toolPrice);
      const { sats } = usdToSats(usd, this.lightning.btcUsdPrice);
      const offer = await createL402Offer({
        secret: this.lightning.l402Secret,
        provider: this.lightning.provider,
        tool: toolName,
        amountSats: sats,
      });
      accepts.push(offer);
      // Standard L402 challenge header so Lightning-native clients can pay too.
      wwwAuth = `L402 macaroon="${offer.macaroon}", invoice="${offer.invoice}"`;
    }

    const headers: OutgoingHttpHeaders = { 'Content-Type': 'application/json' };
    if (wwwAuth) headers['WWW-Authenticate'] = wwwAuth;

    res.writeHead(402, headers).end(
      JSON.stringify({
        x402Version: 1,
        error: error ?? 'Payment required',
        accepts,
      })
    );
  }

  private setupMessageInterception() {
    const originalOnMessage = this.onmessage;
    this.onmessage = async (message: JSONRPCMessage, extra?: MessageExtraInfo) => {
      if (isJSONRPCRequest(message) && message.method === 'tools/call' && isToolCallParams(message.params)) {
        const toolName = message.params.name;
        const toolPrice = this.toolPricing[toolName];
        if (toolPrice && this.pendingPayment && message.id !== undefined) {
          this.requestPaymentMap.set(message.id, { ...this.pendingPayment, request: message });
        }
      }
      if (originalOnMessage) {
        await originalOnMessage.call(this, message, extra);
      }
    };
  }

  private getX402RequirementsForTool(toolName: string, price: Price): PaymentRequirements[] {
    const network = this.network;
    const atomicAmountForAsset = processPriceToAtomicAmount(price, network);
    if ('error' in atomicAmountForAsset) {
      throw new Error(atomicAmountForAsset.error);
    }
    const { maxAmountRequired, asset } = atomicAmountForAsset;
    if (!assetIsErc20(asset)) {
      throw new Error('Only ERC-20 asset payments are supported in this transport');
    }
    return [
      {
        scheme: 'exact',
        network,
        maxAmountRequired,
        resource: `mcp://tool/${toolName}`,
        description: `Payment for MCP tool: ${toolName}`,
        mimeType: 'application/json',
        payTo: getAddress(this.payTo),
        maxTimeoutSeconds: 60,
        asset: getAddress(asset.address),
        outputSchema: undefined,
        extra: asset.eip712,
      },
    ];
  }

  private async settlePayment(
    paymentInfo: PaymentInfo,
    response: JSONRPCResponse | JSONRPCError
  ): Promise<SettlementInfo> {
    if (isJSONRPCError(response)) {
      return { error: 'Response is an error' };
    }
    const toolName = paymentInfo.toolName;
    const usd = parseUsdPrice(paymentInfo.toolPrice as string);

    // [LOOP ADDITION] L402 settlement: the preimage is the proof; record to ledger.
    if (paymentInfo.rail === 'l402' && paymentInfo.l402 && this.lightning) {
      const { sats } = usdToSats(usd, this.lightning.btcUsdPrice);
      await this.ledger.record({
        rail: 'l402',
        tool: toolName,
        amount: String(sats),
        unit: 'sats',
        usd,
        proof: paymentInfo.l402.preimage,
      });
      return { rail: 'l402', preimage: paymentInfo.l402.preimage };
    }

    // x402 settlement (faithful original flow) + ledger row.
    try {
      const { settle } = useFacilitator(this.facilitator);
      if (!paymentInfo.payment) throw new Error('Missing x402 payment payload');
      const requirements = this.getX402RequirementsForTool(toolName, paymentInfo.toolPrice);
      const selected = findMatchingPaymentRequirements(requirements, paymentInfo.payment);
      if (!selected) throw new Error('Unable to find matching payment requirements');

      const settleResponse = await settle(paymentInfo.payment, selected);
      if (this.currentResponse) {
        this.responsePaymentHeaders.set(this.currentResponse, settleResponseHeader(settleResponse));
      }
      await this.ledger.record({
        rail: 'x402',
        tool: toolName,
        amount: selected.maxAmountRequired,
        unit: 'usdc-base-units',
        usd,
        proof: settleResponse.transaction,
      });
      return { rail: 'x402', transactionHash: settleResponse.transaction };
    } catch (error) {
      return { error: error instanceof Error ? error.message : String(error) };
    }
  }
}

/**
 * Create a Loop MCP dual-rail server transport.
 *
 * @param payTo       EVM wallet address that receives x402/USDC payments.
 * @param toolPricing Map of tool name -> USD price, e.g. { "get-quote": "$0.01" }.
 * @param options     Optional facilitator, network, and `lightning` config to
 *                    enable the second (L402/Lightning) rail.
 */
export function makePaymentAwareServerTransport(
  payTo: Address | string,
  toolPricing: Record<string, string>,
  options?: Partial<StreamableHTTPServerTransportOptions> & {
    facilitator?: FacilitatorConfig;
    network?: string;
    lightning?: LightningConfig;
    ledger?: LoopLedger;
  }
): LoopDualRailServerTransport {
  return new LoopDualRailServerTransport({
    payTo: getAddress(payTo),
    toolPricing,
    facilitator: options?.facilitator,
    network: options?.network,
    lightning: options?.lightning,
    ledger: options?.ledger,
    sessionIdGenerator: options?.sessionIdGenerator,
    enableJsonResponse: options?.enableJsonResponse,
  });
}
