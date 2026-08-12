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

func TestPackageManagerOtherSupport(t *testing.T) {
	support := newDetector(t).PackageManagerSupport()
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
