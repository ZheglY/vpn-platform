package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSBOMAndReleaseDetectTampering(t *testing.T) {
	output := t.TempDir()
	sbom := filepath.Join(output, "service.spdx.json")
	writeTestJSON(t, sbom, map[string]any{
		"spdxVersion":       "SPDX-2.3",
		"name":              "service",
		"documentNamespace": "https://example.invalid/spdx/service",
		"creationInfo":      map[string]any{"created": "2026-07-26T00:00:00Z"},
		"packages": []any{map[string]any{
			"name": "sha256", "versionInfo": strings.Repeat("b", 64),
			"primaryPackagePurpose": "CONTAINER",
		}},
	})
	digest, err := hashFile(sbom)
	if err != nil {
		t.Fatal(err)
	}
	scan := filepath.Join(output, "service.trivy.json")
	writeTestJSON(t, scan, map[string]any{
		"SchemaVersion": 2,
		"ArtifactName":  "sha256:" + strings.Repeat("b", 64),
		"ArtifactType":  "container_image",
		"Metadata":      map[string]any{"ImageID": "sha256:" + strings.Repeat("b", 64)},
		"Results":       []any{},
	})
	scanDigest, err := hashFile(scan)
	if err != nil {
		t.Fatal(err)
	}
	manifest := releaseManifest{
		FormatVersion: 1, SourceRepository: "https://github.com/example/project",
		SourceCommit: strings.Repeat("a", 40), SourceDate: "2026-07-26T00:00:00Z",
		Version: "1.0.0", Toolchain: toolchain{
			SyftImage:  "anchore/syft:v1@sha256:" + strings.Repeat("c", 64),
			TrivyImage: "aquasec/trivy:v1@sha256:" + strings.Repeat("d", 64),
		},
		Images: []releaseImage{{
			Name: "service", Dockerfile: "service/Dockerfile",
			Platform: "linux/amd64", Tag: "vpn-service/service:git-aaaaaaaaaaaa",
			ImageID: "sha256:" + strings.Repeat("b", 64),
			SBOM:    "service.spdx.json", SBOMSHA256: digest,
			Scan: "service.trivy.json", ScanSHA256: scanDigest,
		}},
	}
	if err := writeJSON(filepath.Join(output, manifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := writeChecksums(output, manifest); err != nil {
		t.Fatal(err)
	}
	spec := inventory{
		FormatVersion: 1, SourceRepository: manifest.SourceRepository, Toolchain: manifest.Toolchain,
		Images: []image{{Name: "service", Dockerfile: "service/Dockerfile"}},
	}
	if err := verifyRelease(output, spec); err != nil {
		t.Fatalf("expected valid release: %v", err)
	}
	manifest.Images[0].SBOMSHA256 = strings.Repeat("0", 64)
	rewriteReleaseMetadata(t, output, manifest)
	if err := verifyRelease(output, spec); err == nil || !strings.Contains(err.Error(), "SBOM checksum mismatch") {
		t.Fatalf("expected wrong manifest checksum to be rejected, got %v", err)
	}
	manifest.Images[0].SBOMSHA256 = digest
	manifest.Version = "../bad"
	rewriteReleaseMetadata(t, output, manifest)
	if err := verifyRelease(output, spec); err == nil || !strings.Contains(err.Error(), "metadata is invalid") {
		t.Fatalf("expected bad release version to be rejected, got %v", err)
	}
	manifest.Version = "1.0.0"
	rewriteReleaseMetadata(t, output, manifest)
	wrongSubject := filepath.Join(output, "wrong-subject.spdx.json")
	writeTestJSON(t, wrongSubject, map[string]any{
		"spdxVersion":       "SPDX-2.3",
		"name":              "service",
		"documentNamespace": "https://example.invalid/spdx/service",
		"creationInfo":      map[string]any{"created": "2026-07-26T00:00:00Z"},
		"packages": []any{map[string]any{
			"name": "sha256", "versionInfo": strings.Repeat("e", 64),
			"primaryPackagePurpose": "CONTAINER",
		}},
	})
	if err := validateSBOM(wrongSubject, manifest.Images[0].ImageID); err == nil {
		t.Fatal("expected SBOM for another image to be rejected")
	}
	if err := os.Remove(wrongSubject); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(output, "unchecksummed.txt")
	if err := os.WriteFile(extra, []byte("not bound\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRelease(output, spec); err == nil {
		t.Fatal("expected unchecksummed artifact to be rejected")
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sbom, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRelease(output, spec); err == nil {
		t.Fatal("expected tampered SBOM to be rejected")
	}
}

func TestVerifyReleaseRejectsMissingManifest(t *testing.T) {
	if err := verifyRelease(t.TempDir(), inventory{}); err == nil || !strings.Contains(err.Error(), "read release manifest") {
		t.Fatalf("expected missing manifest to be rejected, got %v", err)
	}
}

func TestRepositoryReleaseInventoryIsComplete(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join(workingDirectory, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repositoryRoot); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(workingDirectory) }()
	spec, err := loadInventory("deploy/release/images.json")
	if err != nil {
		t.Fatalf("load release inventory: %v", err)
	}
	if len(spec.Images) != 19 {
		t.Fatalf("release inventory has %d images, want 19", len(spec.Images))
	}
	foundMigrate := false
	for _, item := range spec.Images {
		foundMigrate = foundMigrate || item.Name == "migrate"
	}
	if !foundMigrate {
		t.Fatal("release inventory is missing migrate")
	}
	runner := &recordingRunner{}
	if err := runLocalBuild([]string{"--inventory", "deploy/release/images.json"}, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 19 || !runner.hasImage("vpn-service/migrate:local") {
		t.Fatalf("local build calls = %d, migrate = %t", len(runner.calls), runner.hasImage("vpn-service/migrate:local"))
	}
	runner.calls = nil
	if err := runLocalScan([]string{"--inventory", "deploy/release/images.json"}, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 19 || !runner.hasImage("vpn-service/migrate:local") || !runner.hasArgument("--vex") {
		t.Fatalf("local scan calls = %d, migrate = %t, vex = %t", len(runner.calls), runner.hasImage("vpn-service/migrate:local"), runner.hasArgument("--vex"))
	}
}

func TestLoadInventoryRejectsUnclassifiedDockerfile(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"services/covered", "services/unclassified", "tools", "deploy/observability"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"services/covered/Dockerfile", "services/unclassified/Dockerfile"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inventoryPath := filepath.Join(root, "images.json")
	writeTestJSON(t, inventoryPath, inventory{
		FormatVersion: 1, SourceRepository: "https://github.com/example/project",
		Toolchain: toolchain{
			SyftImage:  "anchore/syft:v1@sha256:" + strings.Repeat("a", 64),
			TrivyImage: "aquasec/trivy:v1@sha256:" + strings.Repeat("b", 64),
		},
		Images: []image{{Name: "covered", Dockerfile: "services/covered/Dockerfile"}},
	})
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(workingDirectory) }()
	if _, err := loadInventory(inventoryPath); err == nil {
		t.Fatal("expected unclassified Dockerfile to be rejected")
	}
}

func TestLoadInventoryRejectsFloatingToolImage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "images.json")
	writeTestJSON(t, path, inventory{
		FormatVersion: 1, SourceRepository: "https://github.com/example/project",
		Toolchain: toolchain{
			SyftImage:  "anchore/syft:latest",
			TrivyImage: "aquasec/trivy:1@sha256:" + strings.Repeat("a", 64),
		},
		Images: []image{{Name: "service", Dockerfile: "Dockerfile"}},
	})
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(workingDirectory) }()
	if _, err := loadInventory(path); err == nil {
		t.Fatal("expected floating Syft image to be rejected")
	}
}

func TestPinnedImageRequiresDigest(t *testing.T) {
	if !pinnedImage("example/tool:v1@sha256:" + strings.Repeat("c", 64)) {
		t.Fatal("expected digest-pinned image")
	}
	for _, value := range []string{"example/tool:v1", "example/tool@sha256:short", "@sha256:" + strings.Repeat("c", 64)} {
		if pinnedImage(value) {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestVersionRejectsShellAndPathCharacters(t *testing.T) {
	for _, value := range []string{"1.2.3", "v1.2.3-rc.1", "git_abcdef"} {
		if !versionPattern.MatchString(value) {
			t.Fatalf("expected version %q to be accepted", value)
		}
	}
	for _, value := range []string{"", "../release", "1.0;echo", "1.0 $(id)", strings.Repeat("a", 65)} {
		if versionPattern.MatchString(value) {
			t.Fatalf("expected version %q to be rejected", value)
		}
	}
}

func TestInventoryDecoderRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	inventoryPath := filepath.Join(root, "images.json")
	body := `{"format_version":1,"source_repository":"https://github.com/example/project","toolchain":{},"images":[],"unknown":true}`
	if err := os.WriteFile(inventoryPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadInventory(inventoryPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("loadInventory() error = %v, want unknown-field rejection", err)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func rewriteReleaseMetadata(t *testing.T, output string, manifest releaseManifest) {
	t.Helper()
	for _, name := range []string{manifestFilename, "SHA256SUMS"} {
		if err := os.Remove(filepath.Join(output, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(filepath.Join(output, manifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := writeChecksums(output, manifest); err != nil {
		t.Fatal(err)
	}
}

type recordedCall struct {
	name string
	args []string
}

type recordingRunner struct {
	calls []recordedCall
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string, _ io.Writer) error {
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...)})
	return nil
}

func (*recordingRunner) Output(context.Context, string, ...string) (string, error) {
	return "", nil
}

func (r *recordingRunner) hasImage(image string) bool {
	return r.hasArgument(image)
}

func (r *recordingRunner) hasArgument(argument string) bool {
	for _, call := range r.calls {
		for _, item := range call.args {
			if item == argument {
				return true
			}
		}
	}
	return false
}
