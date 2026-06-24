package version

// Build-time variables, injected via -ldflags by Taskfile/goreleaser.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Full returns "Version (Commit) Date".
func Full() string { return Version + " (" + Commit + ") " + Date }
