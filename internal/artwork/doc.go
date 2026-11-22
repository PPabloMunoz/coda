// Package artwork fetches release cover art from public providers, trying
// them in order: Cover Art Archive first, then fallbacks (Deezer). Results
// are raw image bytes for the caller to cache on disk (config.CoverPath),
// record in the DB (store.UpsertCover), and embed into audio files.
package artwork
