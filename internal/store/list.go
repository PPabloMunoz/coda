package store

import "strings"

type PageOptions struct {
	Limit  int
	Offset int
}

type SortOptions struct {
	Sort  string
	Order string
}

type SearchOptions struct {
	Query string
}

type ArtistListOptions struct {
	SearchOptions
	SortOptions
	PageOptions
	IncludeTrackStats bool
	// All includes collaboration-only artists (in_library = 0).
	// Default (false) returns only user-added library artists.
	All bool
}

type ReleaseGroupListOptions struct {
	SearchOptions
	SortOptions
	PageOptions
	ArtistID       string
	IncludeRelease bool
	Type           string
	HasRelease     *bool
	Primary        *bool
}

type FileListOptions struct {
	SearchOptions
	SortOptions
	PageOptions
	Linked       *bool
	IncludeTrack bool
	Format       string
}

// likePattern escapes LIKE wildcards and wraps value for unaccent substring match.
func likePattern(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return "%" + value + "%"
}

// sortExpression maps a sort key to its SQL expression, falling back to fallback.
func sortExpression(value string, allowed map[string]string, fallback string) string {
	if expression, ok := allowed[value]; ok {
		return expression
	}
	return allowed[fallback]
}

// orderDirection maps sort order to ASC/DESC, defaulting to ASC.
func orderDirection(value string) string {
	if strings.EqualFold(value, "desc") {
		return "DESC"
	}
	return "ASC"
}
