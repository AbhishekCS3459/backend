package productcatalog

import "testing"

func TestNormalizeText(t *testing.T) {
	cases := map[string]string{
		"Maggi 2-Minute Noodles":        "maggi 2 minute noodles",
		"maggi 2 minute noodles":        "maggi 2 minute noodles",
		"MAGGI  2-MINUTE   NOODLES ":    "maggi 2 minute noodles",
		"1000+ Sticker Book For Kids":   "1000+ sticker book for kids",
		"Children's Books":              "children's books",
		"Lay’s India’s Magic Masala":    "lay's india's magic masala",
		"Stationery & Games":            "stationery & games",
		"Tata Salt (1 kg) - Iodised!":   "tata salt 1 kg iodised",
		"Dr. Oetker FunFoods":           "dr oetker funfoods",
		"Café Coffee Day":               "cafe coffee day",
		"Amul 70% Dark, 1,000 g / 1.5L": "amul 70% dark 1000 g 1.5l",
		"'Quoted' Kids' Toys":           "quoted kids toys",
		"  ":                            "",
	}
	for in, want := range cases {
		if got := NormalizeText(in); got != want {
			t.Errorf("NormalizeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSearchFields(t *testing.T) {
	got := BuildSearchFields(SearchInput{
		Name:  "1000+ Sticker Book For Kids",
		Brand: "Maple Press",
		Categories: []Category{
			{Name: "Children's Books", Collection: "Stationery & Games", Group: "Household Essentials"},
		},
	})
	want := SearchFields{
		Name:       "1000+ sticker book for kids",
		Brand:      "maple press",
		Category:   "children's books",
		Collection: "stationery & games",
		Group:      "household essentials",
		Text:       "1000+ sticker book for kids maple press children's books stationery & games household essentials",
	}
	if got != want {
		t.Fatalf("BuildSearchFields =\n%+v\nwant\n%+v", got, want)
	}

	multi := BuildSearchFields(SearchInput{Name: "Lay's Chips", Categories: []Category{
		{Name: "Chips & Crisps", Collection: "Chips & Namkeen", Group: "Snacks & Drinks"},
		{Name: "Party Snacks", Collection: "Chips & Namkeen", Group: "Snacks & Drinks"},
	}})
	if multi.Category != "chips & crisps" || multi.Collection != "chips & namkeen" || multi.Group != "snacks & drinks" {
		t.Fatalf("primary category = %+v", multi)
	}
	if multi.Text != "lay's chips chips & crisps chips & namkeen snacks & drinks party snacks" {
		t.Fatalf("text should cover every category once, got %q", multi.Text)
	}

	if empty := BuildSearchFields(SearchInput{Name: "Salt"}); empty != (SearchFields{Name: "salt", Text: "salt"}) {
		t.Fatalf("no categories = %+v", empty)
	}
}
