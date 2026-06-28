package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/Loop-XXI/loop-mcp/internal/l402"
	"github.com/Loop-XXI/loop-mcp/internal/mcp"
	"github.com/Loop-XXI/loop-mcp/internal/phoenixd"
)

func main() {
	phoenixdURL := getEnv("PHOENIXD_URL", "http://localhost:9740")
	phoenixdPassword := getEnv("PHOENIXD_PASSWORD", "")
	priceSats, _ := strconv.ParseInt(getEnv("MCP_PRICE_SATS", "10"), 10, 64)
	ginMode := getEnv("GIN_MODE", "release")
	port := getEnv("PORT", "8080")

	if ginMode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	pdClient := phoenixd.NewClient(phoenixdURL, phoenixdPassword)
	l402Gate := l402.NewMiddleware(pdClient, priceSats)
	mcpHandler := mcp.NewHandler()

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"service": "loop-mcp", "status": "ok", "version": "0.1.0"})
	})

	// Domain verification (Satring, 402index, etc.)
	r.GET("/.well-known/satring-verify", func(c *gin.Context) {
		code := os.Getenv("SATRING_VERIFY_CODE")
		if code == "" {
			code = "5aab6f01770fa6fa9e09736ff0be6035661c8c6218d6f824644d59c3a2342d7e"
		}
		c.String(200, code)
	})

	// MCP discovery (no auth)
	r.GET("/mcp", mcpHandler.Discover)

	// MCP tool call (L402 gated)
	r.POST("/mcp", l402Gate.Gate(), mcpHandler.Call)

	addr := fmt.Sprintf(":%s", port)
	log.Printf("loop-mcp listening on %s | price=%d sats | phoenixd=%s", addr, priceSats, phoenixdURL)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
