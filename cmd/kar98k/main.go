package main

import "github.com/kar98k/internal/cli"

var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
)

func main() {
	cli.SetVersion(version, buildTime)
	cli.SetGitCommit(gitCommit)
	cli.Execute()
}
