#!/usr/bin/env node
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { config } from 'dotenv';
import { createWalletClient, http, publicActions } from 'viem';
import { privateKeyToAccount } from 'viem/accounts';
import { baseSepolia } from 'viem/chains';
import type { Wallet } from 'x402/types';
import { makePaymentAwareClientTransport, type LightningPayer } from '../src/index.js';

config();

/**
 * Example MCP client. Set RAIL=l402 to pay over Lightning, otherwise x402/USDC.
 * For the Lightning rail you supply a `LightningPayer` that pays the invoice and
 * returns the preimage (here a stub; in production this is your LN wallet/LSP).
 */
async function main() {
  const rail = (process.env.RAIL as 'x402' | 'l402') ?? 'x402';
  const url = process.env.MCP_SERVER_URL ?? 'http://localhost:3022/mcp';

  const account = privateKeyToAccount(process.env.SENDER_PRIVATE_KEY as `0x${string}`);
  const wallet = createWalletClient({ account, chain: baseSepolia, transport: http() }).extend(publicActions);

  // A real payer would pay `invoice` over Lightning and return the preimage.
  const lightningPayer: LightningPayer = {
    async payInvoice(invoice) {
      throw new Error(`Connect a Lightning wallet to pay: ${invoice}`);
    },
  };

  const transport = makePaymentAwareClientTransport(url, wallet as Wallet, {
    preferredRail: rail,
    lightningPayer,
    paymentCallback: (proof, r) => console.log(`💰 paid via ${r}: ${proof}`),
  });

  const client = new Client({ name: 'loop-example-client', version: '0.1.0' }, { capabilities: {} });
  await client.connect(transport);

  const tools = await client.listTools();
  console.log('Tools:', tools.tools.map((t) => t.name).join(', '));

  const added = await client.callTool({ name: 'add-todo', arguments: { todo: 'pay agents over either rail' } });
  console.log('add-todo ->', JSON.stringify(added.content));

  const listed = await client.callTool({ name: 'list-todos', arguments: {} });
  console.log('list-todos ->', JSON.stringify(listed.content));

  await client.close();
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
