package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pageArgs struct {
	Offset   int    `json:"offset,omitempty" jsonschema:"Use next_offset from the previous result"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"Number of products or groups and maximum merchants to return (default 5, maximum 20)"`
	Snapshot *int64 `json:"snapshot,omitempty" jsonschema:"Use snapshot_id from the previous result to keep all pages on the same snapshot"`
}

func (p pageArgs) limit() (int, error) {
	if p.Offset < 0 || p.Offset > 10000 {
		return 0, fmt.Errorf("offset must be between 0 and 10000")
	}
	return countOrDefault(p.Limit, 5, 20, "limit")
}

type searchArgs struct {
	pageArgs
	Query string `json:"query,omitempty" jsonschema:"Standard product name, plan, or slug such as ChatGPT Plus"`
	Brand string `json:"brand,omitempty" jsonschema:"PriceMemo brand such as OpenAI"`
}

type compareArgs struct {
	pageArgs
	Slug               string   `json:"slug,omitempty" jsonschema:"Canonical slug. Provide either slug or product"`
	Product            string   `json:"product,omitempty" jsonschema:"Standard product name such as ChatGPT Plus. Provide either product or slug"`
	Query              string   `json:"q,omitempty" jsonschema:"Search merchant quote titles or terms for this product"`
	SourcePlatform     string   `json:"source_platform,omitempty" jsonschema:"Merchant listing source such as 16688"`
	DeliveryType       string   `json:"delivery_type,omitempty" jsonschema:"Quote delivery type"`
	Period             string   `json:"period,omitempty" jsonschema:"Service period filter"`
	Warranty           string   `json:"warranty,omitempty" jsonschema:"Warranty filter"`
	AutoDelivery       *bool    `json:"auto_delivery,omitempty" jsonschema:"Filter by automatic delivery"`
	UpdatedWithinHours *int     `json:"updated_within_hours,omitempty" jsonschema:"Restrict to quotes observed within this many hours (1–168)"`
	Exclude            string   `json:"exclude,omitempty" jsonschema:"Exclude merchant quote keywords"`
	InStock            *bool    `json:"in_stock,omitempty" jsonschema:"Use in-stock offers only (default true). False includes other stock statuses"`
	OfferKind          string   `json:"offer_kind,omitempty" jsonschema:"Use comparable (default), related, or all"`
	MinPrice           *float64 `json:"min_price,omitempty" jsonschema:"Minimum observed price (nonnegative)"`
	MaxPrice           *float64 `json:"max_price,omitempty" jsonschema:"Maximum observed price (nonnegative)"`
	Fingerprint        string   `json:"fingerprint,omitempty" jsonschema:"Expand this specific group's merchants. Pass its currency as well"`
	Currency           string   `json:"currency,omitempty" jsonschema:"Currency of the fingerprint to expand, such as CNY"`
}

func (a compareArgs) query() (url.Values, error) {
	inStock := true
	if a.InStock != nil {
		inStock = *a.InStock
	}
	query := url.Values{"in_stock": {strconv.FormatBool(inStock)}}
	switch a.OfferKind {
	case "", "comparable":
		query.Set("comparable", "true")
	case "related":
		query.Set("comparable", "false")
	case "all":
	default:
		return nil, fmt.Errorf("offer_kind must be comparable, related, or all")
	}
	setString(query, "q", a.Query)
	setString(query, "source_platform", a.SourcePlatform)
	setString(query, "delivery_type", a.DeliveryType)
	setString(query, "period", a.Period)
	setString(query, "warranty", a.Warranty)
	setString(query, "exclude", a.Exclude)
	setOptional(query, "auto_delivery", a.AutoDelivery)
	setOptional(query, "updated_within_hours", a.UpdatedWithinHours)
	setOptional(query, "min_price", a.MinPrice)
	setOptional(query, "max_price", a.MaxPrice)
	setOptional(query, "snapshot", a.Snapshot)
	return query, nil
}

type offerArgs struct {
	OfferID int64 `json:"offer_id" jsonschema:"Positive offer ID from pricememo_compare_offers"`
}

type historyArgs struct {
	Slug           string `json:"slug" jsonschema:"Standard product slug returned by pricememo_search_products"`
	SourcePlatform string `json:"source_platform,omitempty" jsonschema:"Merchant listing source such as 16688"`
	RecentPoints   *int   `json:"recent_points,omitempty" jsonschema:"Number of recent daily points per currency (default 7, maximum 91)"`
}

func setString(query url.Values, key, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

func setOptional[T bool | int | int64 | float64](query url.Values, key string, value *T) {
	if value != nil {
		query.Set(key, fmt.Sprint(*value))
	}
}

func countOrDefault(value *int, fallback, maximum int, name string) (int, error) {
	if value == nil {
		return fallback, nil
	}
	if *value < 1 || *value > maximum {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return *value, nil
}

func addWorkflowTool[In, Out any](server *mcp.Server, name, description string, run func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "pricememo_" + name,
		Description: description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		out, err := run(ctx, input)
		return nil, out, err
	})
}

func newMCPServer(client *priceMemoClient) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ai-price-memory-mcp",
		Version: "0.2.0",
	}, &mcp.ServerOptions{
		Instructions: "Use pricememo_compare_offers with a product name or slug for current quotes and official price references. Use pricememo_search_products to discover plans. Compare prices within the same currency and check terms, observation time and source links. Follow next_offset with the same filters and snapshot_id.",
	})
	addWorkflowTool(server, "search_products", "Find standard AI products by name or slug. Exact matches come first, with compact price and stock summaries.", client.searchProducts)
	addWorkflowTool(server, "compare_offers", "Compare a product's grouped merchant quotes with its official price reference. By default shows comparable in-stock quotes and expands one group into merchant options in the same currency.", client.compareOffers)
	addWorkflowTool(server, "get_offer_details", "Get one merchant quote, its source, terms and the merchant's description when available.", client.getOfferDetails)
	addWorkflowTool(server, "get_price_history", "Summarize a product's published 90-day historical data by currency, with the lowest trusted observation and recent daily points.", client.getPriceHistory)
	return server
}
