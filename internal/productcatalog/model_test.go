package productcatalog

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestKeywords(t *testing.T) {
	got := keywords("  Dark CHOCOLATE, chocolate & a 70% bar!")
	want := []string{"dark", "chocolate", "70", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keywords = %v, want %v", got, want)
	}
	if got := keywords("a b c"); len(got) != 0 {
		t.Fatalf("single letters should be dropped, got %v", got)
	}
	if got := keywords("Lay’s Haldiram's"); !reflect.DeepEqual(got, []string{"lays", "haldirams"}) {
		t.Fatalf("apostrophes should be dropped, got %v", got)
	}
}

func TestWordPattern(t *testing.T) {
	cases := []struct {
		word, text string
		want       bool
	}{
		{"lays", "Lay's Classic Salted Chips", true},
		{"lays", "Lay’s Magic Masala", true},
		{"lay", "Swiss Beauty Cover Play Concealer", false},
		{"lay", "Himalaya Neem Soap", false},
		{"milk", "Amul Buttermilk", false},
		{"milk", "Cadbury Dairy Milk", true},
		{"choc", "Dark Chocolate", true},
		{"7up", "7UP Nimbooz", true},
		{"c", "C++ Primer", true},
	}
	for _, c := range cases {
		got := regexp.MustCompile("(?i)" + wordPattern(c.word)).MatchString(c.text)
		if got != c.want {
			t.Errorf("wordPattern(%q) on %q = %v, want %v", c.word, c.text, got, c.want)
		}
	}
	if got := wordPattern("a.b"); got != `\ba['’]?\.['’]?b` {
		t.Errorf("metacharacters must be escaped, got %q", got)
	}
}

func TestIsProductID(t *testing.T) {
	cases := map[string]bool{
		"776963": true,
		"12":     false,
		"12a456": false,
		"":       false,
	}
	for q, want := range cases {
		if got := isProductID(q); got != want {
			t.Errorf("isProductID(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestDecodeScrapedProduct(t *testing.T) {
	raw, err := bson.Marshal(bson.M{
		"_id":             "blinkit:bengaluru:koramangala:776963",
		"platform":        "blinkit",
		"city":            "bengaluru",
		"locality":        "Koramangala",
		"address":         "Koramangala, Bengaluru, Karnataka, India",
		"scrapedAt":       time.Date(2026, 9, 26, 19, 9, 1, 0, time.UTC),
		"productId":       "776963",
		"name":            "1000+ Sticker Book For Kids",
		"brand":           "Maple Press",
		"unit":            "1 pc",
		"price":           int32(159),
		"mrp":             int32(230),
		"discountPercent": int32(31),
		"inStock":         true,
		"inventory":       int32(2),
		"imageUrl":        "https://cdn.grofers.com/x.png",
		"merchantId":      "31719",
		"groupId":         int32(2823390),
		"url":             "https://blinkit.com/prn/x/prid/776963",
		"categories": bson.A{bson.M{
			"id": int32(273), "name": "Children's Books", "collection": "Stationery & Games", "group": "Household Essentials",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var p Product
	if err := bson.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p.finish()

	if p.ID != "blinkit:bengaluru:koramangala:776963" || p.SKU != "TDZ-776963" || p.ProductID != "776963" {
		t.Fatalf("unexpected ids: %+v", p)
	}
	if p.Price != 159 || p.MRP != 230 || p.DiscountPercent != 31 || !p.InStock {
		t.Fatalf("unexpected pricing: %+v", p)
	}
	want := []Category{{ID: 273, Name: "Children's Books", Collection: "Stationery & Games", Group: "Household Essentials"}}
	if p.Category != "Children's Books" || !reflect.DeepEqual(p.Categories, want) {
		t.Fatalf("unexpected categories: %q %+v", p.Category, p.Categories)
	}
}

func TestFinishWithoutCategoriesOrProductID(t *testing.T) {
	p := Product{ID: "blinkit:bengaluru:hebbal:4242"}
	p.finish()
	if p.Categories == nil || p.Category != "" || p.SKU != "TDZ-4242" {
		t.Fatalf("unexpected product: %+v", p)
	}
}

func TestSKUProductID(t *testing.T) {
	cases := map[string]string{"TDZ-776963": "776963", "tdz-128940": "128940", "TDZ-1": "1"}
	for sku, want := range cases {
		if got, ok := skuProductID(sku); !ok || got != want {
			t.Errorf("skuProductID(%q) = %q, %v", sku, got, ok)
		}
	}
	for _, bad := range []string{"TDZ-", "TDZ-abc", "blinkit-776963", "776963", "TD", "TDZ-" + strings.Repeat("9", 21)} {
		if _, ok := skuProductID(bad); ok {
			t.Errorf("skuProductID(%q) should not match", bad)
		}
	}
}

func TestCatalogKey(t *testing.T) {
	if got := CatalogKey("776963"); got != "todayz:776963" {
		t.Errorf("CatalogKey = %q, want todayz:776963", got)
	}
	if id, ok := CatalogKeyProductID("todayz:776963"); !ok || id != "776963" {
		t.Errorf("CatalogKeyProductID(todayz:776963) = %q, %v", id, ok)
	}
	for _, bad := range []string{"blinkit:776963", "todayz:", "todayz:abc", "variant:776963", "776963",
		"todayz:" + strings.Repeat("9", 21)} {
		if _, ok := CatalogKeyProductID(bad); ok {
			t.Errorf("CatalogKeyProductID(%q) should not match", bad)
		}
	}
}

func TestNormalizeFilters(t *testing.T) {
	got, err := normalizeFilters(Filters{Group: " Snacks & Drinks ", Brand: " Lay's "})
	if err != nil {
		t.Fatal(err)
	}
	want := Filters{Group: "Snacks & Drinks", Brand: "Lay's"}
	if got != want {
		t.Fatalf("normalizeFilters = %+v, want %+v", got, want)
	}
	if _, err := normalizeFilters(Filters{Category: strings.Repeat("a", maxFilterValue+1)}); err != ErrInvalidFilter {
		t.Fatalf("overlong filter err = %v, want ErrInvalidFilter", err)
	}
	for _, bad := range []Filters{{MinPrice: -1}, {MaxPrice: -1}, {MinPrice: 50, MaxPrice: 10}} {
		if _, err := normalizeFilters(bad); err != ErrInvalidFilter {
			t.Errorf("normalizeFilters(%+v) err = %v, want ErrInvalidFilter", bad, err)
		}
	}
	if _, err := normalizeFilters(Filters{MinPrice: 50}); err != nil {
		t.Fatalf("min price without max: %v", err)
	}
}

func TestMatchFilterStockAndPrice(t *testing.T) {
	got := matchFilter("", Filters{InStock: true, MinPrice: 20, MaxPrice: 80})
	want := bson.D{
		{Key: "inStock", Value: true},
		{Key: "price", Value: bson.D{{Key: "$gte", Value: 20.0}, {Key: "$lte", Value: 80.0}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matchFilter = %v, want %v", got, want)
	}
}

func TestMatchFilterKeepsCategoryLevelsTogether(t *testing.T) {
	got := matchFilter("", Filters{Brand: "Lay's", Group: "Snacks & Drinks", Category: "Chips & Crisps"})
	want := bson.D{
		{Key: "brand", Value: "Lay's"},
		{Key: "categories", Value: bson.D{{Key: "$elemMatch", Value: bson.D{
			{Key: "group", Value: "Snacks & Drinks"},
			{Key: "name", Value: "Chips & Crisps"},
		}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matchFilter = %v, want %v", got, want)
	}
}

func TestSearchFilter(t *testing.T) {
	if got := searchFilter("  "); got != nil {
		t.Fatalf("empty query should not filter, got %v", got)
	}

	re := func(word string) bson.D {
		r := bson.Regex{Pattern: wordPattern(word), Options: "i"}
		return bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "name", Value: r}}, bson.D{{Key: "brand", Value: r}}}}}
	}
	if got, want := searchFilter("Milk"), re("milk"); !reflect.DeepEqual(got, want) {
		t.Fatalf("single word = %v, want %v", got, want)
	}
	if got, want := searchFilter("dark choc"), (bson.D{{Key: "$and", Value: bson.A{re("dark"), re("choc")}}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("two words = %v, want %v", got, want)
	}
	want := bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "productId", Value: "776963"}}, re("776963")}}}
	if got := searchFilter("776963"); !reflect.DeepEqual(got, want) {
		t.Fatalf("product id = %v, want %v", got, want)
	}
	if got, want := searchFilter(" tdz-776963 "), (bson.D{{Key: "productId", Value: "776963"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("sku = %v, want %v", got, want)
	}
	// Regex metacharacters are split out as separators, so they never reach the pattern.
	if got, want := searchFilter("7up (500"), (bson.D{{Key: "$and", Value: bson.A{re("7up"), re("500")}}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("punctuation = %v, want %v", got, want)
	}
}

func TestFacetOptionsSorts(t *testing.T) {
	got := facetOptions([]valueCount{{Value: "Parle", N: 2}, {Value: "amul", N: 9}, {Value: nil, N: 4}, {Value: " ", N: 1}})
	want := []FilterOption{{Value: "amul", Label: "amul", Count: 9}, {Value: "Parle", Label: "Parle", Count: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("facetOptions = %+v, want %+v", got, want)
	}
}
