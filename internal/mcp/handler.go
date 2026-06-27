package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler serves MCP JSON-RPC 2.0 over HTTP.
type Handler struct {
	httpClient *http.Client
}

func NewHandler() *Handler {
	return &Handler{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type toolInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

var tools = []toolInfo{
	{
		Name:        "btc_price",
		Description: "Get the current Bitcoin price in USD and 15+ fiat currencies. Source: mempool.space. Updated in real-time.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	},
}

// Discover handles GET /mcp — returns tool list (no auth required).
func (h *Handler) Discover(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": tools})
}

// Call handles POST /mcp — dispatches JSON-RPC 2.0 tool calls.
func (h *Handler) Call(c *gin.Context) {
	var req jsonRPCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"jsonrpc": "2.0", "error": gin.H{"code": -32700, "message": "parse error"}, "id": nil})
		return
	}

	switch req.Method {
	case "tools/list":
		c.JSON(http.StatusOK, gin.H{
			"jsonrpc": "2.0",
			"result":  gin.H{"tools": tools},
			"id":      req.ID,
		})

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.JSON(http.StatusOK, jsonRPCError(req.ID, -32602, "invalid params"))
			return
		}

		switch params.Name {
		case "btc_price":
			result, err := h.btcPriceTool()
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"jsonrpc": "2.0",
					"result":  gin.H{"content": []map[string]string{{"type": "text", "text": fmt.Sprintf("Error: %v", err)}}, "isError": true},
					"id":      req.ID,
				})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"jsonrpc": "2.0",
				"result":  gin.H{"content": result, "isError": false},
				"id":      req.ID,
			})

		default:
			c.JSON(http.StatusOK, jsonRPCError(req.ID, -32601, fmt.Sprintf("unknown tool: %s", params.Name)))
		}

	default:
		c.JSON(http.StatusOK, jsonRPCError(req.ID, -32601, fmt.Sprintf("unknown method: %s", req.Method)))
	}
}

func (h *Handler) btcPriceTool() ([]map[string]string, error) {
	resp, err := h.httpClient.Get("https://mempool.space/api/v1/prices")
	if err != nil {
		return nil, fmt.Errorf("mempool.space unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	var prices map[string]interface{}
	if err := json.Unmarshal(body, &prices); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("Bitcoin Price (source: mempool.space)\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	for k, v := range prices {
		sb.WriteString(fmt.Sprintf("%-6s %v\n", k+":", v))
	}

	return []map[string]string{
		{"type": "text", "text": sb.String()},
		{"type": "text", "text": fmt.Sprintf("Raw JSON:\n%s", string(body))},
	}, nil
}

func jsonRPCError(id interface{}, code int, msg string) gin.H {
	return gin.H{
		"jsonrpc": "2.0",
		"error":   gin.H{"code": code, "message": msg},
		"id":      id,
	}
}
