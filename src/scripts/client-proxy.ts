#!/usr/bin/env node
import { createWalletClient, http, publicActions } from 'viem';
import { privateKeyToAccount } from 'viem/accounts';
import * as chains from 'viem/chains';
import type { Wallet } from 'x402/types';
import { createClientProxy } from '../proxy/index.js';

/**
 * Loop MCP client-proxy CLI.
 *
 * Lets a non-payment-aware MCP client (e.g. Claude Desktop) talk to a
 * payment-required Loop MCP server. Pays on the caller's behalf over the
 * preferred rail.
 *
 *   TARGET_URL=https://server/mcp PRIVATE_KEY=0x... npx loop-mcp client-proxy
 *   MODE=http PORT=3001 RAIL=l402 ... npx loop-mcp client-proxy
 */
async function main() {
  const targetUrl = process.env.TARGET_URL;
  const privateKey = process.env.PRIVATE_KEY as `0x${string}` | undefined;
  const mode = (process.env.MODE as 'stdio' | 'http') ?? 'stdio';
  const port = process.env.PORT ? Number(process.env.PORT) : undefined;
  const networkName = (process.env.NETWORK ?? 'baseSepolia') as keyof typeof chains;
  const rail = (process.env.RAIL as 'x402' | 'l402') ?? 'x402';

  if (!targetUrl || !privateKey) {
    console.error('Set TARGET_URL and PRIVATE_KEY environment variables.');
    process.exit(1);
  }

  const chain = (chains[networkName] ?? chains.baseSepolia) as chains.Chain;
  const wallet = createWalletClient({
    account: privateKeyToAccount(privateKey),
    chain,
    transport: http(),
  }).extend(publicActions);

  await createClientProxy({
    targetUrl,
    wallet: wallet as Wallet,
    mode,
    port,
    rail,
    onPayment: (proof, r) => console.error(`[loop-mcp] paid via ${r}: ${proof}`),
  });
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
