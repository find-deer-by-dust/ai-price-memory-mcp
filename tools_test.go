package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectTestMCP(t *testing.T, handler http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	client, err := newPriceMemoClient(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newMCPServer(client).Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := mcpClient.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func callTool[T any](t *testing.T, session *mcp.ClientSession, name, args string) T {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "pricememo_" + name,
		Arguments: json.RawMessage(args),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.StructuredContent == nil {
		t.Fatalf("unexpected MCP result: %+v", result)
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected text content: %+v", result.Content[0])
	}
	var out T
	if err := json.Unmarshal([]byte(content.Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSearchRanksExactStandardProductFirst(t *testing.T) {
	session := connectTestMCP(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/products" || r.URL.Query().Has("q") {
			t.Errorf("unexpected standard product request %s", r.URL)
		}
		_, _ = io.WriteString(w, `{"items":[{"slug":"chatgpt-business","display_name":"ChatGPT Business","subtitle":"Upgrade from ChatGPT Plus"},{"slug":"chatgpt-plus","display_name":"ChatGPT Plus","lowest_price":"17.00"}],"snapshot_id":42,"total":2}`)
	})
	result := callTool[productSearchResult](t, session, "search_products", `{"query":"CHATGPT plus","limit":1}`)
	if len(result.Items) != 1 || result.Items[0].Slug != "chatgpt-plus" || result.NextOffset == nil || *result.NextOffset != 1 {
		t.Fatalf("unexpected search result %+v", result)
	}
	if got := result.Items[0].TrustedLowPrice; got == nil || *got != price("17.00") {
		t.Fatalf("search changed original price: %v", got)
	}
}

func TestCompareUsesTheSameSnapshotAndCurrencyForMerchants(t *testing.T) {
	session := connectTestMCP(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path == "/api/v1/products" {
			_, _ = io.WriteString(w, `{"items":[{"slug":"chatgpt-pro","display_name":"ChatGPT Pro"},{"slug":"chatgpt-plus","display_name":"ChatGPT Plus"}],"snapshot_id":42,"total":2}`)
			return
		}
		if q.Get("snapshot") != "42" || q.Get("comparable") != "true" || q.Get("in_stock") != "true" || q.Get("q") != "充值 & 续费" {
			t.Errorf("unexpected filters %s", r.URL)
		}
		switch r.URL.Path {
		case "/api/v1/products/chatgpt-plus":
			_, _ = io.WriteString(w, `{"slug":"chatgpt-plus","price_currency":"CNY","snapshot_id":42,"offer_group_count":2,"official_reference":{"price":"20.00","currency":"USD"},"offer_groups":[{"fingerprint":"same-item","price_currency":"CNY","offer_count":4,"shop_count":3,"representative":{"id":10,"currency":"CNY"}}]}`)
		case "/api/v1/products/chatgpt-plus/groups/same-item":
			if q.Get("currency") != "CNY" {
				t.Errorf("unexpected group scope %s", r.URL)
			}
			_, _ = io.WriteString(w, `{"items":[{"id":10,"shop_token":"shop-a","price":"12.00","currency":"CNY","stock_status":"in_stock"},{"id":11,"shop_token":"shop-a","price":"2.00","currency":"CNY","stock_status":"in_stock"},{"id":12,"shop_token":"shop-b","price":"8.00","currency":"CNY","stock_status":"in_stock"},{"id":13,"shop_token":"shop-c","price":"1.00","currency":"USD","stock_status":"in_stock"}]}`)
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	result := callTool[comparisonResult](t, session, "compare_offers", `{"product":"ChatGPT Plus","q":"充值 & 续费","limit":1}`)
	if result.ID == nil || *result.ID != 42 || result.Product.OfficialReference == nil || result.Product.OfficialReference.Currency != "USD" ||
		result.NextOffset == nil || *result.NextOffset != 1 || result.Expanded == nil ||
		result.Expanded.MerchantCount != 2 || len(result.Expanded.Merchants) != 1 ||
		result.Expanded.Merchants[0].ID != 11 {
		t.Fatalf("unexpected comparison result %+v", result)
	}
}

func TestOfferDetailsJoinsDescriptionAndBoundsReturnedText(t *testing.T) {
	description := strings.Repeat("报价", 900)
	session := connectTestMCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/offers/42":
			_, _ = io.WriteString(w, `{"id":42,"description_available":true,"original_description":"","price":"9.00","currency":"CNY"}`)
		case "/api/v1/offers/42/description":
			_, _ = io.WriteString(w, `{"offer_id":42,"original_description":"`+description+`"}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	result := callTool[offerDetails](t, session, "get_offer_details", `{"offer_id":42}`)
	if result.Quote.ID != 42 || result.DescriptionStatus != "available" ||
		!result.DescriptionTruncated || result.MerchantDescription != strings.Repeat("报价", 800) {
		t.Fatalf("unexpected offer detail status %q or description length", result.DescriptionStatus)
	}
}

func TestHistorySummarizesAllBucketsWithinEachCurrency(t *testing.T) {
	session := connectTestMCP(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/products/chatgpt-plus/history" || r.URL.Query().Get("source_platform") != "16688" {
			t.Errorf("unexpected history request %s", r.URL)
		}
		_, _ = io.WriteString(w, `{"trend":[{"bucket_at":"2026-10-02T00:00:00Z","price_currency":"CNY","trusted_lowest_price":"12.00"},{"bucket_at":"2026-10-01T00:00:00Z","price_currency":"CNY","trusted_lowest_price":"2.00"},{"bucket_at":"2026-10-01T00:00:00Z","price_currency":"USD","trusted_lowest_price":"1.00"}]}`)
	})
	result := callTool[priceHistory](t, session, "get_price_history", `{"slug":"chatgpt-plus","source_platform":"16688","recent_points":1}`)
	if len(result.Currencies) != 2 || result.Currencies[0].Currency != "CNY" {
		t.Fatalf("history currencies were not separated: %+v", result)
	}
	cny := result.Currencies[0]
	if cny.BucketCount != 2 || len(cny.RecentPoints) != 1 || cny.RecentPoints[0].BucketAt.Day() != 2 ||
		cny.LowestTrustedPoint == nil || *cny.LowestTrustedPoint.TrustedLowestPrice != "2.00" {
		t.Fatalf("unexpected history summary %+v", cny)
	}
}

func TestPriceMemoClientRejectsInvalidPathsAndReportsUpstreamErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/products/missing" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"detail":"product not found"}`)
	}))
	defer upstream.Close()

	client, err := newPriceMemoClient(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.get(t.Context(), nil, "products", "../meta"); err == nil {
		t.Fatal("expected invalid path to fail")
	}
	if _, err := client.get(t.Context(), nil, "products", "missing"); err == nil ||
		!strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "product not found") {
		t.Fatalf("unexpected upstream error: %v", err)
	}
}
