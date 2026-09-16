// Package plugin implements the Bun lock detector: an example Bomly
// DETECTOR that resolves a dependency graph for Bun projects from
// package.json.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bomly-dev/bomly-sdk/detectorkit"

	"github.com/bomly-dev/bomly-sdk/model"
	sdkplugin "github.com/bomly-dev/bomly-sdk/plugin"
)

// Name is the plugin's identity. It MUST equal the "id" field in
// bomly-plugin.json — Bomly refuses to load a plugin whose manifest id and
// runtime descriptor name disagree.
const Name = "bomly.examples.detector.bun-lock"

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
	sdkplugin.BaseDetector
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
func descriptor() sdkplugin.DetectorDescriptor {
	return sdkplugin.DetectorDescriptor{
		Name:        Name,
		DisplayName: "Bun Lock Detector",
		Aliases:     []string{"bun", "bun-lock"},
		Technique:   sdkplugin.LockfileTechnique,
		// Bun is a first-class SDK package manager whose ecosystem is npm:
		// its packages come from the npm registry and carry pkg:npm
		// identities, which is exactly what this detector mints. Declaring
		// PackageManagerOther instead said the detector handled a manager
		// belonging to EcosystemOther while minting npm identities -- a
		// mismatch nothing catches today only because npm is unambiguous.
		// sdk.BuildPackageURLFor refuses that shape in an ecosystem that
		// spans two registries: (swift, cocoapods) builds a package URL and
		// (swift, other) builds nothing.
		SupportedEcosystems: []model.Ecosystem{model.EcosystemNPM},
		SupportedManagers:   []model.PackageManager{model.PackageManagerBun},
		Tags:                []string{"dependency-detection", "bun"},
	}
}

// support is the detector's package-manager discovery metadata.
func support() []sdkplugin.PackageManagerSupport {
	return []sdkplugin.PackageManagerSupport{
		sdkplugin.Support(model.PackageManagerBun, "bun.lock", "bun.lockb", "package.json"),
	}
}

// Descriptor identifies the detector to Bomly.
func (d *Detector) Descriptor() sdkplugin.DetectorDescriptor { return descriptor() }

// PackageManagerSupport reports package-manager discovery metadata so Bomly
// can include the detector in subproject discovery and scan planning.
func (d *Detector) PackageManagerSupport() []sdkplugin.PackageManagerSupport { return support() }

// Applicable reports whether the project root carries a package.json file.
// A missing manifest is a normal "not applicable"; any other stat failure
// (permissions, I/O) propagates so the host can report it instead of
// silently skipping the project. A directory named package.json does not
// count as a manifest.
func (d *Detector) Applicable(_ context.Context, req sdkplugin.DetectionRequest) (bool, error) {
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
func (d *Detector) ResolveGraph(_ context.Context, req sdkplugin.DetectionRequest) (sdkplugin.DetectionResult, error) {
	manifestPath := filepath.Join(req.ProjectPath, "package.json")
	manifest, err := readPackageJSON(manifestPath)
	if err != nil {
		return sdkplugin.DetectionResult{}, err
	}
	graph := model.New()
	// The scanned project's own package is a module node, not a dependency
	// node. Ownership is the node kind now; the application package type it
	// used to carry is not sufficient on its own, because an
	// application-typed *import* is still a consumed package.
	root, err := model.NewModuleNode(rootManifestName, model.Coordinates{
		Name:           firstNonEmpty(manifest.Name, filepath.Base(req.ProjectPath)),
		Version:        firstNonEmpty(manifest.Version, "0.0.0"),
		Ecosystem:      model.EcosystemNPM,
		PackageManager: model.PackageManagerBun,
		Type:           model.PackageTypeApplication,
	})
	if err != nil {
		return sdkplugin.DetectionResult{}, fmt.Errorf("%s: root module node: %w", Name, err)
	}
	if _, err := detectorkit.EnsureNode(graph, root); err != nil {
		return sdkplugin.DetectionResult{}, err
	}
	for _, dep := range dependencies(manifest) {
		node, err := dependencyNode(dep)
		if err != nil {
			return sdkplugin.DetectionResult{}, err
		}
		// EnsureNode, not AddNode: identity is the canonical package URL, so
		// one package listed under two dependency blocks is one node, and
		// its scopes union onto the survivor instead of the second insert
		// failing.
		inserted, err := detectorkit.EnsureNode(graph, node)
		if err != nil {
			return sdkplugin.DetectionResult{}, err
		}
		if err := graph.AddEdge(root.NodeID(), inserted.NodeID()); err != nil {
			return sdkplugin.DetectionResult{}, err
		}
	}
	return sdkplugin.DetectionResult{
		SubprojectInfo:      req.Subproject,
		RootExecutionTarget: req.ExecutionTarget,
		Graphs: &model.GraphContainer{
			Entries: []model.GraphEntry{{
				Manifest: model.ManifestMetadata{
					Path: manifestPath,
					Kind: model.ManifestKind("package.json"),
				},
				Graph: graph,
			}},
		},
	}, nil
}

type dependencySpec struct {
	Name    string
	Version string
	Scope   model.Scope
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
	out = appendDeps(out, manifest.Dependencies, model.ScopeRuntime)
	out = appendDeps(out, manifest.OptionalDependencies, model.ScopeRuntime)
	out = appendDeps(out, manifest.PeerDependencies, model.ScopeRuntime)
	out = appendDeps(out, manifest.DevDependencies, model.ScopeDevelopment)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func appendDeps(out []dependencySpec, deps map[string]string, scope model.Scope) []dependencySpec {
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
func dependencyNode(dep dependencySpec) (*model.DependencyNode, error) {
	namespace, name := splitNPMName(dep.Name)
	node, err := model.NewDependencyNodeFrom(model.DependencyNode{
		Coordinates: model.Coordinates{
			Name:           name,
			Org:            namespace,
			Version:        cleanVersion(dep.Version),
			Ecosystem:      model.EcosystemNPM,
			PackageManager: model.PackageManagerBun,
		},
		Scopes:  model.ScopesOf(dep.Scope),
		FoundBy: Name,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: dependency %q: %w", Name, dep.Name, err)
	}
	return node, nil
}

func splitNPMName(value string) (string, string) {
	value = strings.TrimSpace(value)
	if after, ok := strings.CutPrefix(value, "@"); ok {
		parts := strings.SplitN(after, "/", 2)
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
func Module() sdkplugin.Module {
	return sdkplugin.Module{
		Kind: sdkplugin.PluginKindDetector,
		Detector: &sdkplugin.DetectorModule{
			Descriptor: descriptor(),
			Support:    support(),
			New: func(context.Context, sdkplugin.HostContext) (sdkplugin.Detector, error) {
				return &Detector{}, nil
			},
		},
	}
}
