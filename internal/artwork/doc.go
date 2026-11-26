// Package artwork fetches release cover art and artist photos from public
// providers. Release covers try Cover Art Archive, then Deezer, then iTunes;
// artist photos try Deezer, then Wikimedia Commons (display-only, never
// embedded). Results are raw image bytes for the caller to cache on disk
// (config.CoverPath), record in the DB (store.UpsertCover), and embed into
// audio files.
package artwork
