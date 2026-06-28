import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import cors from 'cors';
import express, { type Express } from 'express';
import z from 'zod';
import {
  LoopLedger,
  MockLightningProvider,
  PhoenixdProvider,
  makePaymentAwareServerTransport,
} from '../src/index.js';

/**
 * Dual-rail "todo" MCP server.
 *
 * Each tool is paid. Callers may pay over EITHER rail:
 *   - x402 / USDC on Base (X-PAYMENT header), or
 *   - L402 / Lightning sats (Authorization: L402 macaroon:preimage).
 * Both settle into one canonical, rail-tagged ledger.
 */

const PRICING = {
  'list-todos': '$0.001',
  'add-todo': '$0.002',
  'delete-todo': '$0.001',
} as const;

const todos: string[] = [];

// Use phoenixd if configured, otherwise the deterministic mock for local dev.
const lightningProvider =
  process.env.PHOENIXD_URL && process.env.PHOENIXD_PASSWORD
    ? new PhoenixdProvider(process.env.PHOENIXD_URL, process.env.PHOENIXD_PASSWORD)
    : new MockLightningProvider();

const ledger = new LoopLedger();

async function createMcpServer() {
  const mcpServer = new McpServer({ name: 'Loop Todo (dual-rail)', version: '0.1.0' });

  mcpServer.tool('list-todos', `List todos (pays ${PRICING['list-todos']})`, {}, async () => ({
    content: [{ type: 'text', text: JSON.stringify(todos) }],
  }));

  mcpServer.tool(
    'add-todo',
    `Add a todo (pays ${PRICING['add-todo']})`,
    { todo: z.string() },
    async ({ todo }) => {
      todos.push(todo);
      return { content: [{ type: 'text', text: `Added: ${todo}` }] };
    }
  );

  mcpServer.tool(
    'delete-todo',
    `Delete a todo by index (pays ${PRICING['delete-todo']})`,
    { index: z.number() },
    async ({ index }) => {
      todos.splice(index, 1);
      return { content: [{ type: 'text', text: `Removed index ${index}` }] };
    }
  );

  const transport = makePaymentAwareServerTransport(
    process.env.PAYMENT_WALLET_ADDRESS ?? '0x0000000000000000000000000000000000000000',
    PRICING,
    {
      network: process.env.NETWORK === 'base' ? 'base' : 'base-sepolia',
      ledger,
      lightning: {
        provider: lightningProvider,
        l402Secret: process.env.L402_SECRET ?? 'dev-secret-change-me',
        btcUsdPrice: Number(process.env.BTC_USD_PRICE ?? 65000),
      },
    }
  );

  await mcpServer.connect(transport);
  return { transport, mcpServer };
}

const app: Express = express();
app.use(express.json());
app.use(cors());

let instance: Awaited<ReturnType<typeof createMcpServer>> | null = null;
async function getInstance() {
  if (!instance) instance = await createMcpServer();
  return instance;
}

app.post('/mcp', async (req, res) => {
  const { transport } = await getInstance();
  await transport.handleRequest(req, res, req.body);
});

// Operator view of the canonical ledger.
app.get('/ledger', async (_req, res) => {
  res.json({ totals: ledger.totals(), rows: ledger.rows });
});

const port = process.env.PORT ?? 3022;
app.listen(port, () => console.error(`Loop dual-rail MCP server on :${port}`));
