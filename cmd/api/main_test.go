package main

import "testing"

func TestParseAllowedOrigins(t *testing.T) {
	for value, want := range map[string]string{
		"":    defaultAllowedOrigins,
		"   ": defaultAllowedOrigins,
		"https://app.example.com, http://localhost:4200": "https://app.example.com,http://localhost:4200",
	} {
		if got, err := parseAllowedOrigins(value); err != nil || got != want {
			t.Errorf("parseAllowedOrigins(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"*", ",", "localhost:3000", "http://localhost:3000/", "ftp://example.com", "https://example.com?x=1", "https://user@example.com"} {
		if _, err := parseAllowedOrigins(value); err == nil {
			t.Errorf("parseAllowedOrigins(%q) accepted an invalid origin", value)
		}
	}
}
