// Command workflow-plugin-atlas-migrate is a workflow engine external plugin
// that provides the Atlas migration driver only. Atlas has heavy dependencies
// (HCL toolchain) so it is published as a separate binary to keep the main
// plugin binary minimal.
package main

import (
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/atlasplugin"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// version is set at build time via -X main.version=<version>.
var version string //nolint:unused

func main() {
	sdk.Serve(atlasplugin.NewPlugin())
}
