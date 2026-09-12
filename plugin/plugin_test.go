package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sdk "github.com/bomly-dev/bomly-sdk"
	"github.com/bomly-dev/bomly-sdk/conformance"
)

func newDetector(t *testing.T) sdk.Detector {
	t.Helper()
	detector, err := Module().Detector.New(context.Background(), nil)
	if err != nil {
		t.Fatalf("construct detector: %v", err)
	}
	return detector
}

// TestBunPackageManagerSupport pins the discovery declaration. The detector
// reads Bun projects, and bun is a first-class SDK package manager -- it used
// to declare PackageManagerOther, which put it in the generic bucket while it
// minted npm identities.
func TestBunPackageManagerSupport(t *testing.T) {
	support := newDetector(t).PackageManagerSupport()
	if len(support) != 1 || support[0].PackageManager != sdk.PackageManagerBun {
		t.Fatalf("expected PackageManagerBun support, got %#v", support)
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

	result, err := newDetector(t).ResolveGraph(context.Background(), sdk.DetectionRequest{ProjectPath: dir})
	if err != nil {
		t.Fatalf("ResolveGraph() error = %v", err)
	}
	graph, err := result.ConsolidatedGraph()
	if err != nil {
		t.Fatalf("ConsolidatedGraph() error = %v", err)
	}
	if len(graph.Nodes()) != 4 {
		t.Fatalf("expected root plus three dependencies, got %d", len(graph.Nodes()))
	}

	// The scanned project is a module node; the three package.json entries
	// are dependency nodes. Ownership is the kind, not a flag.
	if got := len(graph.ModuleNodes()); got != 1 {
		t.Fatalf("expected exactly one module node for the scanned project, got %d", got)
	}
	if got := len(graph.DependencyNodes()); got != 3 {
		t.Fatalf("expected three dependency nodes, got %d", got)
	}
	root := graph.ModuleNodes()[0]
	if root.NodeID() != "module:package.json#pkg:npm/bun-app@1.0.0" {
		t.Fatalf("unexpected root module identity %q", root.NodeID())
	}

	// A dependency node is keyed by its canonical package URL.
	node, ok := graph.DependencyNode("pkg:npm/is-odd@3.0.1")
	if !ok {
		t.Fatalf("expected is-odd dependency keyed by its canonical package URL, got %#v", nodeIDs(graph))
	}
	if node.Coordinates.PURL != "pkg:npm/is-odd@3.0.1" {
		t.Fatalf("unexpected PURL %q", node.Coordinates.PURL)
	}
	if node.PackageRef != node.NodeID() {
		t.Fatalf("PackageRef %q must be the node identity %q", node.PackageRef, node.NodeID())
	}
	if node.FoundBy != Name {
		t.Fatalf("FoundBy = %q, want %q", node.FoundBy, Name)
	}
	if !node.HasScope(sdk.ScopeRuntime) {
		t.Fatalf("expected runtime scope")
	}

	// An npm scope becomes the package URL namespace, percent-encoded.
	scoped, ok := graph.DependencyNode("pkg:npm/%40types/node@20.0.0")
	if !ok {
		t.Fatalf("expected @types/node dependency, got %#v", nodeIDs(graph))
	}
	if scoped.DisplayName() != "@types/node" {
		t.Fatalf("display name = %q, want @types/node", scoped.DisplayName())
	}

	dev, ok := graph.DependencyNode("pkg:npm/typescript@5.4.0")
	if !ok {
		t.Fatalf("expected typescript dependency, got %#v", nodeIDs(graph))
	}
	if !dev.HasScope(sdk.ScopeDevelopment) {
		t.Fatalf("expected development scope")
	}

	// Every dependency hangs off the root module.
	for _, dep := range graph.DependencyNodes() {
		parents, err := graph.Dependents(dep.NodeID())
		if err != nil {
			t.Fatalf("Dependents(%q) error = %v", dep.NodeID(), err)
		}
		if len(parents) != 1 || parents[0].NodeID() != root.NodeID() {
			t.Fatalf("dependency %q is not attached to the root module, parents = %#v", dep.NodeID(), parents)
		}
	}
}

func nodeIDs(graph *sdk.Graph) []string {
	out := make([]string, 0, len(graph.Nodes()))
	for _, node := range graph.Nodes() {
		out = append(out, node.NodeID())
	}
	return out
}

// A package listed under two dependency blocks is one node carrying both
// scopes. The old graph errored on the second insert of an identity it
// already held; insertion folds by identity now.
func TestDuplicateDependencyFoldsIntoOneNode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "bun-app",
  "version": "1.0.0",
  "dependencies": { "typescript": "5.4.0" },
  "devDependencies": { "typescript": "5.4.0" }
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
	if got := len(graph.DependencyNodes()); got != 1 {
		t.Fatalf("expected one folded dependency node, got %d (%#v)", got, nodeIDs(graph))
	}
	node := graph.DependencyNodes()[0]
	if !node.HasScope(sdk.ScopeRuntime) || !node.HasScope(sdk.ScopeDevelopment) {
		t.Fatalf("folded node must carry both scopes, got %#v", node.Scopes)
	}
}

// A detector declares its module in its own working-directory coordinate
// space: it does not know where it was mounted, and Bomly's consolidation
// stage rebases every module's declaring path onto the repository root. The
// declaring path must therefore stay the bare manifest name even when the
// request names a subproject, or the host would rebase an already-rebased
// path.
func TestModuleDeclaringPathIsNotSubprojectPrefixed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"bun-app","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	result, err := newDetector(t).ResolveGraph(context.Background(), sdk.DetectionRequest{
		ProjectPath: dir,
		Subproject:  sdk.Subproject{RelativePath: "packages/api"},
	})
	if err != nil {
		t.Fatalf("ResolveGraph() error = %v", err)
	}
	graph, err := result.ConsolidatedGraph()
	if err != nil {
		t.Fatalf("ConsolidatedGraph() error = %v", err)
	}
	root := graph.ModuleNodes()[0]
	if root.DeclaringManifestPath != "package.json" {
		t.Fatalf("declaring manifest path = %q, want the un-prefixed %q", root.DeclaringManifestPath, "package.json")
	}
	if root.NodeID() != "module:package.json#pkg:npm/bun-app@1.0.0" {
		t.Fatalf("unexpected module identity %q", root.NodeID())
	}
}

// A dependency entry whose key names nothing is dropped, not fatal.
func TestBlankDependencyNameIsDropped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "bun-app",
  "version": "1.0.0",
  "dependencies": { "": "1.0.0", "is-odd": "3.0.1" }
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
	if got := len(graph.DependencyNodes()); got != 1 {
		t.Fatalf("expected the blank entry to be dropped, got %#v", nodeIDs(graph))
	}
}

func TestApplicableRequiresPackageJSON(t *testing.T) {
	detector := newDetector(t)
	empty := t.TempDir()
	applicable, err := detector.Applicable(context.Background(), sdk.DetectionRequest{ProjectPath: empty})
	if err != nil {
		t.Fatalf("Applicable() error = %v", err)
	}
	if applicable {
		t.Fatal("expected not applicable without package.json")
	}

	withManifest := t.TempDir()
	if err := os.WriteFile(filepath.Join(withManifest, "package.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	applicable, err = detector.Applicable(context.Background(), sdk.DetectionRequest{ProjectPath: withManifest})
	if err != nil {
		t.Fatalf("Applicable() error = %v", err)
	}
	if !applicable {
		t.Fatal("expected applicable with package.json")
	}
}

// A directory named package.json is not a manifest and must not make the
// detector applicable.
func TestApplicableRejectsPackageJSONDirectory(t *testing.T) {
	detector := newDetector(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "package.json"), 0o755); err != nil {
		t.Fatalf("mkdir package.json: %v", err)
	}
	applicable, err := detector.Applicable(context.Background(), sdk.DetectionRequest{ProjectPath: root})
	if err != nil {
		t.Fatalf("Applicable() error = %v", err)
	}
	if applicable {
		t.Fatal("expected not applicable when package.json is a directory")
	}
}

// A stat failure other than "does not exist" (here ENOTDIR: the project path
// is a regular file, so package.json cannot be statted beneath it) must
// propagate instead of silently reporting "not applicable".
func TestApplicablePropagatesStatErrors(t *testing.T) {
	detector := newDetector(t)
	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("plain file"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	applicable, err := detector.Applicable(context.Background(), sdk.DetectionRequest{ProjectPath: filePath})
	if err == nil {
		t.Fatal("expected a stat error to propagate")
	}
	if applicable {
		t.Fatal("expected not applicable on stat error")
	}
}

// TestConformance runs the SDK conformance suite against the module,
// including the bomly-plugin.json identity cross-check.
func TestConformance(t *testing.T) {
	conformance.Test(t, conformance.Config{
		Module:       Module(),
		ManifestPath: filepath.Join("..", "bomly-plugin.json"),
	})
}

// TestProbeBinary builds the real plugin binary and probes it over the
// managed HashiCorp gRPC transport, asserting the served descriptor equals
// the in-process one.
func TestProbeBinary(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available; skipping managed-transport probe")
	}
	binaryPath := filepath.Join(t.TempDir(), "bomly-plugin-bun-lock-detector")
	build := exec.Command(goBinary, "build", "-o", binaryPath, "./cmd/bomly-plugin-bun-lock-detector")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin binary: %v\n%s", err, output)
	}
	conformance.ProbeBinary(t, binaryPath, conformance.WithModule(Module()))
}
