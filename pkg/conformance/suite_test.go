package conformance_test

import (
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/goose"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/conformance"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
)

func TestConformance_GolangMigrate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance: requires postgres")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("no postgres available: %v", err)
	}
	defer h.Close(t)

	suite := conformance.NewSuite(h.DSN())
	suite.Run(t, golangmigrate.New())
}

func TestConformance_Goose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance: requires postgres")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("no postgres available: %v", err)
	}
	defer h.Close(t)

	suite := conformance.NewSuite(h.DSN())
	suite.Run(t, goose.New())
}
