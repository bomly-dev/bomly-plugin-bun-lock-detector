// Package plugin implements the Bun lock detector: an example Bomly
// DETECTOR that resolves a dependency graph for Bun projects from
// package.json, demonstrating PackageManagerOther support.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bomly-dev/bomly-sdk"
	"github.com/bomly-dev/bomly-sdk/detectorkit"
)

// Name is the plugin's identity. It MUST equal the "id" field in
// bomly-plugin.json — Bomly refuses to load a plugin whose manifest id and
// runtime descriptor name disagree.
const Name = "bomly.examples.detector.bun-lock"

const bunPM = sdk.PackageManager("bun")

// rootManifestName is the declaring manifest path of the root module node,
// written in this detector's own working-directory coordinate space.
//
// A module node's identity is "module:<declaring path>#<purl>", and the path
// must be repository-relative -- a raw checkout path would make the identity
// vary from machine to machine, and the constructor rejects it outright. It is
// deliberately NOT prefixed with Subproject.RelativePath here: a detector does
// not know where it was mounted, so Bomly's consolidation stage rebases every
// module's declaring path onto the repository root itself. Prefixing it here
// would duplicate work the host owns.
const rootManifestName = "package.json"

// Detector is the component. Embedding sdk.BaseDetector supplies the default
// Ready implementation (always ready); Applicable is overridden to require a
// package.json in the project root.
type Detector struct {
	sdk.BaseDetector
}

type packageJSON struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

// descriptor is the detector's static registration data.
func descriptor() sdk.DetectorDescriptor {
	return sdk.DetectorDescriptor{
		Name:                Name,
		DisplayName:         "Bun Lock Detector",
		Aliases:             []string{"bun", "bun-lock"},
		Technique:           sdk.LockfileTechnique,
		SupportedEcosystems: []sdk.Ecosystem{sdk.EcosystemOther, sdk.EcosystemNPM},
		SupportedManagers:   []sdk.PackageManager{sdk.PackageManagerOther},
		Tags:                []string{"dependency-detection", "package-manager-other-demo"},
	}
}

// support is the detector's package-manager discovery metadata.
func support() []sdk.PackageManagerSupport {
	return []sdk.PackageManagerSupport{
		sdk.Support(sdk.PackageManagerOther, "bun.lock", "bun.lockb", "package.json"),
	}
}

// Descriptor identifies the detector to Bomly.
func (d *Detector) Descriptor() sdk.DetectorDescriptor { return descriptor() }

// PackageManagerSupport reports package-manager discovery metadata so Bomly
// can include the detector in subproject discovery and scan planning.
func (d *Detector) PackageManagerSupport() []sdk.PackageManagerSupport { return support() }

// Applicable reports whether the project root carries a package.json file.
// A missing manifest is a normal "not applicable"; any other stat failure
// (permissions, I/O) propagates so the host can report it instead of
// silently skipping the project. A directory named package.json does not
// count as a manifest.
func (d *Detector) Applicable(_ context.Context, req sdk.DetectionRequest) (bool, error) {
	path := filepath.Join(req.ProjectPath, "package.json")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return !info.IsDir(), nil
}

// ResolveGraph resolves the Bun project's dependency graph from package.json.
func (d *Detector) ResolveGraph(_ context.Context, req sdk.DetectionRequest) (sdk.DetectionResult, error) {
	manifestPath := filepath.Join(req.ProjectPath, "package.json")
	manifest, err := readPackageJSON(manifestPath)
	if err != nil {
		return sdk.DetectionResult{}, err
	}
	graph := sdk.New()
	// The scanned project's own package is a module node, not a dependency
	// node. Ownership is the node kind now; the application package type it
	// used to carry is not sufficient on its own, because an
	// application-typed *import* is still a consumed package.
	root, err := sdk.NewModuleNode(rootManifestName, sdk.Coordinates{
		Name:           firstNonEmpty(manifest.Name, filepath.Base(req.ProjectPath)),
		Version:        firstNonEmpty(manifest.Version, "0.0.0"),
		Ecosystem:      sdk.EcosystemNPM,
		PackageManager: bunPM,
		Type:           sdk.PackageTypeApplication,
	})
	if err != nil {
		return sdk.DetectionResult{}, fmt.Errorf("%s: root module node: %w", Name, err)
	}
	if _, err := detectorkit.EnsureNode(graph, root); err != nil {
		return sdk.DetectionResult{}, err
	}
	for _, dep := range dependencies(manifest) {
		node, err := dependencyNode(dep)
		if err != nil {
			return sdk.DetectionResult{}, err
		}
		// EnsureNode, not AddNode: identity is the canonical package URL, so
		// one package listed under two dependency blocks is one node, and
		// its scopes union onto the survivor instead of the second insert
		// failing.
		inserted, err := detectorkit.EnsureNode(graph, node)
		if err != nil {
			return sdk.DetectionResult{}, err
		}
		if err := graph.AddEdge(root.NodeID(), inserted.NodeID()); err != nil {
			return sdk.DetectionResult{}, err
		}
	}
	return sdk.DetectionResult{
		SubprojectInfo:      req.Subproject,
		RootExecutionTarget: req.ExecutionTarget,
		Graphs: &sdk.GraphContainer{
			Entries: []sdk.GraphEntry{{
				Manifest: sdk.ManifestMetadata{
					Path: manifestPath,
					Kind: sdk.ManifestKind("package.json"),
				},
				Graph: graph,
			}},
		},
	}, nil
}

type dependencySpec struct {
	Name    string
	Version string
	Scope   sdk.Scope
}

func readPackageJSON(path string) (packageJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return packageJSON{}, fmt.Errorf("read package.json: %w", err)
	}
	var manifest packageJSON
	if err := json.Unmarshal(data, &manifest); err != nil {
		return packageJSON{}, fmt.Errorf("decode package.json: %w", err)
	}
	return manifest, nil
}

func dependencies(manifest packageJSON) []dependencySpec {
	var out []dependencySpec
	out = appendDeps(out, manifest.Dependencies, sdk.ScopeRuntime)
	out = appendDeps(out, manifest.OptionalDependencies, sdk.ScopeRuntime)
	out = appendDeps(out, manifest.PeerDependencies, sdk.ScopeRuntime)
	out = appendDeps(out, manifest.DevDependencies, sdk.ScopeDevelopment)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func appendDeps(out []dependencySpec, deps map[string]string, scope sdk.Scope) []dependencySpec {
	for name, version := range deps {
		// A blank key is not a package. It used to produce a node with an
		// empty identity; node construction now refuses one, and refusing
		// the whole manifest over a malformed entry would be worse than
		// dropping the entry that names nothing.
		if strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, dependencySpec{Name: name, Version: version, Scope: scope})
	}
	return out
}

// dependencyNode builds one dependency node from a package.json entry.
//
// The package URL is no longer assembled here. A node's identity is minted by
// the constructor from its coordinates and validated against the purl
// specification, so a hand-built URL would only be a second opinion about the
// same thing — and PackageRef is derived from that identity rather than set.
// Construction is through the prototype constructor so every field this
// detector states travels with the node: building the node first and
// assigning afterwards is how sibling npm-family detectors silently dropped
// what they had detected.
func dependencyNode(dep dependencySpec) (*sdk.DependencyNode, error) {
	namespace, name := splitNPMName(dep.Name)
	node, err := sdk.NewDependencyNodeFrom(sdk.DependencyNode{
		Coordinates: sdk.Coordinates{
			Name:           name,
			Org:            namespace,
			Version:        cleanVersion(dep.Version),
			Ecosystem:      sdk.EcosystemNPM,
			PackageManager: bunPM,
		},
		Scopes:  sdk.ScopesOf(dep.Scope),
		FoundBy: Name,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: dependency %q: %w", Name, dep.Name, err)
	}
	return node, nil
}

func splitNPMName(value string) (string, string) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@") {
		parts := strings.SplitN(strings.TrimPrefix(value, "@"), "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}
	return "", value
}

func cleanVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "^~<>= ")
	if value == "" || strings.ContainsAny(value, " *xX|") {
		return "0.0.0"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Module packages the detector for both execution modes: Bomly can embed it
// in-process or serve it as a managed plugin subprocess (see
// cmd/bomly-plugin-bun-lock-detector).
func Module() sdk.Module {
	return sdk.Module{
		Kind: sdk.PluginKindDetector,
		Detector: &sdk.DetectorModule{
			Descriptor: descriptor(),
			Support:    support(),
			New: func(context.Context, sdk.HostContext) (sdk.Detector, error) {
				return &Detector{}, nil
			},
		},
	}
}
