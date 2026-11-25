package types

import "testing"

// Each typed string accepts exactly its documented forms. The cases are the
// valid and invalid examples spec/language/data/typed_strings.heron lists.
func TestTypedStrings(t *testing.T) {
	cases := map[string]struct{ valid, invalid []string }{
		"types.Duration": {
			valid:   []string{"30d", "20m", "200ms", "1h30m", "1.5h", "2w", "0s"},
			invalid: []string{"thirty days", "30", "30 d", "-5m", "30D", "1h 30m", "P30D", ""},
		},
		"types.Money": {
			valid:   []string{"40.00 EUR", "40 EUR", "-1.5 USD", "0.001 BTC"},
			invalid: []string{"40 euros", "EUR 40", "40.00EUR", "40.00 eur", "1,000.00 EUR", "+40 EUR", ".5 EUR"},
		},
		"types.Timestamp": {
			valid:   []string{"2026-03-01", "2026-03-01T09:30:00", "2026-03-01T09:30:00Z", "2026-03-01T09:30:00.250+02:00"},
			invalid: []string{"yesterday", "2026-3-1", "2026-02-30", "01.03.2026", "2026-03-01 09:30:00", "2026-03-01T09:30"},
		},
	}
	for typ, c := range cases {
		parse := TypedStrings[typ]
		if parse == nil {
			t.Fatalf("no parser for %s", typ)
		}
		for _, s := range c.valid {
			if err := parse(s); err != nil {
				t.Errorf("%s: %q should parse: %v", typ, s, err)
			}
		}
		for _, s := range c.invalid {
			if err := parse(s); err == nil {
				t.Errorf("%s: %q should not parse", typ, s)
			}
		}
	}
}
