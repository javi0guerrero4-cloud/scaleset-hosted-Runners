package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// viewCoin is the presentation-ready shape sent to the browser, so the client
// only has to move strings into the DOM.
type viewCoin struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Symbol    string `json:"symbol"`
	Image     string `json:"image"`
	Initial   string `json:"initial"`
	Rank      int    `json:"rank"`
	Price     string `json:"price"`
	Change    string `json:"change"`
	MarketCap string `json:"market_cap"`
	Volume    string `json:"volume"`
	Range     string `json:"range"`
	Up        bool   `json:"up"`
	SparkUp   bool   `json:"spark_up"`
	Points    string `json:"points"`
}

func sortByMarketCap(coins []marketCoin) []marketCoin {
	sorted := make([]marketCoin, len(coins))
	copy(sorted, coins)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].MarketCap > sorted[j].MarketCap
	})
	return sorted
}

func buildViews(coins []marketCoin) []viewCoin {
	sorted := sortByMarketCap(coins)

	views := make([]viewCoin, 0, len(sorted))
	for i, c := range sorted {
		change := 0.0
		if c.Change24h != nil {
			change = *c.Change24h
		}

		series := c.Sparkline.Price
		sparkUp := len(series) > 1 && series[len(series)-1] >= series[0]

		views = append(views, viewCoin{
			ID:        c.ID,
			Name:      c.Name,
			Symbol:    strings.ToUpper(c.Symbol),
			Image:     c.Image,
			Initial:   initialOf(c.Name),
			Rank:      i + 1,
			Price:     formatPrice(c.Price),
			Change:    formatPercent(change),
			MarketCap: formatCompact(c.MarketCap),
			Volume:    formatCompact(c.Volume),
			Range:     formatPrice(c.Low24h) + " – " + formatPrice(c.High24h),
			Up:        change >= 0,
			SparkUp:   sparkUp,
			Points:    sparkPoints(series),
		})
	}
	return views
}

func initialOf(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func formatPrice(v float64) string {
	switch {
	case v >= 1000:
		return "$" + groupThousands(strconv.FormatFloat(v, 'f', 2, 64))
	case v >= 1:
		return "$" + strconv.FormatFloat(v, 'f', 2, 64)
	case v >= 0.01:
		return "$" + strconv.FormatFloat(v, 'f', 4, 64)
	default:
		return "$" + strconv.FormatFloat(v, 'f', 6, 64)
	}
}

func formatCompact(v float64) string {
	units := []struct {
		div    float64
		suffix string
	}{
		{1e12, "T"},
		{1e9, "B"},
		{1e6, "M"},
		{1e3, "K"},
	}
	for _, u := range units {
		if v >= u.div {
			return "$" + strconv.FormatFloat(v/u.div, 'f', 2, 64) + u.suffix
		}
	}
	return "$" + strconv.FormatFloat(v, 'f', 2, 64)
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%+.2f%%", v)
}

func groupThousands(s string) string {
	whole, frac, hasFrac := strings.Cut(s, ".")

	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// sparkPoints normalises a price series into polyline coordinates for a
// 100x32 viewBox.
func sparkPoints(series []float64) string {
	if len(series) < 2 {
		return ""
	}

	const width, height, pad = 100.0, 32.0, 3.0
	const maxPoints = 48

	sample := series
	if len(series) > maxPoints {
		step := float64(len(series)-1) / float64(maxPoints-1)
		sample = make([]float64, maxPoints)
		for i := range sample {
			sample[i] = series[int(float64(i)*step+0.5)]
		}
	}

	min, max := sample[0], sample[0]
	for _, v := range sample[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	if span == 0 {
		span = 1
	}

	var b strings.Builder
	for i, v := range sample {
		x := pad + (width-2*pad)*float64(i)/float64(len(sample)-1)
		y := pad + (height-2*pad)*(1-(v-min)/span)
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", x, y)
	}
	return b.String()
}
