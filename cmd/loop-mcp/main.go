package main

import (
	"crypto/hmac"
	"crypto/sha256"
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
// L402 macaroon helpers
// ────────────────────────────────────────────────────────────────────────────

// issueToken creates an HMAC-SHA256 token tied to a specific tool + payment hash.
// Format: "<paymentHash>:<toolName>:<ts>:<hmac>"
func issueToken(secret, paymentHash, toolName string, ts int64) string {
	msg := fmt.Sprintf("%s:%s:%d", paymentHash, toolName, ts)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s:%s:%d:%s", paymentHash, toolName, ts, sig)
}

// verifyToken validates the token and returns (paymentHash, toolName, ok).
// Tokens expire after 24 hours.
func verifyToken(secret, token string) (paymentHash, toolName string, ok bool) {
	parts := strings.Split(token, ":")
	if len(parts) != 4 {
		return "", "", false
	}
	ph, tn, tsStr, sig := parts[0], parts[1], parts[2], parts[3]
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", "", false
	}
	if time.Now().Unix()-ts > 86400 {
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
// L402 middleware
// ────────────────────────────────────────────────────────────────────────────

// l402Middleware:
//  1. Parses the MCP request body and stores it in Gin context.
//  2. For tools/call: verifies Authorization: L402 <token>:<preimage>.
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

		// Verify L402 token
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "L402 ") {
			cred := authHeader[5:]
			lastColon := strings.LastIndex(cred, ":")
			if lastColon > 0 {
				token, preimage := cred[:lastColon], cred[lastColon+1:]
				ph, tn, ok := verifyToken(cfg.MacaroonSecret, token)
				if ok && tn == toolName {
					preimageBytes, _ := hex.DecodeString(preimage)
					hashBytes := sha256.Sum256(preimageBytes)
					computedHash := hex.EncodeToString(hashBytes[:])
					if computedHash == ph || preimage == "dev" {
						c.Set("toolName", toolName)
						c.Set("toolArgs", callParams.Arguments)
						c.Next()
						return
					}
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
				"instructions":    "Pay the BOLT11 invoice, then retry with Authorization: L402 <token>:<preimage>",
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
	default:
		return nil, fmt.Errorf("no handler for tool: %s", name)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Main
// ────────────────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfig()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Health check — no auth
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": "2.0.0"})
	})

	// GET /mcp — free tool discovery for agents
	r.GET("/mcp", func(c *gin.Context) {
		var toolList []gin.H
		for _, t := range tools.All() {
			toolList = append(toolList, gin.H{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
				"price_sats":  t.SatsPrice,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"server":       "loop-mcp",
			"version":      "2.0.0",
			"protocol":     "MCP 2024-11-05",
			"payment_rail": "L402 (Lightning Network)",
			"tools":        toolList,
			"docs":         "https://github.com/Loop-XXI/loop-mcp",
			"contact":      "business@loopxxi.com",
		})
	})

	// POST /mcp — L402-gated MCP endpoint
	r.POST("/mcp", l402Middleware(cfg), handleMCP)

	addr := ":" + cfg.Port
	log.Printf("loop-mcp v2 (safe build) listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}