package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const manifestFilename = "release-manifest.json"

var (
	imageNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	versionPattern   = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
	commitPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type inventory struct {
	FormatVersion       int              `json:"format_version"`
	SourceRepository    string           `json:"source_repository"`
	Toolchain           toolchain        `json:"toolchain"`
	ExcludedDockerfiles []excludedDocker `json:"excluded_dockerfiles"`
	Images              []image          `json:"images"`
}

type toolchain struct {
	SyftImage  string `json:"syft_image"`
	TrivyImage string `json:"trivy_image"`
}

type image struct {
	Name       string `json:"name"`
	Dockerfile string `json:"dockerfile"`
	VEX        string `json:"vex,omitempty"`
}

type excludedDocker struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type releaseManifest struct {
	FormatVersion    int            `json:"format_version"`
	SourceRepository string         `json:"source_repository"`
	SourceCommit     string         `json:"source_commit"`
	SourceDate       string         `json:"source_date"`
	Version          string         `json:"version"`
	Toolchain        toolchain      `json:"toolchain"`
	Images           []releaseImage `json:"images"`
}

type releaseImage struct {
	Name       string `json:"name"`
	Dockerfile string `json:"dockerfile"`
	Platform   string `json:"platform"`
	Tag        string `json:"tag"`
	ImageID    string `json:"image_id"`
	SBOM       string `json:"sbom"`
	SBOMSHA256 string `json:"sbom_sha256"`
	Scan       string `json:"scan"`
	ScanSHA256 string `json:"scan_sha256"`
}

type spdxDocument struct {
	SPDXVersion       string           `json:"spdxVersion"`
	Name              string           `json:"name"`
	DocumentNamespace string           `json:"documentNamespace"`
	CreationInfo      *json.RawMessage `json:"creationInfo"`
	Packages          []spdxPackage    `json:"packages"`
}

type spdxPackage struct {
	Name                  string `json:"name"`
	VersionInfo           string `json:"versionInfo"`
	PrimaryPackagePurpose string `json:"primaryPackagePurpose"`
}

type trivyReport struct {
	SchemaVersion int             `json:"SchemaVersion"`
	ArtifactName  string          `json:"ArtifactName"`
	ArtifactType  string          `json:"ArtifactType"`
	Metadata      json.RawMessage `json:"Metadata"`
	Results       json.RawMessage `json:"Results"`
}

type commandRunner interface {
	Run(context.Context, string, []string, io.Writer) error
	Output(context.Context, string, ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args []string, stdout io.Writer) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func (execRunner) Output(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Stderr = os.Stderr
	body, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return strings.TrimSpace(string(body)), nil
}

func main() {
	if err := run(os.Args[1:], execRunner{}); err != nil {
		fmt.Fprintln(os.Stderr, "release operation failed:", err)
		os.Exit(1)
	}
}

func run(args []string, runner commandRunner) error {
	if len(args) == 0 {
		return errors.New("expected build, verify, local-build, or local-scan command")
	}
	switch args[0] {
	case "build":
		return runBuild(args[1:], runner)
	case "verify":
		return runVerify(args[1:])
	case "local-build":
		return runLocalBuild(args[1:], runner)
	case "local-scan":
		return runLocalScan(args[1:], runner)
	default:
		return errors.New("expected build, verify, local-build, or local-scan command")
	}
}

func runLocalBuild(args []string, runner commandRunner) error {
	flags := flag.NewFlagSet("local-build", flag.ContinueOnError)
	inventoryPath := flags.String("inventory", "deploy/release/images.json", "release inventory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("local-build accepts no positional arguments")
	}
	spec, err := loadInventory(*inventoryPath)
	if err != nil {
		return err
	}
	for _, item := range spec.Images {
		arguments := []string{"build", "--file", item.Dockerfile, "--tag", "vpn-service/" + item.Name + ":local", "."}
		if err := runner.Run(context.Background(), "docker", arguments, os.Stdout); err != nil {
			return fmt.Errorf("build local image %s: %w", item.Name, err)
		}
	}
	return nil
}

func runLocalScan(args []string, runner commandRunner) error {
	flags := flag.NewFlagSet("local-scan", flag.ContinueOnError)
	inventoryPath := flags.String("inventory", "deploy/release/images.json", "release inventory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("local-scan accepts no positional arguments")
	}
	spec, err := loadInventory(*inventoryPath)
	if err != nil {
		return err
	}
	repositoryRoot, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	for _, item := range spec.Images {
		arguments := []string{
			"run", "--rm", "-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-v", "vpn-service-trivy-cache:/root/.cache/trivy",
		}
		if item.VEX != "" {
			arguments = append(arguments, "-v", repositoryRoot+":/workspace:ro")
		}
		arguments = append(arguments, spec.Toolchain.TrivyImage, "image", "--scanners", "vuln",
			"--severity", "HIGH,CRITICAL", "--exit-code", "1", "--no-progress")
		if item.VEX != "" {
			arguments = append(arguments, "--show-suppressed", "--vex", "/workspace/"+item.VEX)
		}
		arguments = append(arguments, "vpn-service/"+item.Name+":local")
		if err := runner.Run(context.Background(), "docker", arguments, os.Stdout); err != nil {
			return fmt.Errorf("scan local image %s: %w", item.Name, err)
		}
	}
	return nil
}

func runBuild(args []string, runner commandRunner) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	inventoryPath := flags.String("inventory", "deploy/release/images.json", "release inventory")
	outputDir := flags.String("output", "", "release output directory")
	version := flags.String("version", "", "release version")
	commit := flags.String("commit", "", "full source commit")
	sourceDate := flags.String("source-date", "", "source commit date")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outputDir == "" || !versionPattern.MatchString(*version) || !commitPattern.MatchString(*commit) {
		return errors.New("output, version, and a full lowercase source commit are required")
	}
	parsedDate, err := time.Parse(time.RFC3339, *sourceDate)
	if err != nil {
		return errors.New("source-date must be RFC3339")
	}
	spec, err := loadInventory(*inventoryPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return fmt.Errorf("create release output: %w", err)
	}
	entries, err := os.ReadDir(*outputDir)
	if err != nil {
		return fmt.Errorf("inspect release output: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("release output must be empty")
	}

	manifest := releaseManifest{
		FormatVersion: 1, SourceRepository: spec.SourceRepository,
		SourceCommit: *commit, SourceDate: parsedDate.UTC().Format(time.RFC3339),
		Version: *version, Toolchain: spec.Toolchain,
	}
	for _, item := range spec.Images {
		result, buildErr := buildImage(context.Background(), runner, spec, item, *outputDir, manifest)
		if buildErr != nil {
			return fmt.Errorf("build %s: %w", item.Name, buildErr)
		}
		manifest.Images = append(manifest.Images, result)
	}
	manifestPath := filepath.Join(*outputDir, manifestFilename)
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}
	if err := writeChecksums(*outputDir, manifest); err != nil {
		return err
	}
	return verifyRelease(*outputDir, spec)
}

func buildImage(ctx context.Context, runner commandRunner, spec inventory, item image, outputDir string, manifest releaseManifest) (releaseImage, error) {
	tag := fmt.Sprintf("vpn-service/%s:git-%s", item.Name, manifest.SourceCommit[:12])
	buildArgs := []string{
		"build", "--pull=false", "--platform", "linux/amd64", "--file", item.Dockerfile, "--tag", tag,
		"--build-arg", "VERSION=" + manifest.Version,
		"--build-arg", "COMMIT=" + manifest.SourceCommit,
		"--build-arg", "DATE=" + manifest.SourceDate,
		"--label", "org.opencontainers.image.source=" + manifest.SourceRepository,
		"--label", "org.opencontainers.image.revision=" + manifest.SourceCommit,
		"--label", "org.opencontainers.image.version=" + manifest.Version,
		"--label", "org.opencontainers.image.created=" + manifest.SourceDate,
		".",
	}
	if err := runner.Run(ctx, "docker", buildArgs, os.Stdout); err != nil {
		return releaseImage{}, err
	}
	imageID, err := runner.Output(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return releaseImage{}, err
	}
	if !digestPattern.MatchString(imageID) {
		return releaseImage{}, errors.New("docker returned an invalid image ID")
	}

	sbomName := item.Name + ".spdx.json"
	sbomPath := filepath.Join(outputDir, sbomName)
	sbomFile, err := os.OpenFile(sbomPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return releaseImage{}, fmt.Errorf("create SBOM: %w", err)
	}
	syftArgs := []string{
		"run", "--rm", "-v", "/var/run/docker.sock:/var/run/docker.sock",
		spec.Toolchain.SyftImage, "scan", "docker:" + imageID, "--output", "spdx-json",
	}
	runErr := runner.Run(ctx, "docker", syftArgs, sbomFile)
	closeErr := sbomFile.Close()
	if runErr != nil {
		_ = os.Remove(sbomPath)
		return releaseImage{}, runErr
	}
	if closeErr != nil {
		return releaseImage{}, fmt.Errorf("close SBOM: %w", closeErr)
	}
	if err := validateSBOM(sbomPath, imageID); err != nil {
		return releaseImage{}, err
	}
	sbomDigest, err := hashFile(sbomPath)
	if err != nil {
		return releaseImage{}, err
	}
	scanName := item.Name + ".trivy.json"
	scanPath := filepath.Join(outputDir, scanName)
	scanFile, err := os.OpenFile(scanPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return releaseImage{}, fmt.Errorf("create vulnerability report: %w", err)
	}
	trivyArgs := []string{
		"run", "--rm", "-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", "vpn-service-trivy-cache:/root/.cache/trivy",
	}
	if item.VEX != "" {
		repositoryRoot, err := filepath.Abs(".")
		if err != nil {
			return releaseImage{}, fmt.Errorf("resolve repository root: %w", err)
		}
		trivyArgs = append(trivyArgs, "-v", repositoryRoot+":/workspace:ro")
	}
	trivyArgs = append(trivyArgs, spec.Toolchain.TrivyImage, "image", "--scanners", "vuln",
		"--severity", "HIGH,CRITICAL", "--exit-code", "1", "--no-progress", "--format", "json")
	if item.VEX != "" {
		trivyArgs = append(trivyArgs, "--show-suppressed", "--vex", "/workspace/"+item.VEX)
	}
	trivyArgs = append(trivyArgs, imageID)
	runErr = runner.Run(ctx, "docker", trivyArgs, scanFile)
	closeErr = scanFile.Close()
	if runErr != nil {
		_ = os.Remove(scanPath)
		return releaseImage{}, runErr
	}
	if closeErr != nil {
		return releaseImage{}, fmt.Errorf("close vulnerability report: %w", closeErr)
	}
	if err := validateTrivyReport(scanPath, imageID); err != nil {
		return releaseImage{}, err
	}
	scanDigest, err := hashFile(scanPath)
	if err != nil {
		return releaseImage{}, err
	}
	return releaseImage{
		Name: item.Name, Dockerfile: item.Dockerfile, Platform: "linux/amd64",
		Tag: tag, ImageID: imageID,
		SBOM: sbomName, SBOMSHA256: sbomDigest,
		Scan: scanName, ScanSHA256: scanDigest,
	}, nil
}

func runVerify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	outputDir := flags.String("output", "", "release output directory")
	inventoryPath := flags.String("inventory", "deploy/release/images.json", "release inventory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outputDir == "" {
		return errors.New("output is required")
	}
	spec, err := loadInventory(*inventoryPath)
	if err != nil {
		return err
	}
	return verifyRelease(*outputDir, spec)
}

func loadInventory(inventoryPath string) (inventory, error) {
	body, err := os.ReadFile(inventoryPath)
	if err != nil {
		return inventory{}, fmt.Errorf("read inventory: %w", err)
	}
	var spec inventory
	if err := decodeStrictJSON(body, &spec); err != nil {
		return inventory{}, fmt.Errorf("decode inventory: %w", err)
	}
	if spec.FormatVersion != 1 || !strings.HasPrefix(spec.SourceRepository, "https://github.com/") ||
		!pinnedImage(spec.Toolchain.SyftImage) || !pinnedImage(spec.Toolchain.TrivyImage) ||
		len(spec.Images) == 0 {
		return inventory{}, errors.New("release inventory metadata is invalid")
	}
	seenNames := make(map[string]struct{}, len(spec.Images))
	coveredDockerfiles := make(map[string]struct{}, len(spec.Images)+len(spec.ExcludedDockerfiles))
	for _, item := range spec.Images {
		if !imageNamePattern.MatchString(item.Name) || !validDockerfilePath(item.Dockerfile) {
			return inventory{}, fmt.Errorf("invalid image inventory entry %q", item.Name)
		}
		if _, exists := seenNames[item.Name]; exists {
			return inventory{}, fmt.Errorf("duplicate image %q", item.Name)
		}
		seenNames[item.Name] = struct{}{}
		if _, exists := coveredDockerfiles[item.Dockerfile]; exists {
			return inventory{}, fmt.Errorf("duplicate Dockerfile %q", item.Dockerfile)
		}
		coveredDockerfiles[item.Dockerfile] = struct{}{}
		if info, statErr := os.Stat(filepath.FromSlash(item.Dockerfile)); statErr != nil || info.IsDir() {
			return inventory{}, fmt.Errorf("dockerfile for %q is unavailable", item.Name)
		}
		if item.VEX != "" {
			if !validRepositoryPath(item.VEX) {
				return inventory{}, fmt.Errorf("invalid VEX path for %q", item.Name)
			}
			if info, statErr := os.Stat(filepath.FromSlash(item.VEX)); statErr != nil || info.IsDir() {
				return inventory{}, fmt.Errorf("VEX for %q is unavailable", item.Name)
			}
		}
	}
	for _, item := range spec.ExcludedDockerfiles {
		if !validDockerfilePath(item.Path) || !imageNamePattern.MatchString(item.Reason) {
			return inventory{}, fmt.Errorf("invalid excluded Dockerfile %q", item.Path)
		}
		if _, exists := coveredDockerfiles[item.Path]; exists {
			return inventory{}, fmt.Errorf("duplicate Dockerfile %q", item.Path)
		}
		coveredDockerfiles[item.Path] = struct{}{}
		if info, statErr := os.Stat(filepath.FromSlash(item.Path)); statErr != nil || info.IsDir() {
			return inventory{}, fmt.Errorf("excluded Dockerfile %q is unavailable", item.Path)
		}
	}
	discovered, err := discoverReleaseDockerfiles()
	if err != nil {
		return inventory{}, err
	}
	if len(discovered) != len(coveredDockerfiles) {
		return inventory{}, errors.New("release inventory Dockerfile set is incomplete")
	}
	for _, dockerfile := range discovered {
		if _, covered := coveredDockerfiles[dockerfile]; !covered {
			return inventory{}, fmt.Errorf("release inventory does not classify %q", dockerfile)
		}
	}
	return spec, nil
}

func validDockerfilePath(value string) bool {
	base := path.Base(value)
	return validRepositoryPath(value) && (base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile."))
}

func validRepositoryPath(value string) bool {
	clean := path.Clean(value)
	return value != "" && !path.IsAbs(value) && !strings.Contains(value, `\`) &&
		clean == value && !strings.HasPrefix(clean, "../")
}

func discoverReleaseDockerfiles() ([]string, error) {
	var result []string
	for _, root := range []string{"services", "tools", "deploy/observability"} {
		err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || (entry.Name() != "Dockerfile" && !strings.HasPrefix(entry.Name(), "Dockerfile.")) {
				return nil
			}
			result = append(result, filepath.ToSlash(filePath))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("discover release Dockerfiles: %w", err)
		}
	}
	sort.Strings(result)
	return result, nil
}

func decodeStrictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func pinnedImage(value string) bool {
	parts := strings.Split(value, "@")
	return len(parts) == 2 && parts[0] != "" && digestPattern.MatchString(parts[1])
}

func validateSBOM(path, expectedArtifact string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read SBOM: %w", err)
	}
	var document spdxDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return fmt.Errorf("decode SBOM: %w", err)
	}
	if document.SPDXVersion != "SPDX-2.3" || document.Name == "" ||
		!strings.HasPrefix(document.DocumentNamespace, "https://") ||
		document.CreationInfo == nil || len(document.Packages) == 0 {
		return errors.New("SBOM is incomplete or not SPDX 2.3")
	}
	expectedAlgorithm, expectedDigest, ok := strings.Cut(expectedArtifact, ":")
	if !ok {
		return errors.New("SBOM expected artifact is invalid")
	}
	matchedSubject := false
	for _, item := range document.Packages {
		if item.PrimaryPackagePurpose != "CONTAINER" {
			continue
		}
		if matchedSubject || item.Name != expectedAlgorithm || item.VersionInfo != expectedDigest {
			return errors.New("SBOM container subject does not match the image ID")
		}
		matchedSubject = true
	}
	if !matchedSubject {
		return errors.New("SBOM container subject is missing")
	}
	return nil
}

func validateTrivyReport(reportPath, expectedArtifact string) error {
	body, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read vulnerability report: %w", err)
	}
	var report trivyReport
	if err := json.Unmarshal(body, &report); err != nil {
		return fmt.Errorf("decode vulnerability report: %w", err)
	}
	if report.SchemaVersion < 2 || report.ArtifactName != expectedArtifact ||
		report.ArtifactType == "" || len(report.Metadata) == 0 || len(report.Results) == 0 {
		return errors.New("vulnerability report is incomplete or for another artifact")
	}
	return nil
}

func writeJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create release manifest: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}

func writeChecksums(outputDir string, manifest releaseManifest) error {
	paths := []string{manifestFilename}
	for _, item := range manifest.Images {
		paths = append(paths, item.SBOM, item.Scan)
	}
	sort.Strings(paths)
	file, err := os.OpenFile(filepath.Join(outputDir, "SHA256SUMS"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create checksums: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, name := range paths {
		digest, hashErr := hashFile(filepath.Join(outputDir, name))
		if hashErr != nil {
			_ = file.Close()
			return hashErr
		}
		if _, err := fmt.Fprintf(writer, "%s  %s\n", digest, name); err != nil {
			_ = file.Close()
			return err
		}
	}
	return errors.Join(writer.Flush(), file.Close())
}

func verifyRelease(outputDir string, spec inventory) error {
	body, err := os.ReadFile(filepath.Join(outputDir, manifestFilename))
	if err != nil {
		return fmt.Errorf("read release manifest: %w", err)
	}
	var manifest releaseManifest
	if err := decodeStrictJSON(body, &manifest); err != nil {
		return fmt.Errorf("decode release manifest: %w", err)
	}
	if manifest.FormatVersion != 1 || !commitPattern.MatchString(manifest.SourceCommit) ||
		manifest.SourceRepository != spec.SourceRepository || !versionPattern.MatchString(manifest.Version) ||
		!pinnedImage(manifest.Toolchain.SyftImage) || !pinnedImage(manifest.Toolchain.TrivyImage) ||
		manifest.Toolchain != spec.Toolchain || len(manifest.Images) != len(spec.Images) {
		return errors.New("release manifest metadata is invalid")
	}
	if _, err := time.Parse(time.RFC3339, manifest.SourceDate); err != nil {
		return errors.New("release manifest source date is invalid")
	}
	seen := make(map[string]struct{}, len(manifest.Images))
	for index, item := range manifest.Images {
		expectedImage := spec.Images[index]
		if _, exists := seen[item.Name]; exists {
			return fmt.Errorf("duplicate release image %q", item.Name)
		}
		seen[item.Name] = struct{}{}
		if item.Name != expectedImage.Name || item.Dockerfile != expectedImage.Dockerfile ||
			!digestPattern.MatchString(item.ImageID) ||
			item.Tag != fmt.Sprintf("vpn-service/%s:git-%s", item.Name, manifest.SourceCommit[:12]) ||
			item.Platform != "linux/amd64" ||
			filepath.Base(item.SBOM) != item.SBOM || filepath.Base(item.Scan) != item.Scan {
			return fmt.Errorf("release image %q metadata is invalid", item.Name)
		}
		sbomPath := filepath.Join(outputDir, item.SBOM)
		if err := validateSBOM(sbomPath, item.ImageID); err != nil {
			return fmt.Errorf("release image %q: %w", item.Name, err)
		}
		digest, err := hashFile(sbomPath)
		if err != nil {
			return err
		}
		if digest != item.SBOMSHA256 {
			return fmt.Errorf("release image %q SBOM checksum mismatch", item.Name)
		}
		scanPath := filepath.Join(outputDir, item.Scan)
		if err := validateTrivyReport(scanPath, item.ImageID); err != nil {
			return fmt.Errorf("release image %q: %w", item.Name, err)
		}
		digest, err = hashFile(scanPath)
		if err != nil {
			return err
		}
		if digest != item.ScanSHA256 {
			return fmt.Errorf("release image %q vulnerability report checksum mismatch", item.Name)
		}
	}
	expectedChecksums := []string{manifestFilename}
	for _, item := range manifest.Images {
		expectedChecksums = append(expectedChecksums, item.SBOM, item.Scan)
	}
	if err := verifyChecksums(outputDir, expectedChecksums); err != nil {
		return err
	}
	return verifyArtifactSet(outputDir, append(expectedChecksums, "SHA256SUMS"))
}

func verifyArtifactSet(outputDir string, expected []string) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("inspect release artifacts: %w", err)
	}
	expectedSet := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		expectedSet[name] = struct{}{}
	}
	if len(entries) != len(expectedSet) {
		return errors.New("release output has an unexpected artifact set")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return errors.New("release output contains a directory")
		}
		if _, ok := expectedSet[entry.Name()]; !ok {
			return fmt.Errorf("unexpected release artifact %q", entry.Name())
		}
	}
	return nil
}

func verifyChecksums(outputDir string, expected []string) (resultErr error) {
	file, err := os.Open(filepath.Join(outputDir, "SHA256SUMS"))
	if err != nil {
		return fmt.Errorf("open checksums: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close checksums: %w", closeErr))
		}
	}()
	scanner := bufio.NewScanner(file)
	actualNames := make(map[string]struct{}, len(expected))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 || filepath.Base(parts[1]) != parts[1] {
			return errors.New("invalid checksums entry")
		}
		actual, err := hashFile(filepath.Join(outputDir, parts[1]))
		if err != nil {
			return err
		}
		if actual != parts[0] {
			return fmt.Errorf("checksum mismatch for %s", parts[1])
		}
		if _, exists := actualNames[parts[1]]; exists {
			return fmt.Errorf("duplicate checksum for %s", parts[1])
		}
		actualNames[parts[1]] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(actualNames) != len(expected) {
		return errors.New("checksums file has an unexpected artifact set")
	}
	for _, name := range expected {
		if _, exists := actualNames[name]; !exists {
			return fmt.Errorf("checksum is missing for %s", name)
		}
	}
	return nil
}

func hashFile(path string) (digest string, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open artifact: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close artifact: %w", closeErr))
		}
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash artifact: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
