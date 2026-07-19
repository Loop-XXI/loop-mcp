package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Loop-XXI/loop-mcp/tools"
)

// ────────────────────────────────────────────────────────────────────────────
// Config
// ────────────────────────────────────────────────────────────────────────────

type Config struct {
	Port             string
	PhoenixdURL      string
	PhoenixdPassword string
	MacaroonSecret   string
	GatewayURL       string // Loop Gateway base URL for fiat credit-key debits
}

func loadConfig() Config {
	phoenixPw := os.Getenv("PHOENIXD_HTTP_PASSWORD")
	if phoenixPw == "" {
		phoenixPw = os.Getenv("PHOENIXD_PASSWORD")
	}
	macaroon := os.Getenv("MACAROON_HMAC_SECRET")
	if macaroon == "" {
		macaroon = "loop-mcp-default-macaroon-secret-change-me"
	}
	log.Printf("loop-mcp v2 config: port=%s phoenixd=%s phoenixPwSet=%v macaroonSet=%v",
		envOrDefault("PORT", "8080"), envOrDefault("PHOENIXD_URL", "http://localhost:9740"),
		phoenixPw != "", macaroon != "")
	return Config{
		Port:             envOrDefault("PORT", "8080"),
		PhoenixdURL:      envOrDefault("PHOENIXD_URL", "http://localhost:9740"),
		PhoenixdPassword: phoenixPw,
		MacaroonSecret:   macaroon,
		GatewayURL:       envOrDefault("LOOP_GATEWAY_URL", "https://api.loopxxi.com"),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

// ────────────────────────────────────────────────────────────────────────────
// MCP server card (Smithery / directory metadata)
// ────────────────────────────────────────────────────────────────────────────

// serveServerCard serves the MCP server-card metadata used by Smithery and
// registry crawlers. Prices are read from tools.All() so they stay in sync
// with the production gate.
func serveServerCard(c *gin.Context) {
	toolList := make([]gin.H, 0, len(tools.All()))
	for _, t := range tools.All() {
		toolList = append(toolList, gin.H{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
			"annotations": t.Annotations,
			"price": gin.H{
				"currency": "sats",
				"amount":   t.SatsPrice,
			},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"schema_version": "1.0",
		"id":             "loop-mcp",
		"name":           "loop-mcp",
		"version":        "2.3.0",
		"description":    "Pay-per-call MCP server with 15 Bitcoin, data, text, and developer utilities over Lightning or prepaid credits.",
		"license":        "MIT",
		"provider": gin.H{
			"name":  "Loop XXI LLC",
			"email": "business@loopxxi.com",
			"url":   "https://loopxxi.com",
		},
		"repository": gin.H{
			"type": "git",
			"url":  "https://github.com/Loop-XXI/loop-mcp",
		},
		"endpoints": []gin.H{
			{
				"url":              "https://mcp.loopxxi.com/mcp",
				"transport":        "streamable-http",
				"protocol_version": "2024-11-05",
				"authentication":   "L402",
				"auth_model":       "payment-required",
			},
		},
		"tools": toolList,
	})
}

// serveAgentPaymentManifest serves machine-readable payment metadata for buyer
// agents. It is generated from tools.All() so the price/tool list is always
// current. Routes: GET /.well-known/agent-payments.json and
// GET /agent-payments.json.
func serveAgentPaymentManifest(c *gin.Context) {
	maxPrice := int64(0)
	toolList := make([]gin.H, 0, len(tools.All()))
	for _, t := range tools.All() {
		if t.SatsPrice > maxPrice {
			maxPrice = t.SatsPrice
		}
		toolList = append(toolList, gin.H{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
			"price_sats":   t.SatsPrice,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"schema_version": "1.0.0",
		"provider": gin.H{
			"name":  "Loop XXI LLC",
			"url":   "https://loopxxi.com",
			"email": "business@loopxxi.com",
		},
		"service": gin.H{
			"id":          "loop-mcp",
			"name":        "loop-mcp",
			"version":     "2.3.0",
			"description": "Pay-per-call MCP server with 15 Bitcoin, data, text, and developer utilities over Lightning or prepaid credits.",
		},
		"endpoints": []gin.H{
			{
				"type":                "mcp",
				"url":                 "https://mcp.loopxxi.com/mcp",
				"transport":           "streamable-http",
				"protocol_version":    "2024-11-05",
				"authentication":      "L402",
				"auth_model":          "payment-required",
				"payment_requirement": "per-request",
			},
			{
				"type":           "rest",
				"url":            "https://mcp.loopxxi.com/l402/btc_price",
				"transport":      "https",
				"authentication": "L402",
				"auth_model":     "payment-required",
				"tool":           "btc_price",
				"description":    "Read-only REST probe endpoint for btc_price. Returns 402 with Lightning invoice if unpaid.",
			},
		},
		"payment_rails": []gin.H{
			{
				"name":          "L402 Lightning",
				"type":          "lightning",
				"description":   "Pay per call via BOLT11 invoice. Re-present Authorization: L402 <token>:<preimage> after payment.",
				"currency":      "BTC",
				"unit":          "sat",
				"pricing_model": "per-request",
			},
			{
				"name":          "Fiat credits",
				"type":          "bearer_token",
				"description":   "Prepaid fiat credit keys issued by Loop Gateway. Use Authorization: Bearer loop_<credit_key>.",
				"currency":      "USD",
				"unit":          "credit",
				"pricing_model": "per-request-debit",
			},
		},
		"safety_and_terms": gin.H{
			"billing_model":          "Per-request pricing. No subscription, no recurring charges.",
			"max_price_sats":         maxPrice,
			"personal_data_required": false,
			"payment_is_credential":  true,
			"refund_policy":          "All sales are final once a tool result is delivered. Disputes to business@loopxxi.com.",
			"contact":                "business@loopxxi.com",
			"terms_url":              "https://loopxxi.com/terms",
			"privacy_url":            "https://loopxxi.com/privacy",
		},
		"tools": toolList,
		"examples": gin.H{
			"preflight": "curl -s https://mcp.loopxxi.com/.well-known/agent-payments.json",
			"l402_flow": []string{
				"# 1. Call without auth → receive 402 + invoice + token",
				"curl -X POST https://mcp.loopxxi.com/mcp -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"btc_price\",\"arguments\":{}}}'",
				"# 2. Pay the BOLT11 invoice out-of-band, then retry with proof",
				"curl -X POST https://mcp.loopxxi.com/mcp -H 'Content-Type: application/json' -H 'Authorization: L402 <TOKEN>:<PREIMAGE>' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"btc_price\",\"arguments\":{}}}'",
			},
			"fiat_credit_flow": []string{
				"# 1. Obtain a loop_ credit key from Loop Gateway",
				"# 2. Use the credit key as Bearer token on every call",
				"curl -X POST https://mcp.loopxxi.com/mcp -H 'Content-Type: application/json' -H 'Authorization: Bearer loop_<CREDIT_KEY>' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"btc_price\",\"arguments\":{}}}'",
			},
		},
	})
}

// serveL402Manifest serves the Lightning Enable-compatible discovery manifest.
// It advertises the REST-shaped paid endpoints that a generic L402 buyer can
// call without constructing an MCP JSON-RPC body.
func serveL402Manifest(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"$schema": "https://docs.lightningenable.com/schemas/l402-manifest-v1.json",
		"version": "1.0",
		"service": gin.H{
			"name":                 "loop-mcp Bitcoin Agent Tools",
			"description":          "Focused Bitcoin intelligence and developer utilities for autonomous agents, paid per call over L402.",
			"base_url":             "https://mcp.loopxxi.com",
			"logo_url":             nil,
			"contact_email":        "business@loopxxi.com",
			"documentation_url":    "https://github.com/Loop-XXI/loop-mcp",
			"terms_of_service_url": "https://loopxxi.com/terms",
			"categories":           []string{"bitcoin", "finance", "agent-tools", "data"},
		},
		"l402": gin.H{
			"default_price_sats": 10,
			"payment_flow":       "402_challenge_response",
			"auth_header_format": "Authorization: L402 <macaroon>:<preimage>",
			"capabilities": gin.H{
				"preimage_in_response": true,
				"supported_currencies": []string{"BTC"},
			},
		},
		"endpoints": []gin.H{
			{
				"id":           "get-btc-price",
				"path":         "/l402/btc_price",
				"method":       "GET",
				"summary":      "Current Bitcoin price",
				"description":  "Current Bitcoin price in USD and major fiat currencies, returned as structured JSON.",
				"pricing":      gin.H{"model": "perrequest", "base_price_sats": 10},
				"l402_enabled": true,
			},
			{
				"id":           "get-btc-send-decision",
				"path":         "/l402/btc_send_decision",
				"method":       "GET",
				"summary":      "Bitcoin send-or-wait decision",
				"description":  "Live SEND_NOW, WAIT, or URGENT_ONLY recommendation using mempool pressure and fee savings.",
				"pricing":      gin.H{"model": "perrequest", "base_price_sats": 15},
				"l402_enabled": true,
			},
			{
				"id":           "get-optimal-send-window",
				"path":         "/l402/optimal_send_window",
				"method":       "GET",
				"summary":      "Optimal Bitcoin send window",
				"description":  "Congestion forecast and recommended UTC send window with confidence and RBF viability.",
				"pricing":      gin.H{"model": "perrequest", "base_price_sats": 25},
				"l402_enabled": true,
			},
		},
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// ────────────────────────────────────────────────────────────────────────────
// L402 macaroon helpers
// ────────────────────────────────────────────────────────────────────────────

// issueToken creates an HMAC-SHA256 token tied to a specific tool + payment hash.
// Format: "<paymentHash>:<toolName>:<ts>:<hmac>"
func issueToken(secret, paymentHash, toolName string, ts int64) string {
	msg := fmt.Sprintf("%s:%s:%d", paymentHash, toolName, ts)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	sig := hex.EncodeToString(mac.Sum(nil))
	raw := fmt.Sprintf("%s:%s:%d:%s", paymentHash, toolName, ts, sig)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// verifyToken validates the token and returns (paymentHash, toolName, ok).
// Tokens expire with the one-hour Lightning invoice.
func verifyToken(secret, token string) (paymentHash, toolName string, ok bool) {
	return verifyTokenAt(secret, token, time.Now().Unix())
}

func verifyTokenAt(secret, token string, nowUnix int64) (paymentHash, toolName string, ok bool) {
	// New challenges use base64url so indexers and legacy L402 parsers accept
	// the opaque token. Continue accepting pre-migration colon-delimited tokens.
	if decoded, err := base64.RawURLEncoding.DecodeString(token); err == nil {
		token = string(decoded)
	}
	parts := strings.Split(token, ":")
	if len(parts) != 4 {
		return "", "", false
	}
	ph, tn, tsStr, sig := parts[0], parts[1], parts[2], parts[3]
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", "", false
	}
	age := nowUnix - ts
	if age < 0 || age > 3600 {
		return "", "", false
	}
	msg := fmt.Sprintf("%s:%s:%d", ph, tn, ts)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", "", false
	}
	return ph, tn, true
}

func verifyPaymentPreimage(paymentHash, preimage string) bool {
	preimageBytes, err := hex.DecodeString(preimage)
	if err != nil || len(preimageBytes) != 32 {
		return false
	}
	hashBytes := sha256.Sum256(preimageBytes)
	computedHash := hex.EncodeToString(hashBytes[:])
	return hmac.Equal([]byte(strings.ToLower(paymentHash)), []byte(computedHash))
}

// consumedPaymentHashes makes each L402 proof single-use within the running
// service. This prevents one paid invoice from being replayed for repeated
// tool execution. Railway runs a single service instance for this deployment.
var consumedPaymentHashes sync.Map

func consumePaymentPreimage(paymentHash, preimage string) bool {
	if !verifyPaymentPreimage(paymentHash, preimage) {
		return false
	}
	_, alreadyConsumed := consumedPaymentHashes.LoadOrStore(strings.ToLower(paymentHash), struct{}{})
	return !alreadyConsumed
}

// ────────────────────────────────────────────────────────────────────────────
// phoenixd invoice creation (used only by L402 middleware to gate tool calls)
// ────────────────────────────────────────────────────────────────────────────

type phoenixdInvoiceResponse struct {
	Serialized  string `json:"serialized"`
	PaymentHash string `json:"paymentHash"`
}

func createPhoenixdInvoice(cfg Config, amountSats int64, description string) (bolt11, paymentHash string, err error) {
	body := fmt.Sprintf("amountSat=%d&description=%s&expirySeconds=3600",
		amountSats, strings.ReplaceAll(description, " ", "+"))

	req, err := http.NewRequest("POST", cfg.PhoenixdURL+"/createinvoice",
		strings.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("", cfg.PhoenixdPassword)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("phoenixd request: %w", err)
	}
	defer resp.Body.Close()

	var result phoenixdInvoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("phoenixd decode: %w", err)
	}
	if result.Serialized == "" {
		return "", "", fmt.Errorf("phoenixd returned empty invoice")
	}
	return result.Serialized, result.PaymentHash, nil
}

// ────────────────────────────────────────────────────────────────────────────
// MCP request/response types (JSON-RPC 2.0)
// ────────────────────────────────────────────────────────────────────────────

type MCPRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type MCPResponse struct {
	Jsonrpc string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func mcpOK(id interface{}, result interface{}) MCPResponse {
	return MCPResponse{Jsonrpc: "2.0", ID: id, Result: result}
}

func mcpErr(id interface{}, code int, msg string) MCPResponse {
	return MCPResponse{Jsonrpc: "2.0", ID: id, Error: &MCPError{Code: code, Message: msg}}
}

// ────────────────────────────────────────────────────────────────────────────
// Loop Gateway fiat credit-key debit (second payment rail alongside L402)
// ────────────────────────────────────────────────────────────────────────────

// gatewayDebitResponse is the response from POST /v1/credits/debit on Loop Gateway.
type gatewayDebitResponse struct {
	Status      string `json:"status"`
	Tool        string `json:"tool"`
	DebitedSats int64  `json:"debited_sats"`
	BalanceSats int64  `json:"balance_sats"`
}

// debitGatewayCredit atomically debits sats from a prepaid account via Loop
// Gateway's /v1/credits/debit endpoint. The caller's own credit_key (a loop_
// bearer token) is forwarded as Bearer — Loop Gateway debits the agent's own
// balance. Returns (ok, error): ok=true means the debit succeeded and the
// caller may serve the tool.
func debitGatewayCredit(cfg Config, creditKey string, toolName string, sats int64) (bool, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"amount_sats": sats,
		"tool":        toolName,
	})
	req, err := http.NewRequest("POST", cfg.GatewayURL+"/v1/credits/debit", strings.NewReader(string(body)))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+creditKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// ────────────────────────────────────────────────────────────────────────────
// L402 middleware
// ────────────────────────────────────────────────────────────────────────────

// l402Middleware:
//  1. Parses the MCP request body and stores it in Gin context.
//  2. For tools/call: verifies Authorization: L402 <token...ge>.
//  3. If absent/invalid: creates a phoenixd invoice and returns HTTP 402.
//  4. If valid: injects toolName + toolArgs into context and calls Next().
func l402Middleware(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req MCPRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, mcpErr(nil, -32700, "parse error"))
			c.Abort()
			return
		}
		c.Set("mcpRequest", req)

		if req.Method != "tools/call" {
			c.Next()
			return
		}

		var callParams struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &callParams)
		toolName := callParams.Name

		tool, err := tools.ByName(toolName)
		if err != nil {
			c.JSON(http.StatusOK, mcpErr(req.ID, -32601, fmt.Sprintf("unknown tool: %s", toolName)))
			c.Abort()
			return
		}

		// ── FIAT PATH: Bearer loop_<credit_key> (Loop Gateway prepaid debit) ──
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer loop_") || strings.HasPrefix(authHeader, "Bearer smartsat_") {
			creditKey := authHeader[7:] // strip "Bearer "
			ok, derr := debitGatewayCredit(cfg, creditKey, toolName, tool.SatsPrice)
			if derr != nil {
				log.Printf("gateway debit error for %s: %v", toolName, derr)
				c.JSON(http.StatusServiceUnavailable, mcpErr(req.ID, -32000, "payment processor unavailable"))
				c.Abort()
				return
			}
			if !ok {
				c.JSON(http.StatusPaymentRequired, gin.H{
					"error": gin.H{
						"code":           402,
						"message":        "Insufficient credit balance. Top up at https://api.loopxxi.com/ai-credits",
						"type":           "insufficient_funds",
						"refill_url":     "https://api.loopxxi.com/ai-credits",
						"requested_sats": tool.SatsPrice,
					},
				})
				c.Abort()
				return
			}
			c.Set("payment_method", "fiat_credit")
			c.Set("toolName", toolName)
			c.Set("toolArgs", callParams.Arguments)
			c.Next()
			return
		}

		// Verify and atomically consume the L402 token.
		if strings.HasPrefix(authHeader, "L402 ") {
			cred := authHeader[5:]
			lastColon := strings.LastIndex(cred, ":")
			if lastColon > 0 {
				token, preimage := cred[:lastColon], cred[lastColon+1:]
				ph, tn, ok := verifyToken(cfg.MacaroonSecret, token)
				if ok && tn == toolName && consumePaymentPreimage(ph, preimage) {
					c.Set("toolName", toolName)
					c.Set("toolArgs", callParams.Arguments)
					c.Next()
					return
				}
			}
		}

		// Issue a 402 with a fresh Lightning invoice
		description := fmt.Sprintf("loop-mcp: %s (%d sats)", toolName, tool.SatsPrice)
		bolt11, paymentHash, err := createPhoenixdInvoice(cfg, tool.SatsPrice, description)
		if err != nil {
			log.Printf("invoice creation failed: %v", err)
			c.JSON(http.StatusServiceUnavailable, mcpErr(req.ID, -32000, "payment infrastructure unavailable"))
			c.Abort()
			return
		}

		ts := time.Now().Unix()
		token := issueToken(cfg.MacaroonSecret, paymentHash, toolName, ts)

		c.Header("WWW-Authenticate", fmt.Sprintf("L402 %s:%s", token, bolt11))
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error": gin.H{
				"code":            402,
				"message":         fmt.Sprintf("Payment required: %d sats for %s", tool.SatsPrice, toolName),
				"payment_request": bolt11,
				"token":           token,
				"sats":            tool.SatsPrice,
				"instructions":    "Pay the BOLT11 invoice, then retry with Authorization: L402 <token...ge>",
			},
		})
		c.Abort()
	}
}

// ────────────────────────────────────────────────────────────────────────────
// MCP handlers
// ────────────────────────────────────────────────────────────────────────────

func handleMCP(c *gin.Context) {
	reqRaw, exists := c.Get("mcpRequest")
	if !exists {
		c.JSON(http.StatusBadRequest, mcpErr(nil, -32700, "parse error"))
		return
	}
	req := reqRaw.(MCPRequest)

	switch req.Method {

	case "initialize":
		c.JSON(http.StatusOK, mcpOK(req.ID, gin.H{
			"protocolVersion": "2024-11-05",
			"serverInfo": gin.H{
				"name":    "loop-mcp",
				"version": "2.3.0",
			},
			"capabilities": gin.H{
				"tools": gin.H{},
			},
		}))

	case "tools/list":
		var toolList []gin.H
		for _, t := range tools.All() {
			toolList = append(toolList, gin.H{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
				"annotations": t.Annotations,
			})
		}
		c.JSON(http.StatusOK, mcpOK(req.ID, gin.H{"tools": toolList}))

	case "tools/call":
		toolNameRaw, _ := c.Get("toolName")
		toolArgsRaw, _ := c.Get("toolArgs")
		toolName := toolNameRaw.(string)
		toolArgs, _ := toolArgsRaw.(json.RawMessage)

		result, err := dispatchTool(toolName, toolArgs)
		if err != nil {
			c.JSON(http.StatusOK, mcpErr(req.ID, -32000, err.Error()))
			return
		}

		resultJSON, _ := json.Marshal(result)
		c.JSON(http.StatusOK, mcpOK(req.ID, gin.H{
			"content": []gin.H{
				{"type": "text", "text": string(resultJSON)},
			},
		}))

	default:
		c.JSON(http.StatusOK, mcpErr(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method)))
	}
}

// dispatchTool routes a verified tools/call to the correct handler.
func dispatchTool(name string, args json.RawMessage) (interface{}, error) {
	if args == nil {
		args = json.RawMessage("{}")
	}
	switch name {
	case "btc_price":
		return tools.HandleBtcPrice(args)
	case "btc_send_decision":
		return tools.HandleBtcSendDecision(args)
	case "lightning_address_resolve":
		return tools.HandleLightningAddressResolve(args)
	case "tx_decode_explain":
		return tools.HandleTxDecodeExplain(args)
	case "optimal_send_window":
		return tools.HandleOptimalSendWindow(args)
	case "json_validate":
		return tools.HandleJSONValidate(args)
	case "json_extract":
		return tools.HandleJSONExtract(args)
	case "csv_to_json":
		return tools.HandleCSVToJSON(args)
	case "text_analyze":
		return tools.HandleTextAnalyze(args)
	case "hash_generate":
		return tools.HandleHashGenerate(args)
	case "base64_convert":
		return tools.HandleBase64Convert(args)
	case "timestamp_convert":
		return tools.HandleTimestampConvert(args)
	case "uuid_generate":
		return tools.HandleUUIDGenerate(args)
	case "url_parse":
		return tools.HandleURLParse(args)
	case "jwt_decode":
		return tools.HandleJWTDecode(args)
	default:
		return nil, fmt.Errorf("no handler for tool: %s", name)
	}
}

// handleRESTL402Tool exposes a simple REST-shaped L402 endpoint for directories
// that probe URLs without a JSON-RPC body. It reuses the same token, invoice,
// and preimage verification model as POST /mcp, but returns a plain JSON tool
// result once paid.
func handleRESTL402Tool(cfg Config, toolName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tool, err := tools.ByName(toolName)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown tool"})
			return
		}

		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "L402 ") {
			cred := authHeader[5:]
			lastColon := strings.LastIndex(cred, ":")
			if lastColon > 0 {
				token, preimage := cred[:lastColon], cred[lastColon+1:]
				ph, tn, ok := verifyToken(cfg.MacaroonSecret, token)
				if ok && tn == toolName && consumePaymentPreimage(ph, preimage) {
					result, err := dispatchTool(toolName, json.RawMessage("{}"))
					if err != nil {
						c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
						return
					}
					c.JSON(http.StatusOK, gin.H{"tool": toolName, "paid": true, "result": result})
					return
				}
			}
		}

		description := fmt.Sprintf("loop-mcp REST: %s (%d sats)", toolName, tool.SatsPrice)
		bolt11, paymentHash, err := createPhoenixdInvoice(cfg, tool.SatsPrice, description)
		if err != nil {
			log.Printf("REST invoice creation failed: %v", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment infrastructure unavailable"})
			return
		}

		token := issueToken(cfg.MacaroonSecret, paymentHash, toolName, time.Now().Unix())
		c.Header("WWW-Authenticate", fmt.Sprintf("L402 version=\"0\", token=\"%s\", macaroon=\"%s\", invoice=\"%s\"", token, token, bolt11))
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":           "payment_required",
			"message":         fmt.Sprintf("Payment required: %d sats for %s", tool.SatsPrice, toolName),
			"payment_request": bolt11,
			"token":           token,
			"sats":            tool.SatsPrice,
			"instructions":    "Pay the BOLT11 invoice, then retry with Authorization: L402 <token...ge>",
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Landing page + free try endpoint (lead-gen)
// ────────────────────────────────────────────────────────────────────────────

// landingHTML is the branded public landing page served at GET /.
// Kept minimal and on-brand (Obsidian/Bone, Inter, no accent color).
const landingHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>loop-mcp | 15 pay-per-call tools for AI agents</title><meta name="description" content="One MCP endpoint for 15 focused Bitcoin, data, text, and developer utilities. Pay per call with prepaid credits or Lightning L402."><meta name="theme-color" content="#050505"><link rel="preconnect" href="https://rsms.me/"><link rel="stylesheet" href="https://rsms.me/inter/inter.css"><style>
:root{--bg:#050505;--ink:#fafafa;--dim:#8f8c86;--faint:#4a4844;--card:#0d0d0c;--line:#1f1e1b;--line2:#2c2a26;--btc:#f7931a;--btc-dim:#8a5310;--max:1160px}*,*::before,*::after{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;background:var(--bg);color:var(--ink);font-family:Inter,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;line-height:1.5;-webkit-font-smoothing:antialiased}a{color:inherit;text-decoration:none}.wrap{width:min(var(--max),calc(100% - 48px));margin:auto}header{height:72px;border-bottom:1px solid var(--line);display:flex;align-items:center}header .wrap{display:flex;align-items:center;justify-content:space-between}.brand{font-size:14px;font-weight:650;letter-spacing:-.02em}.brand span{color:var(--btc)}nav{display:flex;align-items:center;gap:25px}nav a{font-size:13px;color:var(--dim)}nav a:hover{color:var(--ink)}nav .buy{color:var(--ink);border:1px solid var(--line2);border-radius:99px;padding:8px 16px}.kicker{font-size:10px;font-weight:700;letter-spacing:.2em;text-transform:uppercase;color:var(--btc)}
.hero{text-align:center;padding:110px 0 96px;overflow:hidden}.hero h1{font-size:clamp(54px,8.6vw,100px);line-height:.95;letter-spacing:-.06em;font-weight:530;max-width:900px;margin:22px auto 24px}.hero .lede{font-size:18px;color:var(--dim);max-width:560px;margin:0 auto 38px;line-height:1.65}.actions{display:flex;gap:11px;justify-content:center;flex-wrap:wrap}.button{display:inline-flex;align-items:center;justify-content:center;min-height:50px;padding:0 24px;border:1px solid var(--line2);border-radius:99px;font-size:13px;font-weight:650}.button.primary{background:var(--ink);color:#000;border-color:var(--ink)}.stats{display:grid;grid-template-columns:repeat(4,1fr);max-width:880px;margin:70px auto 0;border-top:1px solid var(--line);border-bottom:1px solid var(--line)}.stat{padding:25px 8px}.stat+.stat{border-left:1px solid var(--line)}.stat strong{display:block;font-size:30px;letter-spacing:-.03em}.stat span{display:block;font-size:10px;color:var(--dim);letter-spacing:.1em;text-transform:uppercase}
section{padding:108px 0;border-top:1px solid var(--line)}.title{text-align:center;max-width:640px;margin:0 auto 64px}.title h2{font-size:clamp(38px,5vw,58px);line-height:1.02;letter-spacing:-.045em;font-weight:530;margin:14px 0}.title p{font-size:15px;color:var(--dim);margin:0}.tool-grid{display:grid;grid-template-columns:repeat(5,1fr);gap:12px}.tool{aspect-ratio:1;background:var(--card);border:1px solid var(--line);border-radius:18px;padding:20px;display:flex;flex-direction:column;justify-content:space-between;transition:transform .18s,border-color .18s}.tool:hover{transform:translateY(-3px);border-color:var(--line2)}.tool-icon{width:42px;height:42px;border:1px solid #39342a;border-radius:12px;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:700;color:var(--btc);font-family:ui-monospace,SFMono-Regular,Menlo,monospace}.tool code{font-size:11px;color:var(--ink);overflow-wrap:anywhere}.tool-price{font-size:10px;color:var(--dim)}.tool.hot{border-color:#3e301d;background:linear-gradient(145deg,#15120d,var(--card))}
.rails{display:grid;grid-template-columns:1fr 1fr;gap:20px}.rail{background:var(--card);border:1px solid var(--line);border-radius:20px;padding:50px 42px;text-align:center}.rail svg{width:74px;height:74px;margin-bottom:24px}.rail h3{font-size:29px;letter-spacing:-.03em;margin:10px 0 12px}.rail p{font-size:14px;color:var(--dim);max-width:370px;margin:0 auto 24px;line-height:1.65}.rail code{display:block;font-size:11px;color:var(--faint);padding:11px;border:1px solid var(--line);border-radius:9px;background:#080807;margin:0 0 25px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.preview{display:grid;grid-template-columns:1fr 1fr;gap:20px;align-items:stretch}.preview-art,.preview-copy{background:var(--card);border:1px solid var(--line);border-radius:20px}.preview-art{display:flex;align-items:center;justify-content:center;min-height:410px}.preview-art svg{width:62%;height:70%}.preview-copy{padding:52px;display:flex;flex-direction:column;justify-content:center}.preview-copy h3{font-size:38px;line-height:1.03;letter-spacing:-.04em;margin:14px 0}.preview-copy p{font-size:14px;color:var(--dim);line-height:1.65;margin:0 0 28px}.result{margin-top:18px;border:1px solid var(--line);border-radius:12px;background:#080807;padding:16px;font:12px/1.55 ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--dim);min-height:54px}.result strong{color:var(--btc)}
.machine{display:grid;grid-template-columns:repeat(3,1fr);gap:18px}.endpoint{background:var(--card);border:1px solid var(--line);border-radius:18px;padding:31px}.endpoint svg{width:40px;height:40px;margin-bottom:18px}.endpoint h3{font-size:16px;margin:0 0 7px}.endpoint p{font-size:12.5px;color:var(--dim);margin:0 0 14px}.endpoint code{display:block;font-size:10px;color:var(--faint);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
footer{border-top:1px solid var(--line);padding:40px 0;color:var(--faint);font-size:12px}.foot{display:flex;justify-content:space-between;gap:18px;flex-wrap:wrap}.footlinks{display:flex;gap:22px;flex-wrap:wrap}@media(max-width:900px){.tool-grid{grid-template-columns:repeat(3,1fr)}.rails,.preview{grid-template-columns:1fr}.machine{grid-template-columns:1fr}.stats{grid-template-columns:repeat(2,1fr)}}@media(max-width:620px){nav a:not(.buy){display:none}.tool-grid{grid-template-columns:repeat(2,1fr)}.hero{padding-top:78px}.preview-copy{padding:38px 28px}}
</style></head><body>
<header><div class="wrap"><a class="brand" href="https://loopxxi.com">Loop<span>XXI</span> / loop-mcp</a><nav><a href="https://loopxxi.com/products.html">All products</a><a href="https://github.com/Loop-XXI/loop-mcp">Docs</a><a class="buy" href="https://api.loopxxi.com/ai-credits">Buy credits</a></nav></div></header>
<main><section class="hero"><div class="wrap"><div class="kicker">15 tools live</div><h1>Small jobs should take one call.</h1><p class="lede">One MCP endpoint for Bitcoin, data, text, and developer utilities. Pay only for execution.</p><div class="actions"><a class="button primary" href="https://api.loopxxi.com/ai-credits">Buy prepaid credits</a><a class="button" href="/.well-known/agent-payments.json">Agent manifest</a></div><div class="stats"><div class="stat"><strong>15</strong><span>Tools</span></div><div class="stat"><strong>5–25</strong><span>Sats / call</span></div><div class="stat"><strong>2</strong><span>Payment rails</span></div><div class="stat"><strong>0</strong><span>Subscriptions</span></div></div></div></section>
<section><div class="wrap"><div class="title"><div class="kicker">Tool catalog</div><h2>See the job. Call the tool.</h2><p>Discovery is free. Every result is structured JSON.</p></div><div class="tool-grid">
<div class="tool hot"><div class="tool-icon">BTC</div><code>btc_price</code><span class="tool-price">10 sats</span></div><div class="tool hot"><div class="tool-icon">SEND</div><code>btc_send_decision</code><span class="tool-price">15 sats</span></div><div class="tool hot"><div class="tool-icon">LN</div><code>lightning_address_resolve</code><span class="tool-price">10 sats</span></div><div class="tool hot"><div class="tool-icon">TX</div><code>tx_decode_explain</code><span class="tool-price">25 sats</span></div><div class="tool hot"><div class="tool-icon">FEE</div><code>optimal_send_window</code><span class="tool-price">25 sats</span></div>
<div class="tool"><div class="tool-icon">{ }</div><code>json_validate</code><span class="tool-price">5 sats</span></div><div class="tool"><div class="tool-icon">PATH</div><code>json_extract</code><span class="tool-price">5 sats</span></div><div class="tool"><div class="tool-icon">CSV</div><code>csv_to_json</code><span class="tool-price">10 sats</span></div><div class="tool"><div class="tool-icon">TXT</div><code>text_analyze</code><span class="tool-price">5 sats</span></div><div class="tool"><div class="tool-icon">#</div><code>hash_generate</code><span class="tool-price">5 sats</span></div>
<div class="tool"><div class="tool-icon">64</div><code>base64_convert</code><span class="tool-price">5 sats</span></div><div class="tool"><div class="tool-icon">UTC</div><code>timestamp_convert</code><span class="tool-price">5 sats</span></div><div class="tool"><div class="tool-icon">ID</div><code>uuid_generate</code><span class="tool-price">5 sats</span></div><div class="tool"><div class="tool-icon">URL</div><code>url_parse</code><span class="tool-price">5 sats</span></div><div class="tool"><div class="tool-icon">JWT</div><code>jwt_decode</code><span class="tool-price">5 sats</span></div>
</div></div></section>
<section><div class="wrap"><div class="title"><div class="kicker">Two payment rails</div><h2>Use the rail you already have.</h2><p>The payment credential travels with the request.</p></div><div class="rails">
<div class="rail"><svg viewBox="0 0 74 74" fill="none"><rect x="6" y="16" width="62" height="42" rx="7" stroke="#8f8c86" stroke-width="2.5"/><path d="M6 28H68" stroke="#8f8c86" stroke-width="2.5"/><rect x="14" y="40" width="18" height="5" rx="2.5" fill="#f7931a"/></svg><div class="kicker">Prepaid credits</div><h3>Buy once. Call directly.</h3><p>Fund a $10, $25, or $50 key with Stripe, then send it on every tool call.</p><code>Authorization: Bearer loop_&lt;credit_key&gt;</code><a class="button primary" href="https://api.loopxxi.com/ai-credits">Buy credits</a></div>
<div class="rail"><svg viewBox="0 0 74 74" fill="none"><circle cx="37" cy="37" r="29" stroke="#8f8c86" stroke-width="2.5"/><path d="M41 18L27 40H37L33 56L48 33H38Z" fill="#f7931a" stroke="#f7931a" stroke-width="2" stroke-linejoin="round"/></svg><div class="kicker">Lightning L402</div><h3>Challenge. Pay. Retry.</h3><p>Pay the returned BOLT11 invoice and retry with the token and preimage.</p><code>Authorization: L402 &lt;token&gt;:&lt;preimage&gt;</code><a class="button" href="/.well-known/l402-manifest.json">L402 manifest</a></div>
</div></div></section>
<section><div class="wrap"><div class="preview"><div class="preview-art"><svg viewBox="0 0 300 300" fill="none"><circle cx="150" cy="150" r="118" stroke="#292824" stroke-width="3"/><circle cx="150" cy="150" r="80" stroke="#4a3a21" stroke-width="3"/><path d="M164 76L114 161H147L132 224L189 135H155Z" fill="#f7931a" stroke="#f7931a" stroke-width="3" stroke-linejoin="round"/></svg></div><div class="preview-copy"><div class="kicker">Free preview</div><h3>See a real result.</h3><p>Call the live Bitcoin price tool without a wallet, credit key, or account.</p><button id="try-btn" class="button primary" onclick="tryPrice()">Get BTC price</button><div id="try-result" class="result">Ready.</div></div></div></div></section>
<section><div class="wrap"><div class="title"><div class="kicker">Agent setup</div><h2>Discover first. Pay second.</h2><p>Inspect names, schemas, exact prices, rails, and maximum possible charge.</p></div><div class="machine"><a class="endpoint" href="/.well-known/agent-payments.json"><svg viewBox="0 0 40 40" fill="none"><rect x="4" y="4" width="32" height="32" rx="8" stroke="#8f8c86" stroke-width="2"/><path d="M11 15H29M11 21H29M11 27H22" stroke="#f7931a" stroke-width="2" stroke-linecap="round"/></svg><h3>Payment manifest</h3><p>Names, schemas, prices, rails.</p><code>GET /.well-known/agent-payments.json</code></a><a class="endpoint" href="/mcp"><svg viewBox="0 0 40 40" fill="none"><path d="M7 13L20 5L33 13V27L20 35L7 27Z" stroke="#8f8c86" stroke-width="2"/><circle cx="20" cy="20" r="5" fill="#f7931a"/></svg><h3>MCP discovery</h3><p>Free tool listing and annotations.</p><code>GET /mcp</code></a><a class="endpoint" href="https://github.com/Loop-XXI/loop-mcp"><svg viewBox="0 0 40 40" fill="none"><path d="M11 9L3 20L11 31M29 9L37 20L29 31M24 5L16 35" stroke="#8f8c86" stroke-width="2.3" stroke-linecap="round" stroke-linejoin="round"/></svg><h3>Integration docs</h3><p>Hosted calls and embedded transport.</p><code>github.com/Loop-XXI/loop-mcp</code></a></div></div></section></main>
<footer><div class="wrap foot"><span>Loop XXI LLC</span><div class="footlinks"><a href="https://loopxxi.com/products.html">Products</a><a href="mailto:business@loopxxi.com">Contact</a><a href="https://github.com/Loop-XXI/loop-mcp">GitHub</a></div></div></footer>
<script>async function tryPrice(){const b=document.getElementById('try-btn'),r=document.getElementById('try-result');b.disabled=true;b.textContent='Loading…';r.textContent='Calling live tool…';try{const x=await fetch('/try/btc_price',{method:'POST'}),j=await x.json();if(!x.ok)throw new Error(j.error||'Request failed');const usd=j.usd||j.price_usd||j.price?.usd||j.result?.usd;const stamp=j.timestamp||j.updated_at||j.result?.timestamp||'live';r.innerHTML='<strong>BTC / USD</strong> '+(usd?Number(usd).toLocaleString():'Live result received')+' · '+stamp}catch(e){r.textContent='Preview unavailable: '+e.message}finally{b.disabled=false;b.textContent='Get BTC price'}}</script></body></html>`

// ────────────────────────────────────────────────────────────────────────────
// Main
// ────────────────────────────────────────────────────────────────────────────

// corsMiddleware sets permissive CORS headers so the L402 Playground and
// browser-based agents can read HTTP 402 challenges and paid responses.
// The paid endpoints are already public and stateless; nothing exposed here
// weakens the L402 gate. Wildcard origin is safe because there is no
// cookie-based auth to steal — payment IS the credential.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = "*"
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Vary", "Origin")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Expose-Headers", "WWW-Authenticate")
		c.Header("Access-Control-Max-Age", "600")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func main() {
	cfg := loadConfig()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	// Health check — no auth
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "2.3.0"})
	})

	// MCP server card — Smithery / catalog scanner metadata.
	// Served at both route prefixes so mcp.loopxxi.com and the Railway domain
	// both satisfy scanners without path rewriting.
	r.GET("/.well-known/mcp/server-card.json", serveServerCard)

	// Agent payment manifest — machine-readable for buyer agents.
	r.GET("/.well-known/agent-payments.json", serveAgentPaymentManifest)
	r.GET("/agent-payments.json", serveAgentPaymentManifest)

	// Lightning Enable-compatible L402 discovery manifest.
	r.GET("/.well-known/l402-manifest.json", serveL402Manifest)
	r.GET("/l402-manifest.json", serveL402Manifest)

	// Satring domain verification challenge — no auth.
	// Generated 2026-06-30 for the loop-mcp listing.
	r.GET("/.well-known/satring-verify", func(c *gin.Context) {
		c.String(http.StatusOK, "bf6000d67cc6050662d50b51265736729b00eb0f6a8853d2a8f1e6d1ff7d109e")
	})

	// 402 Index domain verification challenge — public hash only.
	r.GET("/.well-known/402index-verify.txt", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.String(http.StatusOK, "e1de7a05e40bc63e8101b6b7e829098070f70421d4a5f90cb5d67c9d7b741234\n")
	})

	// GET /mcp — free tool discovery for agents
	r.GET("/mcp", func(c *gin.Context) {
		var toolList []gin.H
		for _, t := range tools.All() {
			toolList = append(toolList, gin.H{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
				"annotations": t.Annotations,
				"price_sats":  t.SatsPrice,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"server":   "loop-mcp",
			"version":  "2.3.0",
			"protocol": "MCP 2024-11-05",
			"payment_rails": []gin.H{
				{"name": "L402 (Lightning)", "instructions": "Authorization: L402 <token...>"},
				{"name": "Fiat credit_key (Stripe)", "instructions": "Authorization: Bearer loop_<...ey>. Buy credits at https://api.loopxxi.com/ai-credits"},
			},
			"tools":   toolList,
			"docs":    "https://github.com/Loop-XXI/loop-mcp",
			"contact": "business@loopxxi.com",
		})
	})

	// POST /mcp — L402-gated MCP endpoint
	r.POST("/mcp", l402Middleware(cfg), handleMCP)

	// GET /l402/btc_price — REST-shaped L402 endpoint for directories and
	// simple agents that probe URLs without a JSON-RPC body.
	r.GET("/l402/btc_price", handleRESTL402Tool(cfg, "btc_price"))
	r.GET("/l402/btc_send_decision", handleRESTL402Tool(cfg, "btc_send_decision"))
	r.GET("/l402/optimal_send_window", handleRESTL402Tool(cfg, "optimal_send_window"))

	// GET / — branded public landing page (lead-gen for humans visiting mcp.loopxxi.com)
	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(landingHTML))
	})

	// POST /try/btc_price — free read-only try endpoint (lead-gen; no payment required).
	// Lets a visitor test the tool output without a Lightning wallet or credit key.
	// Only btc_price is exposed here (public mempool.space data, no value gating).
	r.POST("/try/btc_price", func(c *gin.Context) {
		result, err := tools.HandleBtcPrice(json.RawMessage("{}"))
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "price source unavailable"}})
			return
		}
		resultJSON, _ := json.Marshal(result)
		c.JSON(http.StatusOK, gin.H{
			"tool":   "btc_price",
			"free":   true,
			"note":   "In production an agent pays 10 sats (L402) or a fraction of a fiat credit for this call.",
			"result": json.RawMessage(resultJSON),
		})
	})

	addr := ":" + cfg.Port
	log.Printf("loop-mcp v2 (safe build) listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
