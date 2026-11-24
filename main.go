package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	transport := flag.String("transport", "stdio", "MCP transport (stdio or http)")
	listen := flag.String("listen", "127.0.0.1:8080", "address for Streamable HTTP")
	flag.Parse()

	upstream := os.Getenv("PRICEMEMO_BASE_URL")
	if upstream == "" {
		upstream = defaultBaseURL
	}
	client, err := newPriceMemoClient(upstream)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := newMCPServer(client)

	switch *transport {
	case "stdio":
		err = server.Run(ctx, &mcp.StdioTransport{})
	case "http":
		err = runHTTP(ctx, server, *listen)
	default:
		err = fmt.Errorf("unknown transport %q (use stdio or http)", *transport)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func runHTTP(ctx context.Context, server *mcp.Server, address string) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:             true,
		JSONResponse:          true,
		CrossOriginProtection: http.NewCrossOriginProtection(),
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	log.Printf("MCP listening on %s at /mcp", listener.Addr())

	done := make(chan error, 1)
	go func() {
		done <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	case err := <-done:
		return err
	}
}
