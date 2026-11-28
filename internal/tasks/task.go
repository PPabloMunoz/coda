package tasks

type TaskKey string

const (
	TaskScanRoots             TaskKey = "scan-roots"
	TaskImportDiscography     TaskKey = "import-discography"
	TaskPruneArtists          TaskKey = "prune-artists"
	TaskDeleteInactiveArtists TaskKey = "delete-inactive-artists"
	TaskEmbedReleaseCover     TaskKey = "embed-release-cover"
	TaskClearReleaseCover     TaskKey = "clear-release-cover"
	TaskRelinkRelease         TaskKey = "relink-release"
	TaskPrefetchCover         TaskKey = "prefetch-cover"
	TaskPrefetchArtistCover   TaskKey = "prefetch-artist-cover"
)

type Task struct {
	Key TaskKey
	ID  string // Data for the task if needed (e.g. artist id for refresh-artist)
	// ForcePrune allows a scan to prune roots whose complete walk yields
	// zero files. Incomplete walks never prune, even when set.
	ForcePrune bool
}
