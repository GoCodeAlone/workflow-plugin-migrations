package internal_test

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
	"github.com/GoCodeAlone/workflow/schema"
	"gopkg.in/yaml.v3"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal"
)

func TestPlugin_Manifest(t *testing.T) {
	p := internal.NewPlugin()
	m := p.Manifest()
	if m.Name == "" {
		t.Error("Manifest.Name is empty")
	}
	if m.Version == "" {
		t.Error("Manifest.Version is empty")
	}
}

func TestPlugin_ModuleTypes(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	types := p.ModuleTypes()
	want := map[string]bool{
		"database.migrations":       true,
		"database.migration_driver": true,
	}
	for _, tp := range types {
		delete(want, tp)
	}
	if len(want) > 0 {
		t.Errorf("missing module types: %v", want)
	}
}

func TestPlugin_StepTypes(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	types := p.StepTypes()
	want := map[string]bool{
		"step.migrate_up":     true,
		"step.migrate_down":   true,
		"step.migrate_status": true,
		"step.migrate_to":     true,
	}
	for _, tp := range types {
		delete(want, tp)
	}
	if len(want) > 0 {
		t.Errorf("missing step types: %v", want)
	}
}

func TestPlugin_CreateModule_Unknown(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	_, err := p.CreateModule("unknown.type", "x", nil)
	if err == nil {
		t.Error("expected error for unknown module type")
	}
}

func TestPlugin_CreateStep_Unknown(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	_, err := p.CreateStep("step.unknown", "x", nil)
	if err == nil {
		t.Error("expected error for unknown step type")
	}
}

func TestPlugin_CreateStep_MigrateUp(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	step, err := p.CreateStep("step.migrate_up", "my-step", map[string]any{
		"driver":     "golang-migrate",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("CreateStep(step.migrate_up): %v", err)
	}
	if step == nil {
		t.Error("CreateStep returned nil")
	}
}

func TestPlugin_CreateModule_MigrationsValidConfig(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	m, err := p.CreateModule("database.migrations", "my-db", map[string]any{
		"driver":     "golang-migrate",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("CreateModule(database.migrations): %v", err)
	}
	if err := m.Init(); err != nil {
		t.Fatalf("Init(): %v", err)
	}
}

// TestPlugin_SchemaProvider verifies that the plugin implements sdk.SchemaProvider
// and returns a schema for each advertised module type.
func TestPlugin_SchemaProvider(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.SchemaProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.SchemaProvider")
	}
	schemas := p.ModuleSchemas()
	if len(schemas) == 0 {
		t.Fatal("ModuleSchemas() returned empty slice")
	}
	want := map[string]bool{
		"database.migrations":       true,
		"database.migration_driver": true,
	}
	for _, s := range schemas {
		delete(want, s.Type)
	}
	if len(want) > 0 {
		t.Errorf("missing module schemas for: %v", want)
	}
}

// TestPlugin_ModuleContracts verifies that each advertised module type has a strict
// field contract descriptor with at least one required input.
func TestPlugin_ModuleContracts(t *testing.T) {
	p := internal.NewPlugin().(*internal.MigrationsPlugin)
	contracts := p.ModuleContracts()
	wantTypes := []string{"database.migrations", "database.migration_driver"}
	for _, typ := range wantTypes {
		c, ok := contracts[typ]
		if !ok {
			t.Errorf("missing module contract for %q", typ)
			continue
		}
		if len(c.RequiredInputs)+len(c.OptionalInputs) == 0 {
			t.Errorf("module contract for %q has no declared inputs", typ)
		}
	}
}

// TestPlugin_StepContracts verifies that each advertised step type has a strict
// field contract descriptor with at least one input and at least one output.
func TestPlugin_StepContracts(t *testing.T) {
	p := internal.NewPlugin().(*internal.MigrationsPlugin)
	contracts := p.StepContracts()
	wantTypes := []string{
		"step.migrate_up",
		"step.migrate_down",
		"step.migrate_status",
		"step.migrate_to",
	}
	for _, typ := range wantTypes {
		c, ok := contracts[typ]
		if !ok {
			t.Errorf("missing step contract for %q", typ)
			continue
		}
		if len(c.RequiredInputs)+len(c.OptionalInputs) == 0 {
			t.Errorf("step contract for %q has no declared inputs", typ)
		}
		if len(c.Outputs) == 0 {
			t.Errorf("step contract for %q has no declared outputs", typ)
		}
	}
}

// TestPlugin_StepSchemas verifies that PluginStepSchemas returns a schema for
// every advertised step type.
func TestPlugin_StepSchemas(t *testing.T) {
	schemas := internal.PluginStepSchemas()
	want := map[string]bool{
		"step.migrate_up":     true,
		"step.migrate_down":   true,
		"step.migrate_status": true,
		"step.migrate_to":     true,
	}
	for _, s := range schemas {
		if s.Type == "" {
			t.Error("step schema has empty Type")
		}
		delete(want, s.Type)
		if len(s.ConfigFields) == 0 {
			t.Errorf("step schema %q has no config fields", s.Type)
		}
		if len(s.Outputs) == 0 {
			t.Errorf("step schema %q has no outputs", s.Type)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing step schemas for: %v", want)
	}
}

// TestPlugin_ContractCoverage ensures contract coverage matches advertised types:
// every module type and step type must have a corresponding strict contract.
func TestPlugin_ContractCoverage(t *testing.T) {
	mp, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	sp, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	p := internal.NewPlugin().(*internal.MigrationsPlugin)
	moduleContracts := p.ModuleContracts()
	stepContracts := p.StepContracts()

	for _, modType := range mp.ModuleTypes() {
		if _, ok := moduleContracts[modType]; !ok {
			t.Errorf("module type %q has no strict contract descriptor", modType)
		}
	}
	for _, stepType := range sp.StepTypes() {
		if _, ok := stepContracts[stepType]; !ok {
			t.Errorf("step type %q has no strict contract descriptor", stepType)
		}
	}
}

// TestPlugin_PluginJSONStepSchemasDrift guards against the shipped plugin.json
// stepSchemas drifting from the Go PluginStepSchemas() definition.
// It verifies the full schema payload, not just step type names.
func TestPlugin_PluginJSONStepSchemasDrift(t *testing.T) {
	data, err := os.ReadFile("../plugin.json")
	if err != nil {
		t.Fatalf("open plugin.json: %v", err)
	}
	var manifest struct {
		StepSchemas []*schema.StepSchema `json:"stepSchemas"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse plugin.json: %v", err)
	}

	jsonSchemas := canonicalStepSchemas(t, manifest.StepSchemas)
	goSchemas := canonicalStepSchemas(t, internal.PluginStepSchemas())
	if !reflect.DeepEqual(jsonSchemas, goSchemas) {
		t.Fatalf("plugin.json stepSchemas drifted from PluginStepSchemas()\nplugin.json: %#v\nGo: %#v", jsonSchemas, goSchemas)
	}
}

// TestPlugin_ContractsJSONDrift guards against plugin.contracts.json drifting
// from the contract descriptors this plugin publishes for strict validation.
func TestPlugin_ContractsJSONDrift(t *testing.T) {
	data, err := os.ReadFile("../plugin.contracts.json")
	if err != nil {
		t.Fatalf("open plugin.contracts.json: %v", err)
	}
	var file struct {
		Version   string               `json:"version"`
		Contracts []contractDescriptor `json:"contracts"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse plugin.contracts.json: %v", err)
	}

	if file.Version != "1" {
		t.Fatalf("plugin.contracts.json version = %q; want 1", file.Version)
	}
	got := sortedContracts(file.Contracts)
	want := sortedContracts(expectedContractDescriptors())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin.contracts.json drifted from expected descriptors\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestPlugin_DownloadsMatchGoReleaserMainArchiveMatrix(t *testing.T) {
	manifest := readPluginManifest(t)
	cfg := readGoReleaserConfig(t)
	build := cfg.findBuild(t, "workflow-plugin-migrations")

	want := make(map[string]string)
	for _, goos := range build.Goos {
		for _, goarch := range build.Goarch {
			key := goos + "/" + goarch
			want[key] = "https://github.com/GoCodeAlone/workflow-plugin-migrations/releases/download/v" +
				manifest.Version + "/workflow-plugin-migrations-" + goos + "-" + goarch + ".tar.gz"
		}
	}

	got := make(map[string]string)
	for _, d := range manifest.Downloads {
		got[d.OS+"/"+d.Arch] = d.URL
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin.json downloads do not match GoReleaser main archive matrix\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestPlugin_GoReleaserManifestRewriteKeepsTagPrefix(t *testing.T) {
	cfg := readGoReleaserConfig(t)
	for _, hook := range cfg.Before.Hooks {
		if strings.Contains(hook, "/releases/download/") &&
			strings.Contains(hook, "/releases/download/v{{ .Version }}/") {
			return
		}
	}
	t.Fatalf("GoReleaser before hook must rewrite download URLs with the v tag prefix")
}

func TestPlugin_GoReleaserArchivesPackageContractsOnlyForMainPlugin(t *testing.T) {
	cfg := readGoReleaserConfig(t)

	mainArchive := cfg.findArchive(t, "workflow-plugin-migrations")
	if !mainArchive.hasFile("plugin.json") {
		t.Fatal("workflow-plugin-migrations archive does not package plugin.json")
	}
	if !mainArchive.hasFile("plugin.contracts.json") {
		t.Fatal("workflow-plugin-migrations archive does not package plugin.contracts.json")
	}

	atlasArchive := cfg.findArchive(t, "workflow-plugin-atlas-migrate")
	if atlasArchive.hasFile("plugin.contracts.json") {
		t.Fatal("workflow-plugin-atlas-migrate archive must not package main plugin.contracts.json")
	}
}

type contractDescriptor struct {
	Kind   string `json:"kind"`
	Type   string `json:"type"`
	Mode   string `json:"mode"`
	Config string `json:"config,omitempty"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
}

func expectedContractDescriptors() []contractDescriptor {
	return []contractDescriptor{
		{Kind: "module", Type: "database.migrations", Mode: "strict", Config: "workflow.plugins.migrations.MigrationsModuleConfig"},
		{Kind: "module", Type: "database.migration_driver", Mode: "strict", Config: "workflow.plugins.migrations.MigrationDriverConfig"},
		{Kind: "step", Type: "step.migrate_up", Mode: "strict", Input: "workflow.plugins.migrations.MigrateUpInput", Output: "workflow.plugins.migrations.MigrateUpOutput"},
		{Kind: "step", Type: "step.migrate_down", Mode: "strict", Input: "workflow.plugins.migrations.MigrateDownInput", Output: "workflow.plugins.migrations.MigrateDownOutput"},
		{Kind: "step", Type: "step.migrate_status", Mode: "strict", Input: "workflow.plugins.migrations.MigrateStatusInput", Output: "workflow.plugins.migrations.MigrateStatusOutput"},
		{Kind: "step", Type: "step.migrate_to", Mode: "strict", Input: "workflow.plugins.migrations.MigrateToInput", Output: "workflow.plugins.migrations.MigrateToOutput"},
	}
}

func sortedContracts(in []contractDescriptor) []contractDescriptor {
	out := append([]contractDescriptor(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Kind+":"+out[i].Type < out[j].Kind+":"+out[j].Type
	})
	return out
}

func canonicalStepSchemas(t *testing.T, schemas []*schema.StepSchema) map[string]any {
	t.Helper()
	out := make(map[string]any, len(schemas))
	for _, s := range schemas {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal step schema %q: %v", s.Type, err)
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatalf("canonicalize step schema %q: %v", s.Type, err)
		}
		out[s.Type] = v
	}
	return out
}

type pluginManifestFile struct {
	Version   string `json:"version"`
	Downloads []struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
		URL  string `json:"url"`
	} `json:"downloads"`
}

func readPluginManifest(t *testing.T) pluginManifestFile {
	t.Helper()
	data, err := os.ReadFile("../plugin.json")
	if err != nil {
		t.Fatalf("open plugin.json: %v", err)
	}
	var manifest pluginManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse plugin.json: %v", err)
	}
	return manifest
}

type goReleaserConfig struct {
	Before struct {
		Hooks []string `yaml:"hooks"`
	} `yaml:"before"`
	Builds   []goReleaserBuild   `yaml:"builds"`
	Archives []goReleaserArchive `yaml:"archives"`
}

type goReleaserBuild struct {
	ID     string   `yaml:"id"`
	Goos   []string `yaml:"goos"`
	Goarch []string `yaml:"goarch"`
}

type goReleaserArchive struct {
	ID    string   `yaml:"id"`
	Files []string `yaml:"files"`
}

func readGoReleaserConfig(t *testing.T) goReleaserConfig {
	t.Helper()
	data, err := os.ReadFile("../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("open .goreleaser.yaml: %v", err)
	}
	var cfg goReleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}
	return cfg
}

func (cfg goReleaserConfig) findBuild(t *testing.T, id string) goReleaserBuild {
	t.Helper()
	for _, build := range cfg.Builds {
		if build.ID == id {
			return build
		}
	}
	t.Fatalf("missing GoReleaser build %q", id)
	return goReleaserBuild{}
}

func (cfg goReleaserConfig) findArchive(t *testing.T, id string) goReleaserArchive {
	t.Helper()
	for _, archive := range cfg.Archives {
		if archive.ID == id {
			return archive
		}
	}
	t.Fatalf("missing GoReleaser archive %q", id)
	return goReleaserArchive{}
}

func (a goReleaserArchive) hasFile(name string) bool {
	for _, file := range a.Files {
		if file == name {
			return true
		}
	}
	return false
}
