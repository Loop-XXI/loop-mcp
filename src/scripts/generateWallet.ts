#!/usr/bin/env node
import { generatePrivateKey, privateKeyToAccount } from 'viem/accounts';

/** Generate a throwaway EVM wallet for the x402 rail (testnet use). */
const privateKey = generatePrivateKey();
const account = privateKeyToAccount(privateKey);

console.log('Private key:', privateKey);
console.log('Address:    ', account.address);
console.log('\nAdd to .env:  SENDER_PRIVATE_KEY=' + privateKey);
console.log('Fund on Base Sepolia: https://portal.cdp.coinbase.com/products/faucet');
