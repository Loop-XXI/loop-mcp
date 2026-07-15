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
		"version":        "2.2.0",
		"description":    "L402-native MCP server where AI agents pay per Bitcoin/Lightning tool call in sats or fiat credits.",
		"license":        "MIT",
		"provider": gin.H{
			"name":    "Loop XXI LLC",
			"email":   "business@loopxxi.com",
			"url":     "https://loopxxi.com",
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
			"version":     "2.2.0",
			"description": "L402-native MCP server where AI agents pay per Bitcoin/Lightning tool call in sats or fiat credits.",
		},
		"endpoints": []gin.H{
			{
				"type":                 "mcp",
				"url":                  "https://mcp.loopxxi.com/mcp",
				"transport":            "streamable-http",
				"protocol_version":     "2024-11-05",
				"authentication":       "L402",
				"auth_model":           "payment-required",
				"payment_requirement": "per-request",
			},
			{
				"type":            "rest",
				"url":             "https://mcp.loopxxi.com/l402/btc_price",
				"transport":       "https",
				"authentication":  "L402",
				"auth_model":      "payment-required",
				"tool":            "btc_price",
				"description":     "Read-only REST probe endpoint for btc_price. Returns 402 with Lightning invoice if unpaid.",
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
			"name": "loop-mcp Bitcoin Agent Tools",
			"description": "Read-only Bitcoin and Lightning decision tools for autonomous agents, paid per call over L402.",
			"base_url": "https://mcp.loopxxi.com",
			"logo_url": nil,
			"contact_email": "business@loopxxi.com",
			"documentation_url": "https://github.com/Loop-XXI/loop-mcp",
			"terms_of_service_url": "https://loopxxi.com/terms",
			"categories": []string{"bitcoin", "finance", "agent-tools", "data"},
		},
		"l402": gin.H{
			"default_price_sats": 10,
			"payment_flow": "402_challenge_response",
			"auth_header_format": "Authorization: L402 <macaroon>:<preimage>",
			"capabilities": gin.H{
				"preimage_in_response": true,
				"supported_currencies": []string{"BTC"},
			},
		},
		"endpoints": []gin.H{
			{
				"id": "get-btc-price",
				"path": "/l402/btc_price",
				"method": "GET",
				"summary": "Current Bitcoin price",
				"description": "Current Bitcoin price in USD and major fiat currencies, returned as structured JSON.",
				"pricing": gin.H{"model": "perrequest", "base_price_sats": 10},
				"l402_enabled": true,
			},
			{
				"id": "get-btc-send-decision",
				"path": "/l402/btc_send_decision",
				"method": "GET",
				"summary": "Bitcoin send-or-wait decision",
				"description": "Live SEND_NOW, WAIT, or URGENT_ONLY recommendation using mempool pressure and fee savings.",
				"pricing": gin.H{"model": "perrequest", "base_price_sats": 15},
				"l402_enabled": true,
			},
			{
				"id": "get-optimal-send-window",
				"path": "/l402/optimal_send_window",
				"method": "GET",
				"summary": "Optimal Bitcoin send window",
				"description": "Congestion forecast and recommended UTC send window with confidence and RBF viability.",
				"pricing": gin.H{"model": "perrequest", "base_price_sats": 25},
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
// Tokens expire after 24 hours.
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
	if age < 0 || age > 86400 {
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

		// Verify L402 token
		if strings.HasPrefix(authHeader, "L402 ") {
			cred := authHeader[5:]
			lastColon := strings.LastIndex(cred, ":")
			if lastColon > 0 {
				token, preimage := cred[:lastColon], cred[lastColon+1:]
				ph, tn, ok := verifyToken(cfg.MacaroonSecret, token)
				if ok && tn == toolName && verifyPaymentPreimage(ph, preimage) {
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
				"version": "2.0.0",
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
				if ok && tn == toolName && verifyPaymentPreimage(ph, preimage) {
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
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>loop-mcp — Paid MCP tools for AI agents</title>
<meta name="description" content="An L402-native MCP server where AI agents pay per tool call in sats over Lightning or fiat credits via Stripe. Five live Bitcoin and Lightning tools.">
<meta property="og:title" content="loop-mcp">
<meta property="og:description" content="Paid MCP tools for autonomous AI agents. Pay per call in sats or fiat credits.">
<meta property="og:type" content="website">
<meta property="og:url" content="https://mcp.loopxxi.com">
<meta name="theme-color" content="#0a0a0a">
<link rel="preconnect" href="https://rsms.me/">
<link rel="stylesheet" href="https://rsms.me/inter/inter.css">
<link rel="icon" type="image/png" href="https://loopxxi.com/LoopXXI-Logo.png">
<style>
  :root { --bg:#0a0a0a; --ink:#e8e6df; --muted:#8a877e; --dim:#595550; --line:#1f1e1b; --surface:#141412; --green:#22c55e; --btc:#f7931a; --ln:#a78bfa; }
  *,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--ink);font-family:"Inter",-apple-system,BlinkMacSystemFont,sans-serif;font-feature-settings:"ss01","cv11";font-size:17px;line-height:1.65;-webkit-font-smoothing:antialiased;-moz-osx-font-smoothing:grayscale}
  ::selection{background:var(--ink);color:var(--bg)}
  a{color:var(--ink);text-decoration:none;border-bottom:1px solid var(--dim);transition:border-color .2s}
  a:hover{border-color:var(--ink)}
  .wrap{max-width:880px;margin:0 auto;padding:0 32px}
  header{padding:28px 0;border-bottom:1px solid var(--line)}
  header .wrap{display:flex;justify-content:space-between;align-items:center}
  .brand{font-weight:600;font-size:17px;letter-spacing:-0.02em;border:none}
  .brand span{color:var(--muted);font-weight:400}
  .nav-links{display:flex;gap:24px;list-style:none}
  .nav-links a{font-size:14px;color:var(--muted);border:none}
  .nav-links a:hover{color:var(--ink)}
  .hero{padding:80px 0 56px}
  .pill{display:inline-flex;align-items:center;gap:6px;padding:5px 14px;border:1px solid rgba(34,197,94,.3);border-radius:100px;font-size:12px;font-weight:500;letter-spacing:0.04em;text-transform:uppercase;color:var(--green);background:rgba(34,197,94,.06);margin-bottom:28px}
  .pill .dot{width:6px;height:6px;border-radius:50%;background:var(--green);animation:blink 2s ease-in-out infinite}
  @keyframes blink{0%,100%{opacity:1}50%{opacity:.35}}
  h1{font-weight:500;font-size:clamp(36px,5vw,52px);line-height:1.08;letter-spacing:-0.03em;text-wrap:balance}
  .hero p{margin-top:24px;font-size:18px;color:var(--muted);max-width:56ch;text-wrap:pretty}
  .rails{display:flex;gap:12px;margin-top:32px;flex-wrap:wrap}
  .rail{padding:10px 16px;border:1px solid var(--line);border-radius:10px;font-size:13px;color:var(--muted);background:var(--surface)}
  .rail strong{color:var(--ink);font-weight:500}
  section{padding:48px 0;border-top:1px solid var(--line)}
  h2{font-size:11px;font-weight:600;letter-spacing:0.18em;text-transform:uppercase;color:var(--muted);margin-bottom:28px}
  .tools{display:grid;grid-template-columns:1fr 1fr;gap:16px}
  .tool{padding:24px;border:1px solid var(--line);border-radius:12px;background:var(--surface);transition:border-color .25s}
  .tool:hover{border-color:var(--dim)}
  .tool-head{display:flex;justify-content:space-between;align-items:baseline;margin-bottom:10px}
  .tool-name{font-weight:500;font-size:16px;letter-spacing:-0.01em}
  .tool-price{font-size:12px;font-weight:600;color:var(--green);letter-spacing:0.04em}
  .tool-desc{font-size:14px;color:var(--muted);line-height:1.6}
  .try{margin-top:36px;padding:28px;border:1px solid var(--line);border-radius:12px;background:var(--surface)}
  .try h3{font-weight:500;font-size:17px;margin-bottom:8px;letter-spacing:-0.01em}
  .try p{font-size:14px;color:var(--muted);margin-bottom:16px}
  .try-btn{display:inline-flex;align-items:center;gap:8px;padding:10px 22px;border-radius:100px;background:var(--ink);color:var(--bg);font-size:14px;font-weight:600;border:none;cursor:pointer;transition:opacity .2s}
  .try-btn:hover{opacity:.9}
  .try-btn:disabled{opacity:.5;cursor:wait}
  #result{margin-top:20px;padding:16px;background:var(--bg);border:1px solid var(--line);border-radius:8px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px;color:var(--ink);white-space:pre-wrap;word-break:break-all;display:none}
  #result.show{display:block}
  code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px;background:var(--surface);padding:1px 6px;border-radius:4px;border:1px solid var(--line)}
  .code-block{margin-top:16px;padding:16px;background:var(--bg);border:1px solid var(--line);border-radius:8px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;color:var(--muted);overflow-x:auto;white-space:pre;line-height:1.6}
  .code-block .k{color:var(--ln)}
  .code-block .s{color:var(--green)}
  footer{padding:40px 0;border-top:1px solid var(--line)}
  footer .wrap{display:flex;justify-content:space-between;align-items:center;gap:16px;flex-wrap:wrap}
  footer p{font-size:13px;color:var(--muted)}
  footer a{color:var(--muted);border:none}
  footer a:hover{color:var(--ink)}
  @media (max-width:640px){.tools{grid-template-columns:1fr}.wrap{padding:0 20px}.hero{padding:48px 0 32px}}
</style>
</head>
<body>
<header><div class="wrap"><a href="https://loopxxi.com" class="brand">loop-mcp <span>by LoopXXI</span></a><ul class="nav-links"><li><a href="https://github.com/Loop-XXI/loop-mcp">GitHub</a></li><li><a href="https://loopxxi.com">LoopXXI</a></li></ul></div></header>
<div class="wrap"><div class="hero"><div class="pill"><span class="dot"></span>Live · v2.2.0</div><h1>Paid tools for autonomous AI agents.</h1><p>An L402-native MCP server where agents pay per tool call — 10 to 25 sats over Lightning, or fiat-funded credits via Stripe. Payment is the credential: no API keys, no accounts. The first MCP server on the official Registry that charges agents directly.</p><div class="rails"><div class="rail"><strong>Lightning (L402)</strong> — 10-25 sats/call</div><div class="rail"><strong>Stripe credits</strong> — <a href="https://api.loopxxi.com/ai-credits">buy a key</a></div></div></div>
<section><h2>Live tools</h2><div class="tools"><div class="tool"><div class="tool-head"><span class="tool-name">btc_price</span><span class="tool-price">10 sats</span></div><div class="tool-desc">Current Bitcoin price in USD and major fiat currencies. Source: mempool.space.</div></div><div class="tool"><div class="tool-head"><span class="tool-name">btc_send_decision</span><span class="tool-price">15 sats</span></div><div class="tool-desc">Send-or-wait verdict with fee rates, mempool pressure, and estimated savings. One call replaces parsing multiple mempool endpoints.</div></div><div class="tool"><div class="tool-head"><span class="tool-name">lightning_address_resolve</span><span class="tool-price">10 sats</span></div><div class="tool-desc">Resolve a Lightning Address to a payable BOLT11 invoice. Full LNURL-pay protocol in one call.</div></div><div class="tool"><div class="tool-head"><span class="tool-name">tx_decode_explain</span><span class="tool-price">25 sats</span></div><div class="tool-desc">Fetch a Bitcoin transaction by txid and get a structured agent summary — type, fee, flags, confirmation status. Saves 500-2,000 LLM tokens.</div></div><div class="tool"><div class="tool-head"><span class="tool-name">optimal_send_window</span><span class="tool-price">25 sats</span></div><div class="tool-desc">Congestion forecast + recommended send window with a calibrated confidence score and RBF viability. A timing decision no free endpoint provides.</div></div></div><div class="try"><h3>Try it free — no wallet required.</h3><p>Fetch the live Bitcoin price. This read-only call is free on this page; in production, an agent pays 10 sats or a fraction of a fiat credit.</p><button class="try-btn" id="tryBtn" onclick="fetchPrice()"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M5 12h14M12 5l7 7-7 7"/></svg> Get BTC price</button><div id="result"></div></div></section>
<section><h2>How agents pay</h2><p style="color:var(--muted);font-size:15px;max-width:60ch">An agent calls a tool with no auth. The server returns <code>402 Payment Required</code> with a Lightning invoice (L402) or points to a fiat credit key. The agent pays, then retries with the proof of payment — and gets the result.</p><div class="code-block"><span class="k"># 1. Call a tool → get a 402 + Lightning invoice</span>\ncurl -X POST https://mcp.loopxxi.com/mcp \\\n  -H <span class="s">"Content-Type: application/json"</span> \\\n  -d <span class="s">'{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"btc_price","arguments":{}}}'</span>\n\n<span class="k"># 2. Pay the BOLT11 invoice, then retry with the L402 token + preimage</span>\ncurl -X POST https://mcp.loopxxi.com/mcp \\\n  -H <span class="s">"Content-Type: application/json"</span> \\\n  -H <span class="s">"Authorization: L402 &lt;to...pan> \\\n  -d <span class="s">'{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"btc_price","arguments":{}}}'</span>\n\n<span class="k"># Fiat path: buy a credit key, then use it as a Bearer token</span>\ncurl -X POST https://mcp.loopxxi.com/mcp \\\n  -H <span class="s">"Authorization: Bearer loop_&...pan> \\\n  -H <span class="s">"Content-Type: application/json"</span> \\\n  -d <span class="s">'{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"btc_price","arguments":{}}}'</span></div><p style="color:var(--dim);font-size:13px;margin-top:14px">Buy fiat credits at <a href="https://api.loopxxi.com/ai-credits">api.loopxxi.com/ai-credits</a>. Full docs in the <a href="https://github.com/Loop-XXI/loop-mcp">GitHub repo</a>.</p></section></div>
<footer><div class="wrap"><p>© <span id="y"></span> Loop XXI LLC</p><p><a href="mailto:business@loopxxi.com">business@loopxxi.com</a> · <a href="https://github.com/Loop-XXI/loop-mcp">GitHub</a> · <a href="https://loopxxi.com">LoopXXI</a></p></div></footer>
<script>document.getElementById('y').textContent=new Date().getFullYear();async function fetchPrice(){const btn=document.getElementById('tryBtn');const res=document.getElementById('result');btn.disabled=true;btn.textContent='Fetching...';res.className='show';res.textContent='Calling btc_price via the free MCP endpoint...';try{const r=await fetch('/try/btc_price',{method:'POST'});const j=await r.json();res.textContent=JSON.stringify(j,null,2)}catch(e){res.textContent='Error: '+e.message}btn.disabled=false;btn.innerHTML='<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M5 12h14M12 5l7 7-7 7"/></svg> Get BTC price'}</script>
</body>
</html>`

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
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "2.2.0"})
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
			"server":  "loop-mcp",
			"version": "2.2.0",
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
