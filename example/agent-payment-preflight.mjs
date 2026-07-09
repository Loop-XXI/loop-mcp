#!/usr/bin/env node
/**
 * Buyer-agent preflight: read an agent-payments.json manifest and decide
 * whether the service is OK_TO_PAY or DO_NOT_PAY under a sats budget.
 *
 * Zero dependencies. Node 18+ (global fetch).
 *
 * Usage:
 *   node example/agent-payment-preflight.mjs [manifest-url] [--max-sats=N]
 *
 * Examples:
 *   node example/agent-payment-preflight.mjs
 *   node example/agent-payment-preflight.mjs https://mcp.loopxxi.com/agent-payments.json --max-sats=25
 */

const DEFAULT_MANIFEST_URL = "https://mcp.loopxxi.com/.well-known/agent-payments.json";
const DEFAULT_MAX_SATS = 25;

function errorExit(message, code = 1) {
  console.error(`DO_NOT_PAY: ${message}`);
  process.exit(code);
}

function parseArgs(argv) {
  let manifestUrl = DEFAULT_MANIFEST_URL;
  let maxSats = DEFAULT_MAX_SATS;

  for (const arg of argv.slice(2)) {
    if (arg.startsWith("--max-sats=")) {
      const value = arg.slice("--max-sats=".length);
      const parsed = Number(value);
      if (!Number.isFinite(parsed) || parsed < 0 || !Number.isInteger(parsed)) {
        errorExit(`invalid --max-sats value: ${value}`);
      }
      maxSats = parsed;
    } else if (arg === "--help" || arg === "-h") {
      console.log(`usage: node example/agent-payment-preflight.mjs [manifest-url] [--max-sats=N]`);
      process.exit(0);
    } else if (!arg.startsWith("--")) {
      manifestUrl = arg;
    } else {
      errorExit(`unknown argument: ${arg}`);
    }
  }

  return { manifestUrl, maxSats };
}

function getNested(obj, ...keys) {
  let current = obj;
  for (const key of keys) {
    if (current == null || typeof current !== "object") return undefined;
    current = current[key];
  }
  return current;
}

function firstAcceptsRail(paymentTerms) {
  if (!Array.isArray(paymentTerms.accepts)) return undefined;
  return paymentTerms.accepts[0];
}

async function main() {
  const { manifestUrl, maxSats } = parseArgs(process.argv);

  console.log(`Fetching manifest: ${manifestUrl}`);
  console.log(`Agent max budget: ${maxSats} sats\n`);

  let response;
  try {
    response = await fetch(manifestUrl, {
      headers: { Accept: "application/json" },
    });
  } catch (err) {
    errorExit(`fetch failed: ${err.message}`);
  }

  if (!response.ok) {
    errorExit(`manifest returned HTTP ${response.status}`);
  }

  let manifest;
  try {
    manifest = await response.json();
  } catch (err) {
    errorExit(`manifest JSON parse failed: ${err.message}`);
  }

  const service = getNested(manifest, "service");
  const provider = getNested(manifest, "provider") ?? getNested(service, "provider") ?? getNested(service, "provider_url");
  const providerLabel = typeof provider === "object" && provider !== null
    ? [provider.name, provider.email].filter(Boolean).join(" ")
    : provider;
  const serviceName = getNested(service, "name") ?? "unknown";
  const serviceVersion = getNested(service, "version") ?? "";
  const paymentTerms = getNested(manifest, "payment_terms") ?? getNested(manifest, "safety_and_terms");
  const maxPriceSats = paymentTerms?.max_price_sats ?? service?.max_price_sats ?? undefined;

  console.log("Provider:", providerLabel ?? "(not specified)");
  console.log("Service:", serviceName + (serviceVersion ? ` v${serviceVersion}` : ""));

  const paymentRails = Array.isArray(manifest.payment_rails)
    ? manifest.payment_rails
    : getNested(paymentTerms, "rails") ?? [];
  const acceptedRails = paymentRails
    .map((rail) => rail?.name ?? rail?.type ?? String(rail))
    .filter(Boolean);
  console.log("Accepted rails:", acceptedRails.length ? acceptedRails.join(", ") : "(none listed)");

  console.log("Max price sats:", maxPriceSats ?? "(not specified)");

  const tools = Array.isArray(manifest.tools) ? manifest.tools : [];
  console.log("\nTool catalog:");
  if (tools.length === 0) {
    console.log("  (no tools listed)");
  } else {
    const nameWidth = Math.max(4, ...tools.map((t) => String(t.name ?? "").length));
    const priceWidth = Math.max(5, ...tools.map((t) => String(t.price_sats ?? "?").length));
    console.log(`  ${"Tool".padEnd(nameWidth)}  ${"Sats".padStart(priceWidth)}  Description`);
    console.log(`  ${"-".repeat(nameWidth)}  ${"-".repeat(priceWidth)}  -----------`);
    for (const tool of tools) {
      const name = String(tool.name ?? "?").padEnd(nameWidth);
      const price = String(tool.price_sats ?? "?").padStart(priceWidth);
      const description = String(tool.description ?? "").slice(0, 60);
      console.log(`  ${name}  ${price}  ${description}`);
    }
  }

  const firstRail = firstAcceptsRail(paymentTerms ?? {});

  if (acceptedRails.length === 0) {
    errorExit("payment terms missing or no accepted rails");
  }

  if (tools.length === 0) {
    errorExit("no tools advertised in manifest");
  }

  if (maxPriceSats === undefined || maxPriceSats === null) {
    errorExit("max_price_sats not specified in manifest");
  }

  if (typeof maxPriceSats !== "number" || maxPriceSats < 0) {
    errorExit(`invalid max_price_sats: ${maxPriceSats}`);
  }

  if (maxPriceSats > maxSats) {
    errorExit(
      `manifest max_price_sats (${maxPriceSats}) exceeds agent budget (${maxSats})`
    );
  }

  const railSummary = firstRail?.rail ?? acceptedRails.join(" | ");
  const unitSummary = firstRail?.unit ?? "sats";

  console.log(
    `\nAgent decision: OK_TO_PAY — max ${maxPriceSats} ${unitSummary} via ${railSummary}`
  );
  process.exit(0);
}

main().catch((err) => errorExit(err.message ?? String(err)));
