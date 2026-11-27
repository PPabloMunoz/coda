package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/utils"
)

// DetectedTags echoes the file's own signals back to the UI so the
// suggestion banner can explain what was matched on. FilenameGuess reports
// that blanks were filled from the file path (e.g. after an unlink cleared
// the tags), so the UI can show lower confidence for those matches.
type DetectedTags struct {
	Title         string   `json:"title"`
	Album         string   `json:"album"`
	Artists       []string `json:"artists"`
	Track         int      `json:"track"`
	Disc          int      `json:"disc"`
	FilenameGuess bool     `json:"filename_guess"`
}

// SuggestedRelease is the compact release context for a suggested track.
type SuggestedRelease struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Date   *string `json:"date"`
	Status *string `json:"status"`
}

// SuggestedTrack is one ranked link candidate: full artist/group context so
// the link wizard can preselect at the deepest matched level, plus the
// track itself for the final step.
type SuggestedTrack struct {
	Artist  Artist             `json:"artist"`
	Group   ReleaseGroupParams `json:"group"`
	Release SuggestedRelease   `json:"release"`
	Track   TrackParams        `json:"track"`
	Score   float64            `json:"score"`
	Reasons []string           `json:"reasons"`
	HasFile bool               `json:"has_file"`
}

// SuggestionResult is the response model for file link suggestions.
type SuggestionResult struct {
	Detected    DetectedTags     `json:"detected"`
	Suggestions []SuggestedTrack `json:"suggestions"`
}

// suggestCandidate is a track row plus its release/group context before scoring.
type suggestCandidate struct {
	track        TrackParams
	trackCredit  sql.NullInt64
	releaseID    string
	releaseTitle string
	releaseDate  sql.NullString
	releaseStat  sql.NullString
	releaseCred  sql.NullInt64
	groupID      string
	groupTitle   string
	groupType    string
	groupCred    sql.NullInt64
}

// maxSuggestCandidates caps the rows pulled from SQLite before Go-side scoring.
const maxSuggestCandidates = 50

// minSuggestScore drops noise below this score from text matching.
const minSuggestScore = 0.15

// maxFilenameScore caps matches that relied on path-derived signals. Tag
// evidence always outranks a filename guess; an exact track MBID (1.0) is
// unaffected since that tier never uses filename signals.
const maxFilenameScore = 0.89

// SuggestOptions scopes a suggestion request. ArtistID is optional; when set,
// only tracks whose release group credits that artist are eligible — the same
// set the link wizard's group list shows for the artist.
type SuggestOptions struct {
	RootPath string
	FilePath string
	Snapshot *tags.Snapshot
	Limit    int
	ArtistID string
}

// SuggestTracks ranks library tracks against a file's tag snapshot, filling
// blanks from the file path (Coda's DD-TT_Title layout) when tags are
// missing or were cleared by an unlink. Exact track-MBID matches
// short-circuit; otherwise candidates are pulled by recording MBID, album
// MBID, or text and scored in Go. Never alters state.
//
// NOTE: the ranking below is a deliberately simple additive heuristic over
// tag/filename signals — good enough to bootstrap suggestions, but expected
// to be replaced by a more robust matching algorithm (better normalization,
// weighted/phonetic matching, duration and audio-based signals).
func (st *Store) SuggestTracks(ctx context.Context, opts SuggestOptions) (*SuggestionResult, error) {
	limit := opts.Limit
	if limit < 1 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	snap := opts.Snapshot
	if snap == nil {
		snap = &tags.Snapshot{}
	} else {
		cp := *snap
		snap = &cp
	}

	scopeID := strings.TrimSpace(opts.ArtistID)
	var scopeArtist *Artist
	if scopeID != "" {
		a, err := st.GetArtistByID(ctx, scopeID)
		if err != nil {
			return nil, err
		}
		scopeArtist = a
		scopeID = a.ID
	}

	fromFilename := false
	if sig := parseFilenameSignals(opts.RootPath, opts.FilePath); !sig.empty() {
		fromFilename = mergeFilenameSignals(snap, sig)
	}

	res := &SuggestionResult{Detected: detectedFromSnapshot(snap), Suggestions: []SuggestedTrack{}}
	res.Detected.FilenameGuess = fromFilename

	// Tier 1: exact track MBID. Short-circuits because nothing outranks it.
	// MBIDs are authoritative tag evidence, so filename caps never apply here.
	if validMBID(snap.MBReleaseTrackID) {
		if s, ok, err := st.suggestExactTrack(ctx, snap.MBReleaseTrackID, scopeArtist); err != nil {
			return nil, err
		} else if ok {
			res.Suggestions = append(res.Suggestions, *s)
			return res, nil
		}
	}

	cands, err := st.suggestCandidates(ctx, snap, scopeID)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return res, nil
	}

	sugs, err := st.scoreCandidates(ctx, snap, cands, limit)
	if err != nil {
		return nil, err
	}
	if scopeArtist != nil {
		// The wizard scoped to this artist: keep it even when the credit
		// lists another artist first (e.g. a collaboration).
		for i := range sugs {
			sugs[i].Artist = *scopeArtist
		}
	}
	if fromFilename {
		for i := range sugs {
			if sugs[i].Score > maxFilenameScore {
				sugs[i].Score = maxFilenameScore
			}
			sugs[i].Reasons = append(sugs[i].Reasons, "from filename")
		}
	}
	res.Suggestions = sugs
	return res, nil
}

// detectedFromSnapshot echoes the file's own signals. ARTISTS is used as-is;
// ArtistRaw is display-only and only a fallback when ARTISTS is empty — it is
// never split to derive names.
func detectedFromSnapshot(snap *tags.Snapshot) DetectedTags {
	d := DetectedTags{Track: snap.TrackNumber, Disc: snap.DiscNumber}
	d.Title = strings.TrimSpace(snap.Title)
	d.Album = strings.TrimSpace(snap.Album)
	if len(snap.Artists) > 0 {
		d.Artists = append([]string(nil), snap.Artists...)
	} else if strings.TrimSpace(snap.ArtistRaw) != "" {
		d.Artists = []string{strings.TrimSpace(snap.ArtistRaw)}
	}
	return d
}

// validMBID reports whether id is a usable MusicBrainz UUID.
func validMBID(id string) bool {
	return utils.ValidateMBID(strings.TrimSpace(id)) == nil
}

// filenameSignals are tag-like signals guessed from a file path. They only
// ever fill blanks left by the file's tags — tag values always win.
type filenameSignals struct {
	Title   string
	Album   string
	Artists []string
	Track   int
	Disc    int
}

func (s filenameSignals) empty() bool {
	return s.Title == "" && s.Album == "" && len(s.Artists) == 0 && s.Track == 0 && s.Disc == 0
}

// mergeFilenameSignals fills blank snapshot fields from path-derived
// signals, reporting whether anything was filled so callers can label
// provenance. ARTISTS is set as a single name, never split.
func mergeFilenameSignals(snap *tags.Snapshot, sig filenameSignals) bool {
	filled := false
	if strings.TrimSpace(snap.Title) == "" && sig.Title != "" {
		snap.Title = sig.Title
		filled = true
	}
	if strings.TrimSpace(snap.Album) == "" && sig.Album != "" {
		snap.Album = sig.Album
		filled = true
	}
	if len(snap.Artists) == 0 && strings.TrimSpace(snap.ArtistRaw) == "" && len(sig.Artists) > 0 {
		snap.Artists = sig.Artists
		filled = true
	}
	if snap.TrackNumber == 0 && sig.Track > 0 {
		snap.TrackNumber = sig.Track
		filled = true
	}
	if snap.DiscNumber == 0 && sig.Disc > 0 {
		snap.DiscNumber = sig.Disc
		filled = true
	}
	return filled
}

// parseFilenameSignals mirrors library.Target (.../Artist/Album/DD-TT_Title.ext).
// Underscores revert to spaces (the inverse of SanitizeSegment); only leading
// disc/track numbers are interpreted and the rest of the basename is the
// title. Conservative by design: ambiguous layouts yield partial signals, and
// anything deeper than Artist/Album contributes no dir guesses.
func parseFilenameSignals(rootPath, filePath string) filenameSignals {
	var s filenameSignals

	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	s.Title, s.Track, s.Disc = splitTrackPrefix(base)

	dir := filepath.Dir(filePath)
	rel, err := filepath.Rel(rootPath, dir)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return s
	}
	parts := strings.Split(rel, string(filepath.Separator))
	switch len(parts) {
	case 1:
		s.Album = cleanDirName(parts[0])
	case 2:
		if artist := cleanDirName(parts[0]); artist != "" {
			s.Artists = []string{artist}
		}
		s.Album = cleanDirName(parts[1])
	default:
		// Deeper nesting is non-standard (could be Genre/Artist/Album);
		// guessing an artist here risks a wrong-library-artist match.
	}

	return s
}

// cleanDirName reverts SanitizeSegment-style underscores and collapses
// whitespace. Dot-only and empty names are rejected.
func cleanDirName(name string) string {
	name = strings.Join(strings.Fields(strings.ReplaceAll(name, "_", " ")), " ")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// splitTrackPrefix interprets a leading DD-TT, DD.TT, or TT number prefix as
// disc/track and returns the remainder as the title. Names without a numeric
// prefix return the whole name as the title; number-only names return numbers
// with no title. Values over 3 digits (years, barcodes) are left as titles.
func splitTrackPrefix(base string) (title string, track, disc int) {
	plain := func() string {
		return strings.Join(strings.Fields(strings.ReplaceAll(base, "_", " ")), " ")
	}
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }

	i := 0
	for i < len(base) && isDigit(base[i]) {
		i++
	}
	if i == 0 || i > 3 {
		return plain(), 0, 0
	}
	first, _ := strconv.Atoi(base[:i])
	rest := base[i:]

	if rest == "" {
		return "", first, 0
	}
	if rest[0] != '-' && rest[0] != '.' && rest[0] != ' ' {
		return plain(), 0, 0
	}
	rest = strings.TrimLeft(rest, "-. ")

	// Optional second number: DD-TT disc-track.
	j := 0
	for j < len(rest) && isDigit(rest[j]) {
		j++
	}
	if j > 0 && j <= 3 {
		second, _ := strconv.Atoi(rest[:j])
		tail := strings.Trim(strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimLeft(rest[j:], "-. "), "_", " ")), " "), "-. ")
		if tail == "" {
			// "01" / "01-02": numbers without a title.
			return "", second, first
		}
		return tail, second, first
	}

	// Single TT prefix.
	tail := strings.Trim(strings.Join(strings.Fields(strings.ReplaceAll(rest, "_", " ")), " "), "-. ")
	if tail == "" {
		return "", first, 0
	}
	if _, err := strconv.Atoi(tail); err == nil && len(tail) <= 3 {
		// "01 02": numbers without a title.
		return "", first, 0
	}
	return tail, first, 0
}

// suggestExactTrack builds a full-context suggestion for a track id.
// ok=false when the track is unknown, has no active artist behind it, or (in
// scoped mode) its group doesn't credit the scoped artist.
func (st *Store) suggestExactTrack(ctx context.Context, trackID string, scope *Artist) (s *SuggestedTrack, ok bool, err error) {
	rel, track, err := st.GetTrackFullRelease(ctx, strings.TrimSpace(trackID))
	if err != nil {
		if apperrors.Is(err, apperrors.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var artist *Artist
	if scope != nil {
		// Parity with the wizard's group list: only groups crediting the
		// scoped artist are eligible. Anything else falls through to
		// scoped text search instead of leaking a cross-artist hit.
		if !creditHasArtist(rel.Group.Credit, scope.ID) {
			return nil, false, nil
		}
		artist = scope
	} else {
		artist, ok, err = st.suggestArtist(ctx, rel.Group.Credit, rel.Credit, track.Credit)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}
	hasFile, err := st.tracksHaveFiles(ctx, []string{track.ID})
	if err != nil {
		return nil, false, err
	}
	return &SuggestedTrack{
		Artist: *artist,
		Group:  rel.Group,
		Release: SuggestedRelease{
			ID: rel.ID, Title: rel.Title, Date: rel.Date, Status: rel.Status,
		},
		Track:   *track,
		Score:   1,
		Reasons: []string{"exact track MBID"},
		HasFile: hasFile[track.ID],
	}, true, nil
}

// creditHasArtist reports whether any credit entry references artistID.
func creditHasArtist(entries []CreditEntry, artistID string) bool {
	for _, e := range entries {
		if e.ArtistID == artistID {
			return true
		}
	}
	return false
}

// suggestCandidates pulls candidate tracks: recording-MBID exact matches,
// all tracks of an MBID-matched release, or text search by title/album/artist.
// With a scope artist id the same tiers run constrained to that artist's
// groups, with the full group dump as a last resort — so scoped matches are
// as precise as global ones instead of depending on LIMIT windows.
func (st *Store) suggestCandidates(ctx context.Context, snap *tags.Snapshot, scopeID string) ([]suggestCandidate, error) {
	scopeCond := ""
	scopeArgs := []any{}
	if scopeID != "" {
		scopeCond = ` AND EXISTS (
			SELECT 1 FROM artist_credit_items aci_scope
			WHERE aci_scope.artist_credit_id = rg.artist_credit_id
			  AND aci_scope.artist_id = ?)`
		scopeArgs = append(scopeArgs, scopeID)
	}

	if validMBID(snap.MBTrackID) {
		args := append([]any{strings.TrimSpace(snap.MBTrackID)}, scopeArgs...)
		cands, err := st.queryCandidateTracks(ctx, `t.recording_id = ?`+scopeCond, args)
		if err != nil {
			return nil, err
		}
		if len(cands) > 0 {
			return cands, nil
		}
	}

	if validMBID(snap.MBAlbumID) {
		rel, err := st.GetRelease(ctx, strings.TrimSpace(snap.MBAlbumID), false)
		if err != nil {
			if !apperrors.Is(err, apperrors.ErrNotFound) {
				return nil, err
			}
		} else if scopeID == "" || creditHasArtist(rel.Group.Credit, scopeID) {
			cands := make([]suggestCandidate, 0, len(rel.Tracks))
			for _, t := range rel.Tracks {
				var tcID, rcID, gcID sql.NullInt64
				cands = append(cands, suggestCandidate{
					track: t, trackCredit: tcID,
					releaseID: rel.ID, releaseTitle: rel.Title,
					releaseCred: rcID,
					groupID:     rel.Group.ID, groupTitle: rel.Group.Title, groupType: rel.Group.Type,
					groupCred: gcID,
				})
			}
			// Re-query through the candidate path so credit ids are populated.
			if len(cands) > 0 {
				ids := make([]any, 0, len(cands))
				for _, c := range cands {
					ids = append(ids, c.track.ID)
				}
				cond := `t.id IN (` + placeholders(len(ids)) + `)` + scopeCond
				if got, err := st.queryCandidateTracks(ctx, cond, append(ids, scopeArgs...)); err != nil {
					return nil, err
				} else if len(got) > 0 {
					return got, nil
				}
			}
		}
	}

	title := strings.TrimSpace(snap.Title)
	album := strings.TrimSpace(snap.Album)
	if title != "" {
		args := append([]any{likePattern(title)}, scopeArgs...)
		cands, err := st.queryCandidateTracks(ctx,
			`unaccent(t.title) LIKE unaccent(?) ESCAPE '\'`+scopeCond, args)
		if err != nil {
			return nil, err
		}
		if len(cands) > 0 {
			return cands, nil
		}
	}
	if album != "" {
		pat := likePattern(album)
		args := append([]any{pat, pat}, scopeArgs...)
		cands, err := st.queryCandidateTracks(ctx,
			`(unaccent(r.title) LIKE unaccent(?) ESCAPE '\' OR unaccent(rg.title) LIKE unaccent(?) ESCAPE '\')`+scopeCond,
			args)
		if err != nil {
			return nil, err
		}
		if len(cands) > 0 {
			return cands, nil
		}
	}
	if scopeID != "" {
		// Last resort in scoped mode: the artist's groups. Precise tiers
		// above already had their chance, so LIMIT windows can't shadow
		// an exact or title match.
		return st.candidatesByScope(ctx, scopeID)
	}
	// Last resort: artist text → groups → their releases' tracks.
	if names := fileArtistNames(snap); len(names) > 0 {
		return st.candidatesByArtistText(ctx, names)
	}
	return nil, nil
}

// candidatesByScope returns tracks from the scoped artist's release groups,
// capped at 20 groups. Last resort only: the precise tiers above run first
// with the same scope, so LIMIT windows can't shadow an exact or title
// match. Rank still comes from Go-side text scoring.
func (st *Store) candidatesByScope(ctx context.Context, scopeID string) ([]suggestCandidate, error) {
	// Scanned in a closure so rows close via defer before the candidate
	// query below runs on the single-connection DB.
	groupIDs, err := func() ([]any, error) {
		rows, err := st.db.QueryContext(ctx, `
		SELECT DISTINCT rg.id
		FROM release_groups rg
		JOIN artist_credit_items aci_scope
		  ON aci_scope.artist_credit_id = rg.artist_credit_id
		WHERE aci_scope.artist_id = ?
		ORDER BY rg.title
		LIMIT 20`,
			scopeID,
		)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("suggest query scope groups: %w", err),
			)
		}
		defer func() { _ = rows.Close() }()
		var out []any
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("suggest scan scope group: %w", err),
				)
			}
			out = append(out, id)
		}
		if err := rows.Err(); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("suggest reading scope groups: %w", err),
			)
		}
		return out, nil
	}()
	if err != nil {
		return nil, err
	}
	if len(groupIDs) == 0 {
		return nil, nil
	}
	return st.queryCandidateTracks(ctx,
		`rg.id IN (`+placeholders(len(groupIDs))+`)`, groupIDs)
}

// queryCandidateTracks runs one candidate query over tracks + release + group.
func (st *Store) queryCandidateTracks(ctx context.Context, cond string, args []any) ([]suggestCandidate, error) {
	rows, err := st.db.QueryContext(ctx, `
		SELECT t.id, t.release_id, t.disc_number, t.position, t.number, t.title,
			t.artist_credit_id, t.duration_ms, t.recording_id,
			r.title, r.date, r.status, r.artist_credit_id,
			rg.id, rg.title, rg.type, rg.artist_credit_id
		FROM tracks t
		JOIN releases r ON r.id = t.release_id
		JOIN release_groups rg ON rg.id = r.release_group_id
		WHERE `+cond+`
		LIMIT `+strconv.Itoa(maxSuggestCandidates),
		args...,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("suggest query tracks: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	cands := []suggestCandidate{}
	for rows.Next() {
		var c suggestCandidate
		var durMS sql.NullInt64
		if err := rows.Scan(
			&c.track.ID, &c.track.ReleaseID, &c.track.Disc, &c.track.Position,
			&c.track.Number, &c.track.Title, &c.trackCredit, &durMS, &c.track.RecordingID,
			&c.releaseTitle, &c.releaseDate, &c.releaseStat, &c.releaseCred,
			&c.groupID, &c.groupTitle, &c.groupType, &c.groupCred,
		); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("suggest scan track: %w", err),
			)
		}
		if durMS.Valid {
			v := int(durMS.Int64)
			c.track.DurMS = &v
		}
		c.releaseID = c.track.ReleaseID
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("suggest reading tracks: %w", err),
		)
	}
	return cands, nil
}

// candidatesByArtistText finds groups whose credit names match, then returns
// their releases' tracks as candidates.
func (st *Store) candidatesByArtistText(ctx context.Context, names []string) ([]suggestCandidate, error) {
	conds := make([]string, 0, len(names)*2)
	args := make([]any, 0, len(names)*2)
	for _, n := range names {
		pat := likePattern(n)
		conds = append(conds,
			`unaccent(a_query.name) LIKE unaccent(?) ESCAPE '\'`,
			`unaccent(aci_query.credited_name) LIKE unaccent(?) ESCAPE '\'`)
		args = append(args, pat, pat)
	}
	// Scanned in a closure so rows close via defer before the candidate
	// query below runs on the single-connection DB.
	groupIDs, err := func() ([]any, error) {
		rows, err := st.db.QueryContext(ctx, `
		SELECT DISTINCT rg.id
		FROM release_groups rg
		JOIN artist_credit_items aci_query ON aci_query.artist_credit_id = rg.artist_credit_id
		JOIN artists a_query ON a_query.id = aci_query.artist_id
		WHERE `+strings.Join(conds, " OR ")+`
		LIMIT 20`,
			args...,
		)
		if err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("suggest query groups: %w", err),
			)
		}
		defer func() { _ = rows.Close() }()
		var out []any
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("suggest scan group: %w", err),
				)
			}
			out = append(out, id)
		}
		if err := rows.Err(); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("suggest reading groups: %w", err),
			)
		}
		return out, nil
	}()
	if err != nil {
		return nil, err
	}
	if len(groupIDs) == 0 {
		return nil, nil
	}
	return st.queryCandidateTracks(ctx,
		`rg.id IN (`+placeholders(len(groupIDs))+`)`, groupIDs)
}

// scoreCandidates attaches credits, resolves the wizard artist per candidate,
// scores, and returns the top limit entries.
func (st *Store) scoreCandidates(ctx context.Context, snap *tags.Snapshot, cands []suggestCandidate, limit int) ([]SuggestedTrack, error) {
	creditIDs := map[int64]struct{}{}
	for _, c := range cands {
		for _, id := range []sql.NullInt64{c.trackCredit, c.releaseCred, c.groupCred} {
			if id.Valid {
				creditIDs[id.Int64] = struct{}{}
			}
		}
	}
	credits := map[int64][]CreditEntry{}
	if len(creditIDs) > 0 {
		var err error
		credits, err = st.getCreditEntries(ctx, creditIDs)
		if err != nil {
			return nil, err
		}
	}

	artistIDs := map[string]struct{}{}
	for _, c := range cands {
		for _, entries := range [][]CreditEntry{
			credits[c.groupCred.Int64], credits[c.releaseCred.Int64], credits[c.trackCredit.Int64],
		} {
			for _, e := range entries {
				if strings.TrimSpace(e.ArtistID) != "" {
					artistIDs[e.ArtistID] = struct{}{}
				}
			}
		}
	}
	artists := map[string]Artist{}
	if len(artistIDs) > 0 {
		ids := make([]any, 0, len(artistIDs))
		for id := range artistIDs {
			ids = append(ids, id)
		}
		// Scanned in a closure so rows close via defer before the file
		// query below runs on the single-connection DB.
		scanned, err := func() (map[string]Artist, error) {
			rows, err := st.db.QueryContext(ctx, `
			SELECT id, name, sort_name, disambiguation, active, in_library
			FROM artists
			WHERE id IN (`+placeholders(len(ids))+`) AND active = 1`,
				ids...,
			)
			if err != nil {
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("suggest query artists: %w", err),
				)
			}
			defer func() { _ = rows.Close() }()
			out := map[string]Artist{}
			for rows.Next() {
				var a Artist
				if err := rows.Scan(&a.ID, &a.Name, &a.SortName, &a.Disambiguation, &a.Active, &a.InLibrary); err != nil {
					return nil, apperrors.Wrap(
						apperrors.ErrInternal,
						fmt.Errorf("suggest scan artist: %w", err),
					)
				}
				out[a.ID] = a
			}
			if err := rows.Err(); err != nil {
				return nil, apperrors.Wrap(
					apperrors.ErrInternal,
					fmt.Errorf("suggest reading artists: %w", err),
				)
			}
			return out, nil
		}()
		if err != nil {
			return nil, err
		}
		artists = scanned
	}

	trackIDs := make([]string, 0, len(cands))
	for _, c := range cands {
		trackIDs = append(trackIDs, c.track.ID)
	}
	hasFile, err := st.tracksHaveFiles(ctx, trackIDs)
	if err != nil {
		return nil, err
	}

	scored := []SuggestedTrack{}
	for _, c := range cands {
		trackCredit := credits[c.trackCredit.Int64]
		releaseCredit := credits[c.releaseCred.Int64]
		groupCredit := credits[c.groupCred.Int64]

		artist := firstActiveArtist(artists, groupCredit, releaseCredit, trackCredit)
		if artist == nil {
			continue
		}

		track := c.track
		track.Credit = trackCredit
		score, reasons := scoreCandidate(snap, &c, trackCredit, releaseCredit, groupCredit)
		if score < minSuggestScore {
			continue
		}
		if hasFile[c.track.ID] {
			score -= 0.1
			reasons = append(reasons, "already has a file")
		}
		if score < 0 {
			score = 0
		}
		scored = append(scored, SuggestedTrack{
			Artist: *artist,
			Group: ReleaseGroupParams{
				ID: c.groupID, Title: c.groupTitle, Type: c.groupType, Credit: groupCredit,
			},
			Release: SuggestedRelease{
				ID: c.releaseID, Title: c.releaseTitle,
				Date: nullString(c.releaseDate), Status: nullString(c.releaseStat),
			},
			Track:   track,
			Score:   math.Round(score*100) / 100,
			Reasons: reasons,
		})
	}

	// Insertion sort by score desc; candidate sets are small (<= 50).
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0 && scored[j].Score > scored[j-1].Score; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	if scored == nil {
		scored = []SuggestedTrack{}
	}
	return scored, nil
}

// firstActiveArtist resolves the wizard artist: group credit first (the
// primary/library artist groups are listed under), then release, then track.
// Returns nil when no credited artist is an active library row.
func firstActiveArtist(artists map[string]Artist, groups ...[]CreditEntry) *Artist {
	for _, entries := range groups {
		for _, e := range entries {
			if a, ok := artists[e.ArtistID]; ok {
				artist := a
				return &artist
			}
		}
	}
	return nil
}

// suggestArtist resolves the wizard artist for an exact-MBID suggestion from
// already-loaded credit entries.
func (st *Store) suggestArtist(ctx context.Context, groups ...[]CreditEntry) (*Artist, bool, error) {
	for _, entries := range groups {
		for _, e := range entries {
			if strings.TrimSpace(e.ArtistID) == "" {
				continue
			}
			a, err := st.GetArtistByID(ctx, e.ArtistID)
			if err != nil {
				if apperrors.Is(err, apperrors.ErrNotFound) {
					continue
				}
				return nil, false, err
			}
			return a, true, nil
		}
	}
	return nil, false, nil
}

// tracksHaveFiles reports which track ids already have a linked local file.
// local_files.track_id is not UNIQUE, so duplicates are possible — callers
// rank those lower instead of hiding them.
func (st *Store) tracksHaveFiles(ctx context.Context, trackIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(trackIDs))
	if len(trackIDs) == 0 {
		return out, nil
	}
	rows, err := st.db.QueryContext(ctx,
		`SELECT DISTINCT track_id FROM local_files WHERE track_id IN (`+placeholders(len(trackIDs))+`)`,
		toAny(trackIDs)...,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("suggest query linked files: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id sql.NullString
		if err := rows.Scan(&id); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("suggest scan linked file: %w", err),
			)
		}
		if id.Valid {
			out[id.String] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("suggest reading linked files: %w", err),
		)
	}
	return out, nil
}

func toAny(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// fileArtistNames returns the file's artist signals: ordered ARTISTS when
// present, else the display-only ARTIST. Never splits ARTIST into names.
func fileArtistNames(snap *tags.Snapshot) []string {
	names := []string{}
	for _, n := range snap.Artists {
		if strings.TrimSpace(n) != "" {
			names = append(names, strings.TrimSpace(n))
		}
	}
	if len(names) == 0 && strings.TrimSpace(snap.ArtistRaw) != "" {
		names = append(names, strings.TrimSpace(snap.ArtistRaw))
	}
	return names
}

// scoreCandidate scores one candidate track against the file snapshot.
// Weights are hand-picked and additive — a simple heuristic, not a tuned
// model. See SuggestTracks: this is expected to be replaced by a more
// robust matching algorithm.
func scoreCandidate(snap *tags.Snapshot, c *suggestCandidate,
	trackCredit, releaseCredit, groupCredit []CreditEntry,
) (float64, []string) {
	var score float64
	reasons := []string{}

	normTitle := normalizeSearch(strings.TrimSpace(snap.Title))
	normCandTitle := normalizeSearch(strings.TrimSpace(c.track.Title))
	if normTitle != "" && normCandTitle != "" {
		if normTitle == normCandTitle {
			score += 0.45
			reasons = append(reasons, "title match")
		} else if len(normTitle) >= 3 && (strings.Contains(normCandTitle, normTitle) || strings.Contains(normTitle, normCandTitle)) {
			score += 0.25
			reasons = append(reasons, "partial title match")
		}
	}

	if validMBID(snap.MBTrackID) && strings.EqualFold(strings.TrimSpace(snap.MBTrackID), c.track.RecordingID) {
		score += 0.3
		reasons = append(reasons, "recording MBID match")
	}
	if validMBID(snap.MBAlbumID) && strings.EqualFold(strings.TrimSpace(snap.MBAlbumID), c.releaseID) {
		score += 0.15
		reasons = append(reasons, "release MBID match")
	}

	// Artist: MBID match outranks name match.
	fileMBIDs := map[string]struct{}{}
	for _, id := range append(append([]string{}, snap.MBArtistIDs...), snap.MBAlbumArtistIDs...) {
		if validMBID(id) {
			fileMBIDs[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
		}
	}
	idMatch := false
	for _, entries := range [][]CreditEntry{trackCredit, releaseCredit, groupCredit} {
		for _, e := range entries {
			if _, ok := fileMBIDs[strings.ToLower(e.ArtistID)]; ok {
				idMatch = true
			}
		}
	}
	if idMatch {
		score += 0.3
		reasons = append(reasons, "artist MBID match")
	} else {
		fileNames := fileArtistNames(snap)
		candNames := map[string]struct{}{}
		for _, entries := range [][]CreditEntry{trackCredit, releaseCredit, groupCredit} {
			for _, e := range entries {
				for _, n := range []string{e.CreditedName, e.Name} {
					if strings.TrimSpace(n) != "" {
						candNames[normalizeSearch(n)] = struct{}{}
					}
				}
			}
		}
		nameMatch := false
		for _, n := range fileNames {
			nn := normalizeSearch(n)
			if nn == "" {
				continue
			}
			if _, ok := candNames[nn]; ok {
				nameMatch = true
				break
			}
			if len(nn) >= 3 {
				for cn := range candNames {
					if strings.Contains(cn, nn) || strings.Contains(nn, cn) {
						nameMatch = true
						break
					}
				}
			}
			if nameMatch {
				break
			}
		}
		if nameMatch {
			score += 0.25
			reasons = append(reasons, "artist match")
		}
	}

	normAlbum := normalizeSearch(strings.TrimSpace(snap.Album))
	if normAlbum != "" {
		normRel := normalizeSearch(c.releaseTitle)
		normGroup := normalizeSearch(c.groupTitle)
		if normAlbum == normRel || normAlbum == normGroup {
			score += 0.2
			reasons = append(reasons, "album match")
		} else if len(normAlbum) >= 3 && (strings.Contains(normRel, normAlbum) || strings.Contains(normGroup, normAlbum)) {
			score += 0.1
			reasons = append(reasons, "partial album match")
		}
	}

	if snap.TrackNumber > 0 {
		if c.track.Position == snap.TrackNumber || c.track.Number == strconv.Itoa(snap.TrackNumber) {
			score += 0.12
			reasons = append(reasons, "track number match")
		}
		if snap.DiscNumber > 0 && c.track.Disc == snap.DiscNumber {
			score += 0.05
			reasons = append(reasons, "disc match")
		}
	}

	if score > 0.99 {
		score = 0.99
	}
	return score, reasons
}
