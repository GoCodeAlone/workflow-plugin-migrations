// Command workflow-plugin-migrations is a workflow engine external plugin
// that provides golang-migrate and goose database migration drivers,
// module types (database.migrations, database.migration_driver), and
// pipeline step types (step.migrate_up/down/status/to).
//
// When invoked with --wfctl-cli, it serves as the entrypoint for
// `wfctl migrate *` dynamic CLI commands.
package main

import (
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/cli"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func main() {
	sdk.ServePluginFull(internal.NewPlugin(), cli.NewCLIProvider(), nil)
}
