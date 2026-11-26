package tasks

type TaskKey string

const (
	TaskScanRoots             TaskKey = "scan-roots"
	TaskImportDiscography     TaskKey = "import-discography"
	TaskPruneArtists          TaskKey = "prune-artists"
	TaskDeleteInactiveArtists TaskKey = "delete-inactive-artists"
	TaskEmbedReleaseCover     TaskKey = "embed-release-cover"
)

type Task struct {
	Key TaskKey
	ID  string // Data for the task if needed (e.g. artist id for refresh-artist)
}
