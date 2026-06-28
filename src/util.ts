/// <reference lib="dom" />

/**
 * Converts various header formats to a plain object.
 *
 * Workaround for an x402-fetch bug where Headers objects are not preserved during
 * 402 payment retries (the library spreads `...init.headers`, which spreads a
 * Headers object's methods instead of its entries). Without this, MCP's required
 * `Accept: application/json, text/event-stream` header is lost on retry, causing
 * 406 Not Acceptable. See https://github.com/coinbase/x402/pull/314
 */
export function convertHeaders(headers?: HeadersInit): Record<string, string> {
  const headersObject: Record<string, string> = {};
  if (!headers) return headersObject;

  if (headers instanceof Headers) {
    headers.forEach((value, key) => {
      headersObject[key] = value;
    });
  } else if (Array.isArray(headers)) {
    headers.forEach(([key, value]) => {
      headersObject[key] = value;
    });
  } else {
    Object.assign(headersObject, headers);
  }
  return headersObject;
}
