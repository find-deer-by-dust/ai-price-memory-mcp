package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Keep prices as decimal strings so the amounts survive JSON encoding exactly.
type price string

func (p *price) UnmarshalJSON(data []byte) error {
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	if _, ok := new(big.Rat).SetString(string(number)); !ok {
		return fmt.Errorf("invalid upstream price %q", number)
	}
	*p = price(number)
	return nil
}

func comparePrice(a, b *price) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	first, _ := new(big.Rat).SetString(string(*a))
	second, _ := new(big.Rat).SetString(string(*b))
	return first.Cmp(second)
}

type officialPrice struct {
	Provider      string `json:"provider"`
	Plan          string `json:"plan"`
	Price         *price `json:"price,omitempty"`
	Currency      string `json:"currency"`
	BillingPeriod string `json:"billing_period"`
	SourceURL     string `json:"url"`
	CheckedAt     string `json:"checked_at"`
	Note          string `json:"note,omitempty"`
}

type productSummary struct {
	Slug              string         `json:"slug"`
	Name              string         `json:"display_name"`
	Subtitle          string         `json:"subtitle,omitempty"`
	Brand             string         `json:"brand"`
	ProductType       string         `json:"product_type"`
	MarketCurrency    string         `json:"price_currency"`
	TrustedLowPrice   *price         `json:"lowest_price,omitempty" jsonschema:"Upstream low price for trusted comparable in-stock observations in price_currency"`
	MedianPrice       *price         `json:"median_price,omitempty"`
	OfferCount        int            `json:"offer_count"`
	InStockCount      int            `json:"in_stock_count"`
	LastUpdatedAt     string         `json:"last_updated_at,omitempty"`
	OfficialReference *officialPrice `json:"official_reference,omitempty" jsonschema:"Official reference in its original billing period and currency"`
	ProductURL        string         `json:"product_url,omitempty"`
}

type snapshot struct {
	ID *int64 `json:"snapshot_id,omitempty"`
	At string `json:"snapshot_at,omitempty"`
}

type productSearchResult struct {
	snapshot
	Items      []productSummary `json:"items"`
	Total      int              `json:"total"`
	NextOffset *int             `json:"next_offset,omitempty"`
}

func (c *priceMemoClient) searchProducts(ctx context.Context, a searchArgs) (productSearchResult, error) {
	var result productSearchResult
	limit, err := a.pageArgs.limit()
	if err != nil {
		return result, err
	}
	query := make(url.Values)
	setString(query, "brand", a.Brand)
	setOptional(query, "snapshot", a.Snapshot)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(a.Offset))

	words := searchWords(a.Query)
	if words == "" && strings.TrimSpace(a.Query) != "" {
		return result, fmt.Errorf("query must contain a letter or number")
	}
	if words != "" {
		// Search standard plan names, independently of merchant listing titles.
		query.Set("limit", "100")
		query.Set("offset", "0")
	}
	result, err = getJSON[productSearchResult](ctx, c, query, "products")
	if err != nil {
		return result, err
	}
	if a.Snapshot != nil && !sameSnapshot(a.Snapshot, result.ID) {
		return result, fmt.Errorf("PriceMemo returned an unexpected product snapshot")
	}
	if words != "" {
		setOptional(query, "snapshot", result.ID)
		for len(result.Items) < result.Total {
			pageOffset := len(result.Items)
			query.Set("offset", strconv.Itoa(pageOffset))
			page, err := getJSON[productSearchResult](ctx, c, query, "products")
			if err != nil {
				return result, err
			}
			if len(page.Items) == 0 || (result.ID != nil && !sameSnapshot(result.ID, page.ID)) {
				return result, fmt.Errorf("PriceMemo product catalog changed during search")
			}
			result.Items = append(result.Items, page.Items...)
		}
		result.Items = matchProducts(result.Items, words)
		result.Total = len(result.Items)
		start := min(a.Offset, result.Total)
		result.Items = result.Items[start:min(start+limit, result.Total)]
	}
	for i := range result.Items {
		product := &result.Items[i]
		product.ProductURL = c.baseURL.JoinPath("products", product.Slug).String()
	}
	if result.Items == nil {
		result.Items = []productSummary{}
	}
	result.NextOffset = nextOffset(a.Offset, len(result.Items), result.Total)
	return result, nil
}

func searchWords(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

func matchProducts(products []productSummary, query string) []productSummary {
	compact := strings.ReplaceAll(query, " ", "")
	terms := strings.Fields(query)
	var exact, nameMatch, subtitleMatch []productSummary
	for _, product := range products {
		name := searchWords(product.Name)
		slug := strings.ReplaceAll(searchWords(product.Slug), " ", "")
		identity := name + " " + slug + " " + searchWords(product.Brand)
		switch {
		case exactProductMatch(product, compact):
			exact = append(exact, product)
		case containsTerms(identity, terms):
			nameMatch = append(nameMatch, product)
		case containsTerms(identity+" "+searchWords(product.Subtitle), terms):
			subtitleMatch = append(subtitleMatch, product)
		}
	}
	return append(append(exact, nameMatch...), subtitleMatch...)
}

func exactProductMatch(product productSummary, compactQuery string) bool {
	return strings.ReplaceAll(searchWords(product.Name), " ", "") == compactQuery ||
		strings.ReplaceAll(searchWords(product.Slug), " ", "") == compactQuery
}

func containsTerms(value string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(value, term) {
			return false
		}
	}
	return true
}

func nextOffset(offset, count, total int) *int {
	next := offset + count
	if count > 0 && next < total {
		return &next
	}
	return nil
}

func sameSnapshot(want, actual *int64) bool {
	return want != nil && actual != nil && *want == *actual
}

type merchantQuote struct {
	ID             int64    `json:"id"`
	ProductSlug    string   `json:"product_slug,omitempty"`
	Title          string   `json:"original_name"`
	ShopToken      string   `json:"shop_token,omitempty"`
	ShopName       string   `json:"shop_name"`
	SourcePlatform string   `json:"source_platform"`
	Price          *price   `json:"price,omitempty"`
	Currency       string   `json:"currency"`
	StockStatus    string   `json:"stock_status"`
	StockCount     *int     `json:"stock_count,omitempty"`
	DeliveryType   string   `json:"delivery_type"`
	AutoDelivery   *bool    `json:"auto_delivery,omitempty"`
	ServicePeriod  string   `json:"service_period"`
	Warranty       string   `json:"warranty"`
	Comparable     bool     `json:"is_comparable"`
	TrustedPrice   bool     `json:"is_trusted_price"`
	RiskFlags      []string `json:"risk_flags,omitempty"`
	LowPriceNote   string   `json:"low_price_warning,omitempty"`
	SourceURL      string   `json:"source_url"`
	ObservedAt     string   `json:"observed_at"`
}

type offerGroup struct {
	Fingerprint    string        `json:"fingerprint"`
	Currency       string        `json:"price_currency"`
	LowestPrice    *price        `json:"lowest_price,omitempty"`
	HighestPrice   *price        `json:"highest_price,omitempty"`
	OfferCount     int           `json:"offer_count"`
	ShopCount      int           `json:"shop_count"`
	InStockCount   int           `json:"in_stock_count"`
	Representative merchantQuote `json:"representative"`
}

type productDetail struct {
	productSummary
	snapshot
	GroupCount int          `json:"offer_group_count"`
	Groups     []offerGroup `json:"offer_groups"`
}

type comparisonResult struct {
	snapshot
	Product       productSummary `json:"product"`
	Groups        []offerGroup   `json:"groups"`
	TotalGroups   int            `json:"total_groups"`
	NextOffset    *int           `json:"next_offset,omitempty"`
	Expanded      *sellerGroup   `json:"expanded_group,omitempty"`
	ExpansionNote string         `json:"expansion_note,omitempty"`
}

type sellerGroup struct {
	Fingerprint   string          `json:"fingerprint"`
	Currency      string          `json:"currency"`
	MerchantCount int             `json:"merchant_count"`
	Merchants     []merchantQuote `json:"merchants"`
}

func (c *priceMemoClient) compareOffers(ctx context.Context, a compareArgs) (comparisonResult, error) {
	var result comparisonResult
	limit, err := a.pageArgs.limit()
	if err != nil {
		return result, err
	}
	currency := strings.ToUpper(strings.TrimSpace(a.Currency))
	if (a.Fingerprint == "") != (currency == "") {
		return result, fmt.Errorf("fingerprint and currency are required together")
	}
	if a.Fingerprint != "" && a.Offset != 0 {
		return result, fmt.Errorf("offset is only available when listing groups")
	}
	if (strings.TrimSpace(a.Product) == "") == (a.Slug == "") {
		return result, fmt.Errorf("provide either product or slug")
	}
	if a.Slug == "" {
		a.Slug, a.Snapshot, err = c.resolveProduct(ctx, a.Product, a.Snapshot)
		if err != nil {
			return result, err
		}
	}
	query, err := a.query()
	if err != nil {
		return result, err
	}
	detail, err := getJSON[productDetail](ctx, c, query, "products", a.Slug)
	if err != nil {
		return result, err
	}
	if detail.Slug == "" || (a.Snapshot != nil && !sameSnapshot(a.Snapshot, detail.ID)) {
		return result, fmt.Errorf("PriceMemo returned an unexpected product or snapshot")
	}
	setOptional(query, "snapshot", detail.ID)
	detail.ProductURL = c.baseURL.JoinPath("products", detail.Slug).String()
	result = comparisonResult{
		snapshot: detail.snapshot, Product: detail.productSummary,
		Groups: []offerGroup{}, TotalGroups: detail.GroupCount,
	}
	if a.Fingerprint != "" {
		result.Expanded, err = c.groupMerchants(ctx, query, detail.Slug, a.Fingerprint, currency, limit)
		return result, err
	}
	if a.Offset >= detail.GroupCount {
		return result, nil
	}
	pageSize := min(limit, detail.GroupCount-a.Offset)
	if a.Offset == 0 && len(detail.Groups) >= pageSize {
		result.Groups = detail.Groups[:pageSize]
	} else {
		query.Set("limit", strconv.Itoa(limit))
		query.Set("offset", strconv.Itoa(a.Offset))
		type groupPage struct {
			Items []offerGroup `json:"items"`
			Total int          `json:"total"`
			ID    *int64       `json:"snapshot_id"`
		}
		page, err := getJSON[groupPage](ctx, c, query, "products", detail.Slug, "groups")
		if err != nil {
			return result, err
		}
		if detail.ID != nil && !sameSnapshot(detail.ID, page.ID) {
			return result, fmt.Errorf("PriceMemo quote groups changed snapshots during comparison")
		}
		if len(page.Items) > 0 {
			result.Groups = page.Items[:min(limit, len(page.Items))]
		}
		result.TotalGroups = page.Total
		query.Del("offset")
		query.Del("limit")
	}
	result.NextOffset = nextOffset(a.Offset, len(result.Groups), result.TotalGroups)
	if len(result.Groups) == 0 {
		return result, nil
	}
	first := result.Groups[0]
	if first.OfferCount == 1 && first.Representative.ID > 0 {
		result.Expanded = &sellerGroup{first.Fingerprint, first.Currency, 1, []merchantQuote{first.Representative}}
		return result, nil
	}
	if detail.ID == nil {
		result.ExpansionNote = "No snapshot ID was available for expanding the preview."
		return result, nil
	}
	result.Expanded, err = c.groupMerchants(ctx, query, detail.Slug, first.Fingerprint, first.Currency, limit)
	if err != nil {
		result.ExpansionNote = fmt.Sprintf("Could not expand the first group: %v", err)
	}
	return result, nil
}

func (c *priceMemoClient) resolveProduct(ctx context.Context, name string, snapshotID *int64) (string, *int64, error) {
	result, err := c.searchProducts(ctx, searchArgs{Query: name, pageArgs: pageArgs{Snapshot: snapshotID}})
	if err != nil {
		return "", nil, err
	}
	if len(result.Items) == 0 {
		return "", nil, fmt.Errorf("no standard product matched %q", name)
	}
	compact := strings.ReplaceAll(searchWords(name), " ", "")
	first := result.Items[0]
	if len(result.Items) == 1 || (exactProductMatch(first, compact) && !exactProductMatch(result.Items[1], compact)) {
		return first.Slug, result.ID, nil
	}
	slugs := make([]string, 0, len(result.Items))
	for _, product := range result.Items {
		slugs = append(slugs, product.Slug)
	}
	return "", nil, fmt.Errorf("multiple products matched %q. Choose a slug from %s", name, strings.Join(slugs, ", "))
}

func (c *priceMemoClient) groupMerchants(ctx context.Context, query url.Values, slug, fingerprint, currency string, limit int) (*sellerGroup, error) {
	type offers struct {
		Items []merchantQuote `json:"items"`
	}
	query.Set("currency", currency)
	data, err := getJSON[offers](ctx, c, query, "products", slug, "groups", fingerprint)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(data.Items, func(a, b merchantQuote) int {
		if a.StockStatus == b.StockStatus || (a.StockStatus != "in_stock" && b.StockStatus != "in_stock") {
			return comparePrice(a.Price, b.Price)
		}
		if a.StockStatus == "in_stock" {
			return -1
		}
		return 1
	})
	result := &sellerGroup{Fingerprint: fingerprint, Currency: currency, Merchants: make([]merchantQuote, 0, limit)}
	seen := make(map[string]bool)
	for _, offer := range data.Items {
		if !strings.EqualFold(offer.Currency, currency) || (offer.ShopToken != "" && seen[offer.ShopToken]) {
			continue
		}
		seen[offer.ShopToken] = true
		result.MerchantCount++
		if len(result.Merchants) < limit {
			result.Merchants = append(result.Merchants, offer)
		}
	}
	return result, nil
}
