// Package tasks runs background jobs (root scans, discography imports) through a serial FIFO queue with per-artist import state.
//
// Scans sync the DB by path and prune missing files. They also auto-link
// exactly-identifiable files: a file whose on-disk MBReleaseTrackID resolves
// to a known track gets the full link treatment (tag rewrite + cover embed +
// move to its library path), same as manual POST /api/v1/files/:id/link.
// Anything ambiguous is left for the link wizard; occupied destinations are
// skipped and reported, never overwritten.
package tasks
