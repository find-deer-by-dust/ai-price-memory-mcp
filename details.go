package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type offerDetails struct {
	Quote                merchantQuote `json:"quote"`
	MerchantDescription  string        `json:"merchant_description,omitempty"`
	DescriptionStatus    string        `json:"description_status" jsonschema:"available, not_provided, or unavailable"`
	DescriptionTruncated bool          `json:"description_truncated,omitempty"`
}

func (c *priceMemoClient) getOfferDetails(ctx context.Context, a offerArgs) (offerDetails, error) {
	var result offerDetails
	if a.OfferID < 1 {
		return result, fmt.Errorf("offer_id must be positive")
	}
	id := strconv.FormatInt(a.OfferID, 10)
	type offerResponse struct {
		merchantQuote
		Description          string `json:"original_description"`
		DescriptionAvailable bool   `json:"description_available"`
	}
	offer, err := getJSON[offerResponse](ctx, c, nil, "offers", id)
	if err != nil {
		return result, err
	}
	if offer.ID != a.OfferID {
		return result, fmt.Errorf("PriceMemo returned an unexpected offer ID")
	}
	result = offerDetails{Quote: offer.merchantQuote, DescriptionStatus: "not_provided"}
	description := strings.TrimSpace(offer.Description)
	if description == "" && offer.DescriptionAvailable {
		type descriptionResponse struct {
			ID   int64  `json:"offer_id"`
			Text string `json:"original_description"`
		}
		detail, err := getJSON[descriptionResponse](ctx, c, nil, "offers", id, "description")
		if err != nil || detail.ID != a.OfferID {
			result.DescriptionStatus = "unavailable"
			return result, nil
		}
		description = strings.TrimSpace(detail.Text)
	}
	if description != "" {
		result.DescriptionStatus = "available"
		result.MerchantDescription, result.DescriptionTruncated = truncateText(description, 1600)
	}
	return result, nil
}

func truncateText(text string, limit int) (string, bool) {
	count := 0
	for index := range text {
		if count == limit {
			return text[:index], true
		}
		count++
	}
	return text, false
}

type historyPoint struct {
	BucketAt           time.Time `json:"bucket_at"`
	Currency           string    `json:"price_currency"`
	TrustedLowestPrice *price    `json:"trusted_lowest_price,omitempty"`
	MedianPrice        *price    `json:"median_price,omitempty"`
	InStockCount       int       `json:"in_stock_count"`
	ObservationCount   int       `json:"observation_count"`
}

type currencyHistory struct {
	Currency           string         `json:"currency"`
	BucketCount        int            `json:"bucket_count"`
	FirstBucketAt      time.Time      `json:"first_bucket_at"`
	LatestBucketAt     time.Time      `json:"latest_bucket_at"`
	LowestTrustedPoint *historyPoint  `json:"lowest_trusted_point,omitempty"`
	RecentPoints       []historyPoint `json:"recent_points"`
}

type priceHistory struct {
	ProductSlug    string            `json:"product_slug"`
	ProductURL     string            `json:"product_url"`
	SourcePlatform string            `json:"source_platform,omitempty"`
	Currencies     []currencyHistory `json:"currencies"`
}

func (c *priceMemoClient) getPriceHistory(ctx context.Context, a historyArgs) (priceHistory, error) {
	var result priceHistory
	limit, err := countOrDefault(a.RecentPoints, 7, 91, "recent_points")
	if err != nil {
		return result, err
	}
	query := make(url.Values)
	setString(query, "source_platform", a.SourcePlatform)
	type trendResponse struct {
		Points []historyPoint `json:"trend"`
	}
	data, err := getJSON[trendResponse](ctx, c, query, "products", a.Slug, "history")
	if err != nil {
		return result, err
	}
	result = priceHistory{
		ProductSlug: a.Slug, ProductURL: c.baseURL.JoinPath("products", a.Slug).String(),
		SourcePlatform: a.SourcePlatform, Currencies: []currencyHistory{},
	}
	byCurrency := make(map[string][]historyPoint)
	for _, point := range data.Points {
		byCurrency[point.Currency] = append(byCurrency[point.Currency], point)
	}
	for currency, points := range byCurrency {
		slices.SortStableFunc(points, func(a, b historyPoint) int { return a.BucketAt.Compare(b.BucketAt) })
		summary := currencyHistory{
			Currency: currency, BucketCount: len(points), FirstBucketAt: points[0].BucketAt,
			LatestBucketAt: points[len(points)-1].BucketAt, RecentPoints: points[max(0, len(points)-limit):],
		}
		for i := range points {
			point := &points[i]
			if point.TrustedLowestPrice != nil &&
				(summary.LowestTrustedPoint == nil || comparePrice(point.TrustedLowestPrice, summary.LowestTrustedPoint.TrustedLowestPrice) < 0) {
				summary.LowestTrustedPoint = point
			}
		}
		result.Currencies = append(result.Currencies, summary)
	}
	slices.SortFunc(result.Currencies, func(a, b currencyHistory) int { return strings.Compare(a.Currency, b.Currency) })
	return result, nil
}
