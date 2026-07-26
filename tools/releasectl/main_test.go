package main

import (
	"encoding/json"
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
