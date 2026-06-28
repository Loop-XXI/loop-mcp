import { randomUUID } from 'node:crypto';
import type { LedgerEntry, Rail } from './types.js';

/**
 * Canonical, rail-tagged ledger.
 *
 * Loop XXI ground-truth doctrine: there is ONE canonical ledger
 * (`loop_sats_ledger` in production) and rows are not revenue until reconciled.
 * Every settled MCP payment, regardless of rail, lands here with its rail tag so
 * x402/USDC and L402/Lightning revenue roll up into a single net-sats north-star.
 *
 * Synthetic/test rows MUST be tagged `internal:*` and are excluded from canonical
 * revenue totals, mirroring the production gateway's first-revenue safeguards.
 */

export interface LedgerSink {
  append(entry: LedgerEntry): Promise<void> | void;
}

/** Default in-memory sink. Swap for a Supabase-backed sink in production. */
export class InMemoryLedgerSink implements LedgerSink {
  readonly rows: LedgerEntry[] = [];
  append(entry: LedgerEntry): void {
    this.rows.push(entry);
  }
}

export class LoopLedger {
  constructor(private readonly sink: LedgerSink = new InMemoryLedgerSink()) {}

  async record(args: {
    rail: Rail;
    tool: string;
    amount: string;
    unit: string;
    usd: number;
    proof: string;
    tag?: string;
  }): Promise<LedgerEntry> {
    const entry: LedgerEntry = {
      id: randomUUID(),
      ts: new Date().toISOString(),
      ...args,
    };
    await this.sink.append(entry);
    return entry;
  }

  /** All rows (including internal:* tagged). Only available on the in-memory sink. */
  get rows(): LedgerEntry[] {
    return this.sink instanceof InMemoryLedgerSink ? this.sink.rows : [];
  }

  /** Canonical (non-internal) rows only. */
  canonicalRows(): LedgerEntry[] {
    return this.rows.filter((r) => !r.tag?.startsWith('internal:'));
  }

  /** Total USD-equivalent of canonical revenue, broken down by rail. */
  totals(): { usd: number; byRail: Record<Rail, { usd: number; count: number }> } {
    const byRail: Record<Rail, { usd: number; count: number }> = {
      x402: { usd: 0, count: 0 },
      l402: { usd: 0, count: 0 },
    };
    let usd = 0;
    for (const r of this.canonicalRows()) {
      usd += r.usd;
      byRail[r.rail].usd += r.usd;
      byRail[r.rail].count += 1;
    }
    return { usd: round2(usd), byRail };
  }
}

function round2(n: number): number {
  return Math.round(n * 100) / 100;
}
