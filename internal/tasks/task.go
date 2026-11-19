package tasks

type TaskKey string

const (
	TaskScanRoots         TaskKey = "scan-roots"
	TaskImportDiscography TaskKey = "import-discography"
	TaskPruneArtists      TaskKey = "prune-artists"
)

type Task struct {
	Key TaskKey
	ID  string // Data for the task if needed (e.g. artist id for refresh-artist)
}
