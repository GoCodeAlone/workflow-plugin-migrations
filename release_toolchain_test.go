package migrations_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"testing"
)

func TestMigrationImageToolchainMatchesGoMod(t *testing.T) {
	moduleJSON, err := exec.Command("go", "mod", "edit", "-json", "go.mod").Output()
	if err != nil {
		t.Fatalf("read go.mod metadata: %v", err)
	}
	var module struct {
		Go string
	}
	if err := json.Unmarshal(moduleJSON, &module); err != nil {
		t.Fatalf("parse go.mod metadata: %v", err)
	}
	if module.Go == "" {
		t.Fatal("go.mod has no Go version")
	}

	const dockerfile = "cmd/workflow-migrate/Dockerfile"
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	from := regexp.MustCompile(`(?m)^FROM(?:[ \t]+--[^ \t\r\n]+)*[ \t]+([^ \t\r\n]+)[ \t]+AS[ \t]+builder[ \t]*\r?$`)
	builders := from.FindAllStringSubmatch(string(data), -1)
	if len(builders) != 1 {
		t.Fatalf("%s has %d builder stages; want exactly one", dockerfile, len(builders))
	}
	want := "golang:" + module.Go + "-alpine"
	if got := builders[0][1]; got != want {
		t.Fatalf("%s builder image = %q; go.mod requires %q", dockerfile, got, want)
	}
}
