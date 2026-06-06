package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bomly-dev/bomly-cli/sdk"
)

const (
	pluginID      = "bomly.examples.detector.bun-lock"
	pluginVersion = "0.1.0"
)

type detector struct{}

type packageJSON struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

func (d *detector) Metadata(context.Context) (*sdk.PluginMetadata, error) {
	return &sdk.PluginMetadata{
		ID:               pluginID,
		Name:             "Bun Lock Detector",
		Version:          pluginVersion,
		Kind:             sdk.PluginKindDetector,
		PluginAPIVersion: sdk.PluginAPIVersion,
		Description:      "Example detector plugin for Bun projects that demonstrates PackageManagerOther support.",
		Homepage:         "https://github.com/bomly-dev/bomly-plugin-bun-lock-detector",
		License:          "Apache-2.0",
	}, nil
}

func (d *detector) Descriptor(context.Context) (*sdk.DetectorDescriptor, error) {
	return &sdk.DetectorDescriptor{
		Name:                pluginID,
		Enabled:             true,
		Origin:              sdk.ExternalOrigin,
		Technique:           sdk.LockfileTechnique,
		SupportedEcosystems: []sdk.Ecosystem{sdk.EcosystemOther, sdk.EcosystemNPM},
		SupportedManagers:   []sdk.PackageManager{sdk.PackageManagerOther},
		Capabilities:        []string{"dependency-detection", "package-manager-other-demo"},
	}, nil
}

func (d *detector) PackageManagerSupport(context.Context) ([]sdk.PackageManagerSupport, error) {
	return []sdk.PackageManagerSupport{
		sdk.Support(sdk.PackageManagerOther, "bun.lock", "bun.lockb", "package.json"),
	}, nil
}

func (d *detector) Ready(context.Context, *sdk.DetectRequest) (*sdk.ReadyResponse, error) {
	return &sdk.ReadyResponse{Ready: true}, nil
}

func (d *detector) Applicable(_ context.Context, req *sdk.DetectRequest) (*sdk.ApplicableResponse, error) {
	path := filepath.Join(req.ProjectPath, "package.json")
	if _, err := os.Stat(path); err == nil {
		return &sdk.ApplicableResponse{Applicable: true}, nil
	}
	return &sdk.ApplicableResponse{Applicable: false}, nil
}

func (d *detector) Detect(_ context.Context, req *sdk.DetectRequest) (*sdk.DetectResponse, error) {
	manifestPath := filepath.Join(req.ProjectPath, "package.json")
	manifest, err := readPackageJSON(manifestPath)
	if err != nil {
		return nil, err
	}
	graph := sdk.New()
	root := sdk.NewDependency(sdk.Dependency{
		Name:        firstNonEmpty(manifest.Name, filepath.Base(req.ProjectPath)),
		Version:     firstNonEmpty(manifest.Version, "0.0.0"),
		Ecosystem:   string(sdk.EcosystemNPM),
		BuildSystem: "bun",
		FoundBy:     pluginID,
	})
	if err := graph.AddNode(root); err != nil {
		return nil, err
	}
	for _, dep := range dependencies(manifest) {
		node := dependencyNode(dep)
		if err := graph.AddNode(node); err != nil {
			return nil, err
		}
		if err := graph.AddEdge(root.ID, node.ID); err != nil {
			return nil, err
		}
	}
	return &sdk.DetectResponse{
		SubprojectInfo:      req.Subproject,
		RootExecutionTarget: req.ExecutionTarget,
		DetectorName:        pluginID,
		Origin:              sdk.ExternalOrigin,
		Technique:           sdk.LockfileTechnique,
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
		out = append(out, dependencySpec{Name: name, Version: version, Scope: scope})
	}
	return out
}

func dependencyNode(dep dependencySpec) *sdk.Dependency {
	namespace, name := splitNPMName(dep.Name)
	version := cleanVersion(dep.Version)
	purl := sdk.BuildPackageURL("npm", namespace, name, version)
	return sdk.NewDependency(sdk.Dependency{
		Name:        name,
		Org:         namespace,
		Version:     version,
		PURL:        purl,
		PackageRef:  purl,
		Ecosystem:   string(sdk.EcosystemNPM),
		BuildSystem: "bun",
		Scopes:      sdk.ScopesOf(dep.Scope),
		FoundBy:     pluginID,
	})
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

func main() {
	sdk.ServeDetector(&detector{})
}
