package tags

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bogem/id3v2/v2"
)

// picardTXXX maps the uppercase descriptions TagLib emits for ID3 TXXX
// frames to the exact casing MusicBrainz Picard and mutagen-based tools
// expect. TagLib uppercases PropertyMap keys, so it cannot write these
// descriptions itself.
var picardTXXX = map[string]string{
	"MUSICBRAINZ ARTIST ID":             "MusicBrainz Artist Id",
	"MUSICBRAINZ ALBUM ARTIST ID":       "MusicBrainz Album Artist Id",
	"MUSICBRAINZ ALBUM ID":              "MusicBrainz Album Id",
	"MUSICBRAINZ RELEASE GROUP ID":      "MusicBrainz Release Group Id",
	"MUSICBRAINZ RELEASE TRACK ID":      "MusicBrainz Release Track Id",
	"MUSICBRAINZ TRACK ID":              "MusicBrainz Track Id",
	"MUSICBRAINZ ALBUM TYPE":            "MusicBrainz Album Type",
	"MUSICBRAINZ ALBUM STATUS":          "MusicBrainz Album Status",
	"MUSICBRAINZ ALBUM RELEASE COUNTRY": "MusicBrainz Album Release Country",
	"MUSICBRAINZ WORK ID":               "MusicBrainz Work Id",
	"ACOUSTID ID":                       "Acoustid Id",
	"ACOUSTID FINGERPRINT":              "Acoustid Fingerprint",
	"MUSICIP PUID":                      "MusicIP PUID",
}

// normalizeMP3TXXX rewrites uppercase MusicBrainz TXXX descriptions in
// MP3 files to Picard's casing. TagLib writes the frames; this second
// pass only fixes their descriptions, preserving values, encoding and
// every other frame in the tag.
func normalizeMP3TXXX(path string) error {
	if !strings.EqualFold(filepath.Ext(path), ".mp3") {
		return nil
	}

	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("normalize id3 %q: %w", path, err)
	}
	defer func() { _ = tag.Close() }()

	frames := tag.GetFrames("TXXX")
	if len(frames) == 0 {
		return nil
	}

	changed := false
	normalized := make([]id3v2.Framer, 0, len(frames))
	textIndex := make(map[string]int, len(frames))
	for _, f := range frames {
		frame, ok := f.(id3v2.UserDefinedTextFrame)
		if !ok {
			normalized = append(normalized, f)
			continue
		}
		if name, ok := picardTXXX[frame.Description]; ok {
			frame.Description = name
			changed = true
		}
		if i, ok := textIndex[frame.Description]; ok {
			existing := normalized[i].(id3v2.UserDefinedTextFrame)
			normalized[i] = mergeTXXX(existing, frame)
			continue
		}
		textIndex[frame.Description] = len(normalized)
		normalized = append(normalized, frame)
	}
	if !changed {
		return nil
	}

	tag.DeleteFrames("TXXX")
	for _, f := range normalized {
		if frame, ok := f.(id3v2.UserDefinedTextFrame); ok {
			tag.AddUserDefinedTextFrame(frame)
			continue
		}
		tag.AddFrame("TXXX", f)
	}
	if err := tag.Save(); err != nil {
		return fmt.Errorf("normalize id3 %q: %w", path, err)
	}
	return nil
}

// mergeTXXX combines two frames that normalize to the same description.
// Multi-value TXXX values are NUL-separated in memory.
func mergeTXXX(dst, src id3v2.UserDefinedTextFrame) id3v2.UserDefinedTextFrame {
	values := splitTXXXValues(dst.Value)
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		seen[v] = struct{}{}
	}
	for _, v := range splitTXXXValues(src.Value) {
		if _, ok := seen[v]; ok {
			continue
		}
		values = append(values, v)
		seen[v] = struct{}{}
	}
	dst.Value = strings.Join(values, "\x00")
	return dst
}

func splitTXXXValues(v string) []string {
	parts := strings.Split(v, "\x00")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
