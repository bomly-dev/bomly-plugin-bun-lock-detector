# Bun Lock Detector Plugin

Example Bomly detector plugin for Bun projects. It intentionally registers `PackageManagerOther` and declares `bun.lock`, `bun.lockb`, and `package.json` as evidence patterns, which shows how a plugin can support a package manager before Bomly has a first-class SDK enum for it.

The implementation keeps the parser small for example purposes: it reads dependencies from `package.json` and emits npm PURLs while the detector itself is selected through the generic `other` package manager.

## Build and test

```bash
go test ./...
go build -o bin/bomly-plugin-bun-lock-detector .
```

On Windows, use `bin/bomly-plugin-bun-lock-detector.exe`.

## Install for local development

```bash
bomly plugin install ./bin/bomly-plugin-bun-lock-detector --dev
bomly plugin enable bomly.examples.detector.bun-lock
bomly scan --path ./some-bun-project --detectors bomly.examples.detector.bun-lock
```

## Install from an archive

```bash
bomly plugin install ./dist/bomly-plugin-bun-lock-detector_linux_amd64.tar.gz
bomly plugin enable bomly.examples.detector.bun-lock
```

Direct URL installs are also supported, but they must include either a checksum or the explicit insecure opt-out:

```bash
bomly plugin install https://example.internal/bomly-plugin-bun-lock-detector_linux_amd64.tar.gz \
  --checksum sha256:<digest>
```

## Install from a private GitHub Release

```bash
export BOMLY_GITHUB_TOKEN=<token-with-release-access>
bomly plugin install github:bomly-dev/bomly-plugin-bun-lock-detector@v0.1.0
bomly plugin enable bomly.examples.detector.bun-lock
```

`GITHUB_TOKEN`, `GH_TOKEN`, and `GITHUB_AUTH_TOKEN` are also accepted. The token is only attached to `github:owner/repo@tag` release metadata, checksum, and asset downloads.
