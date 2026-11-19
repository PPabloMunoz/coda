package store

import (
	"database/sql/driver"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction(
		"unaccent",
		1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) == 0 || args[0] == nil {
				return nil, nil
			}
			value, ok := args[0].(string)
			if !ok {
				return args[0], nil
			}
			return normalizeSearch(value), nil
		},
	)
}

// normalizeSearch strips diacritics and lowercases text for SQLite searches.
func normalizeSearch(value string) string {
	decomposed := norm.NFD.String(value)
	var result strings.Builder
	result.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}
