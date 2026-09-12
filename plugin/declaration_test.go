package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/bomly-dev/bomly-sdk"
)

// TestDeclaredManagersBelongToDeclaredEcosystems is the guard. A descriptor
// states two things that have to agree: which package managers the detector
// handles, and which ecosystems it produces packages for. The SDK already
// knows which ecosystem a package manager belongs to, so a manager whose
// ecosystem the descriptor does not declare is a declaration that contradicts
// itself.
//
// This detector carried exactly that contradiction: PackageManagerOther, whose
// ecosystem is EcosystemOther, while every node it mints is an npm package.
// Nothing caught it because npm answers to one registry, so the package URL
// came out right anyway -- sdk.BuildPackageURLFor refuses the same shape in an
// ecosystem that spans two, where (swift, cocoapods) builds an identity and
// (swift, other) builds nothing at all.
func TestDeclaredManagersBelongToDeclaredEcosystems(t *testing.T) {
	d := descriptor()
	declared := make(map[sdk.Ecosystem]bool, len(d.SupportedEcosystems))
	for _, ecosystem := range d.SupportedEcosystems {
		declared[ecosystem] = true
	}
	if len(d.SupportedManagers) == 0 {
		t.Fatal("descriptor declares no package managers")
	}
	for _, manager := range d.SupportedManagers {
		ecosystem := manager.Ecosystem()
		if ecosystem == sdk.EcosystemUnknown {
			t.Errorf("package manager %q is not one the SDK knows; it has no ecosystem to check against", manager)
			continue
		}
		if !declared[ecosystem] {
			t.Errorf("descriptor declares package manager %q, whose ecosystem is %q, but does not declare that ecosystem: %v",
				manager, ecosystem, d.SupportedEcosystems)
		}
	}
	// The discovery metadata and the descriptor must name the same managers,
	// or the detector is selected for one manager and advertised for another.
	for _, support := range support() {
		found := false
		for _, manager := range d.SupportedManagers {
			if support.PackageManager == manager {
				found = true
			}
		}
		if !found {
			t.Errorf("package-manager support declares %q, which the descriptor does not list: %v",
				support.PackageManager, d.SupportedManagers)
		}
	}
}

// TestDeclaredEcosystemsAreProduced is the other half: every ecosystem the
// descriptor claims must be one the detector actually emits. EcosystemOther
// was declared here and never produced.
func TestDeclaredEcosystemsAreProduced(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "bun-app",
  "version": "1.0.0",
  "dependencies": {"is-odd": "^3.0.1", "@types/node": "20.0.0"},
  "devDependencies": {"typescript": "~5.4.0"}
}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	result, err := newDetector(t).ResolveGraph(context.Background(), sdk.DetectionRequest{ProjectPath: dir})
	if err != nil {
		t.Fatalf("ResolveGraph() error = %v", err)
	}
	graph, err := result.ConsolidatedGraph()
	if err != nil {
		t.Fatalf("ConsolidatedGraph() error = %v", err)
	}

	d := descriptor()
	declaredEcosystems := make(map[sdk.Ecosystem]bool, len(d.SupportedEcosystems))
	for _, ecosystem := range d.SupportedEcosystems {
		declaredEcosystems[ecosystem] = false
	}
	declaredManagers := make(map[sdk.PackageManager]bool, len(d.SupportedManagers))
	for _, manager := range d.SupportedManagers {
		declaredManagers[manager] = false
	}

	nodes := graph.Nodes()
	if len(nodes) == 0 {
		t.Fatal("graph has no nodes")
	}
	for _, node := range nodes {
		coordinates, ok := sdk.NodeCoordinates(node)
		if !ok {
			t.Fatalf("node %q has no coordinates", node.NodeID())
		}
		ecosystem := coordinates.Ecosystem
		manager := coordinates.PackageManager
		if _, ok := declaredEcosystems[ecosystem]; !ok {
			t.Errorf("node %q carries ecosystem %q, which the descriptor does not declare", node.NodeID(), ecosystem)
		} else {
			declaredEcosystems[ecosystem] = true
		}
		if _, ok := declaredManagers[manager]; !ok {
			t.Errorf("node %q carries package manager %q, which the descriptor does not declare", node.NodeID(), manager)
		} else {
			declaredManagers[manager] = true
		}
		// The identity is what the declaration is ultimately about: bun
		// packages come from the npm registry.
		if purl := sdk.NodePURL(node); !strings.HasPrefix(purl, "pkg:npm/") {
			t.Errorf("node %q has package URL %q, want a pkg:npm identity", node.NodeID(), purl)
		}
	}
	for ecosystem, produced := range declaredEcosystems {
		if !produced {
			t.Errorf("descriptor declares ecosystem %q, but no node carries it", ecosystem)
		}
	}
	for manager, produced := range declaredManagers {
		if !produced {
			t.Errorf("descriptor declares package manager %q, but no node carries it", manager)
		}
	}
}

// TestPackageManagerComesFromTheSDK guards against the local re-spelling this
// change removed. sdk.PackageManager is string-backed so a plugin can name a
// manager the SDK has not grown yet; bun is not one of those, and a local
// sdk.PackageManager("bun") is a copy that stops agreeing with
// PackageManagerBun.Ecosystem() the moment either side moves.
func TestPackageManagerComesFromTheSDK(t *testing.T) {
	found := false
	for _, manager := range sdk.AllPackageManagers() {
		if manager == sdk.PackageManagerBun {
			found = true
		}
	}
	if !found {
		t.Fatal("sdk.AllPackageManagers() no longer lists bun; the descriptor needs revisiting")
	}
	if got := sdk.PackageManagerBun.Ecosystem(); got != sdk.EcosystemNPM {
		t.Fatalf("sdk.PackageManagerBun.Ecosystem() = %q, want %q", got, sdk.EcosystemNPM)
	}
	data, err := os.ReadFile("plugin.go")
	if err != nil {
		t.Fatalf("read plugin.go: %v", err)
	}
	if strings.Contains(string(data), `sdk.PackageManager("`) {
		t.Error("plugin.go mints a package-manager value by hand; use the SDK constant")
	}
}
