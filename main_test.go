package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bomly-dev/bomly-cli/sdk"
)

func TestPackageManagerOtherSupport(t *testing.T) {
	d := &detector{}
	support, err := d.PackageManagerSupport(context.Background())
	if err != nil {
		t.Fatalf("PackageManagerSupport() error = %v", err)
	}
	if len(support) != 1 || support[0].PackageManager != sdk.PackageManagerOther {
		t.Fatalf("expected PackageManagerOther support, got %#v", support)
	}
}

func TestDetectPackageJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "bun-app",
  "version": "1.0.0",
  "dependencies": {
    "is-odd": "^3.0.1",
    "@types/node": "20.0.0"
  },
  "devDependencies": {
    "typescript": "~5.4.0"
  }
}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	result, err := (&detector{}).Detect(context.Background(), &sdk.DetectRequest{ProjectPath: dir})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	graph, err := result.ConsolidatedGraph()
	if err != nil {
		t.Fatalf("ConsolidatedGraph() error = %v", err)
	}
	if len(graph.Nodes()) != 4 {
		t.Fatalf("expected root plus three dependencies, got %d", len(graph.Nodes()))
	}
	node, ok := graph.Node("is-odd@3.0.1")
	if !ok {
		t.Fatalf("expected is-odd dependency")
	}
	if node.PURL != "pkg:npm/is-odd@3.0.1" {
		t.Fatalf("unexpected PURL %q", node.PURL)
	}
	if !node.HasScope(sdk.ScopeRuntime) {
		t.Fatalf("expected runtime scope")
	}
	dev, ok := graph.Node("typescript@5.4.0")
	if !ok {
		t.Fatalf("expected typescript dependency")
	}
	if !dev.HasScope(sdk.ScopeDevelopment) {
		t.Fatalf("expected development scope")
	}
}
