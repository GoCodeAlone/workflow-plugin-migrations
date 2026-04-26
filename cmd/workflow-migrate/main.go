// Command workflow-migrate is a standalone binary for running database migrations
// as a pre-deploy job in DO App Platform, Kubernetes, or any OCI-compatible runtime.
// It applies golang-migrate or goose migrations against a Postgres database.
//
// Usage:
//
//	workflow-migrate up [flags]
//	workflow-migrate down [flags]
//	workflow-migrate status [flags]
//	workflow-migrate goto <version> [flags]
//	workflow-migrate force <version> [flags]
package main

import (
	"fmt"
	"os"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/cli"
)

func main() {
	root := cli.NewRoot()
	root.Use = "workflow-migrate"
	root.Short = "Standalone migration runner for pre-deploy jobs"
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
