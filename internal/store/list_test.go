package store

import (
	"testing"
)

func TestLikePatternEscapes(t *testing.T) {
	got := likePattern(`a%b_c\d`)
	want := `%a\%b\_c\\d%`
	if got != want {
		t.Errorf("likePattern = %q, want %q", got, want)
	}
}

func TestSortExpression(t *testing.T) {
	allowed := map[string]string{"path": "unaccent(path)", "size": "size_bytes"}
	if got := sortExpression("size", allowed, "path"); got != "size_bytes" {
		t.Errorf("got %q", got)
	}
	if got := sortExpression("bogus", allowed, "path"); got != "unaccent(path)" {
		t.Errorf("fallback got %q", got)
	}
}

func TestOrderDirection(t *testing.T) {
	if got := orderDirection("desc"); got != "DESC" {
		t.Errorf("desc = %q", got)
	}
	if got := orderDirection("DESC"); got != "DESC" {
		t.Errorf("DESC = %q", got)
	}
	if got := orderDirection(""); got != "ASC" {
		t.Errorf("empty = %q, want ASC", got)
	}
	if got := orderDirection("bogus"); got != "ASC" {
		t.Errorf("bogus = %q, want ASC", got)
	}
}

func TestNormalizeSearch(t *testing.T) {
	if got := normalizeSearch("Beyoncé"); got != "beyonce" {
		t.Errorf("got %q, want beyonce", got)
	}
	if got := normalizeSearch("ABC"); got != "abc" {
		t.Errorf("got %q", got)
	}
}
