import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import express from 'express';
import type { Wallet } from 'x402/types';
import {
  makePaymentAwareClientTransport,
  type DualRailClientOptions,
  type LightningPayer,
} from '../client.js';
import { makePaymentAwareServerTransport, type LightningConfig } from '../server.js';

/**
 * Proxies let you add (server proxy) or satisfy (client proxy) Loop MCP payments
 * without modifying an existing MCP server or a non-payment-aware MCP client
 * (e.g. Claude Desktop). Faithful to the original x402 proxy surface, extended
 * for the dual-rail config.
 */

/** Injects an upstream API key into proxied requests (server-proxy use). */
export class ApiKeyHook {
  constructor(private readonly apiKey: string) {}
  headers(): Record<string, string> {
    return { Authorization: `Bearer ${this.apiKey}` };
  }
}

export interface ClientProxyConfig {
  targetUrl: string;
  wallet: Wallet;
  mode?: 'stdio' | 'http';
  port?: number;
  /** [LOOP ADDITION] choose rail / supply a Lightning payer. */
  rail?: DualRailClientOptions['preferredRail'];
  lightningPayer?: LightningPayer;
  onPayment?: (proof: string, rail: 'x402' | 'l402') => void;
}

/**
 * Client proxy: sits in front of a payment-required server so a normal MCP client
 * can use it. Pays on the caller's behalf over the preferred rail.
 */
export async function createClientProxy(config: ClientProxyConfig) {
  const transport = makePaymentAwareClientTransport(config.targetUrl, config.wallet, {
    preferredRail: config.rail,
    lightningPayer: config.lightningPayer,
    paymentCallback: config.onPayment,
  });
  const client = new Client({ name: 'loop-mcp-client-proxy', version: '0.1.0' }, { capabilities: {} });
  await client.connect(transport);

  if ((config.mode ?? 'stdio') === 'http') {
    const app = express();
    app.use(express.json());
    app.post('/mcp', async (req, res) => {
      const result = await client.request(req.body, undefined as never);
      res.json(result);
    });
    const port = config.port ?? 3001;
    app.listen(port, () => console.error(`[loop-mcp] client proxy (http) on :${port} -> ${config.targetUrl}`));
  }
  return client;
}

export interface ServerProxyConfig {
  upstreamUrl: string;
  apiKey?: string;
  paymentWallet: string;
  toolPricing: Record<string, string>;
  port?: number;
  network?: string;
  /** [LOOP ADDITION] enable the Lightning/L402 rail on the monetized proxy. */
  lightning?: LightningConfig;
}

/**
 * Server proxy: wraps an existing (possibly API-key-protected) MCP server with
 * Loop MCP dual-rail payments, so external callers pay per tool while the proxy
 * forwards upstream using its own credentials.
 */
export async function createServerProxy(config: ServerProxyConfig) {
  // Upstream client (uses the operator's API key).
  const upstreamHook = config.apiKey ? new ApiKeyHook(config.apiKey) : undefined;
  const upstream = new Client({ name: 'loop-mcp-upstream', version: '0.1.0' }, { capabilities: {} });
  await upstream.connect(
    new StreamableHTTPClientTransport(new URL(config.upstreamUrl), {
      requestInit: upstreamHook ? { headers: upstreamHook.headers() } : undefined,
    })
  );

  // Downstream paid server: re-expose upstream tools behind payment.
  const server = new McpServer({ name: 'loop-mcp-server-proxy', version: '0.1.0' });
  const { tools } = await upstream.listTools();
  for (const tool of tools) {
    server.tool(tool.name, tool.description ?? '', {}, async (args) => {
      const res = await upstream.callTool({ name: tool.name, arguments: args });
      return res as never;
    });
  }

  const transport = makePaymentAwareServerTransport(config.paymentWallet, config.toolPricing, {
    network: config.network,
    lightning: config.lightning,
  });
  await server.connect(transport);

  const app = express();
  app.use(express.json());
  app.post('/mcp', async (req, res) => {
    await transport.handleRequest(req as never, res as never, req.body);
  });
  const port = config.port ?? 3002;
  app.listen(port, () => console.error(`[loop-mcp] server proxy on :${port} -> ${config.upstreamUrl}`));
  return { server, transport };
}
