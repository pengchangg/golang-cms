package version

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func String() string {
	return fmt.Sprintf("cms %s (commit=%s, built=%s)", Version, Commit, BuildTime)
}
