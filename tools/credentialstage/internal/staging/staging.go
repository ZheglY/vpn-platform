package staging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const manifestVersion = 1

type Manifest struct {
	Version     int         `json:"version"`
	Directories []Directory `json:"directories"`
	Files       []File      `json:"files"`
}

type Directory struct {
	Path  string `json:"path"`
	Mode  string `json:"mode"`
	UID   int    `json:"uid"`
	GID   int    `json:"gid"`
	Group string `json:"group"`
}

type File struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	UID         int    `json:"uid"`
	GID         int    `json:"gid"`
	Group       string `json:"group"`
}

func LoadManifest(path string) (Manifest, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read credential manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode credential manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("decode credential manifest: trailing JSON value")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Stage(manifest Manifest, sourceRoot, destinationRoot, group string) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := validateGroup(manifest, group); err != nil {
		return err
	}
	for _, file := range manifest.Files {
		if !selected(file.Group, group) {
			continue
		}
		source, err := confinedPath(sourceRoot, file.Source)
		if err != nil {
			return fmt.Errorf("source %q: %w", file.Source, err)
		}
		destination, err := confinedPath(destinationRoot, file.Destination)
		if err != nil {
			return fmt.Errorf("destination %q: %w", file.Destination, err)
		}
		if err := stageFile(source, destination, file); err != nil {
			return err
		}
	}
	for _, directory := range manifest.Directories {
		if !selected(directory.Group, group) {
			continue
		}
		path, err := confinedPath(destinationRoot, directory.Path)
		if err != nil {
			return fmt.Errorf("directory %q: %w", directory.Path, err)
		}
		mode, _ := parseMode(directory.Mode)
		if err := os.Chown(path, directory.UID, directory.GID); err != nil {
			return fmt.Errorf("set credential directory owner: %w", err)
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("set credential directory mode: %w", err)
		}
	}
	return nil
}

func Verify(manifest Manifest, destinationRoot, group string) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := validateGroup(manifest, group); err != nil {
		return err
	}
	for _, directory := range manifest.Directories {
		if !selected(directory.Group, group) {
			continue
		}
		path, err := confinedPath(destinationRoot, directory.Path)
		if err != nil {
			return err
		}
		if err := verifyMetadata(path, directory.Mode, directory.UID, directory.GID, true); err != nil {
			return err
		}
	}
	for _, file := range manifest.Files {
		if !selected(file.Group, group) {
			continue
		}
		path, err := confinedPath(destinationRoot, file.Destination)
		if err != nil {
			return err
		}
		if err := verifyMetadata(path, file.Mode, file.UID, file.GID, false); err != nil {
			return err
		}
		handle, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open staged credential %q: %w", file.Destination, err)
		}
		_, readErr := io.Copy(io.Discard, handle)
		closeErr := handle.Close()
		if readErr != nil {
			return fmt.Errorf("read staged credential %q: %w", file.Destination, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close staged credential %q: %w", file.Destination, closeErr)
		}
	}
	return nil
}

func stageFile(source, destination string, file File) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect source credential %q: %w", file.Source, err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("source credential %q is not a regular file", file.Source)
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source credential %q: %w", file.Source, err)
	}
	defer func() {
		_ = input.Close()
	}()
	openedInfo, err := input.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened credential %q: %w", file.Source, err)
	}
	if !os.SameFile(sourceInfo, openedInfo) {
		return fmt.Errorf("source credential %q changed while opening", file.Source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create credential destination: %w", err)
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".credential-*")
	if err != nil {
		return fmt.Errorf("create staged credential: %w", err)
	}
	tempPath := output.Name()
	keep := false
	defer func() {
		_ = output.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy credential %q: %w", file.Source, err)
	}
	if err := input.Close(); err != nil {
		return fmt.Errorf("close source credential %q: %w", file.Source, err)
	}
	mode, _ := parseMode(file.Mode)
	if err := output.Chown(file.UID, file.GID); err != nil {
		return fmt.Errorf("set credential owner for %q: %w", file.Destination, err)
	}
	if err := output.Chmod(mode); err != nil {
		return fmt.Errorf("set credential mode for %q: %w", file.Destination, err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync credential %q: %w", file.Destination, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close credential %q: %w", file.Destination, err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("publish credential %q: %w", file.Destination, err)
	}
	keep = true
	return nil
}

func verifyMetadata(path, expectedMode string, uid, gid int, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect staged credential %q: %w", path, err)
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("staged credential path %q is not a directory", path)
	}
	if !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("staged credential path %q is not a regular file", path)
	}
	mode, _ := parseMode(expectedMode)
	if info.Mode().Perm() != mode {
		return fmt.Errorf("staged credential %q mode is %04o, want %04o", path, info.Mode().Perm(), mode)
	}
	actualUID, actualGID, err := fileOwner(info)
	if err != nil {
		return fmt.Errorf("inspect staged credential owner %q: %w", path, err)
	}
	if actualUID != uid || actualGID != gid {
		return fmt.Errorf("staged credential %q owner is %d:%d, want %d:%d", path, actualUID, actualGID, uid, gid)
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Version != manifestVersion {
		return fmt.Errorf("credential manifest version is %d, want %d", manifest.Version, manifestVersion)
	}
	if len(manifest.Directories) == 0 || len(manifest.Files) == 0 {
		return errors.New("credential manifest must contain directories and files")
	}
	directories := make(map[string]Directory, len(manifest.Directories))
	for _, directory := range manifest.Directories {
		if err := validateEntry(directory.Path, directory.Mode, directory.UID, directory.GID, directory.Group, true); err != nil {
			return fmt.Errorf("invalid directory: %w", err)
		}
		if _, exists := directories[directory.Path]; exists {
			return fmt.Errorf("duplicate credential directory %q", directory.Path)
		}
		directories[directory.Path] = directory
	}
	destinations := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		if err := validateEntry(file.Source, file.Mode, file.UID, file.GID, file.Group, false); err != nil {
			return fmt.Errorf("invalid source file: %w", err)
		}
		if err := validateRelativePath(file.Destination); err != nil {
			return fmt.Errorf("invalid destination file: %w", err)
		}
		if _, exists := destinations[file.Destination]; exists {
			return fmt.Errorf("duplicate credential destination %q", file.Destination)
		}
		destinations[file.Destination] = struct{}{}
		parent := filepath.Dir(file.Destination)
		directory, exists := directories[parent]
		if !exists || directory.Group != file.Group || directory.UID != file.UID || directory.GID != file.GID {
			return fmt.Errorf("credential destination %q has no matching owner directory", file.Destination)
		}
	}
	return nil
}

func validateEntry(path, mode string, uid, gid int, group string, directory bool) error {
	if err := validateRelativePath(path); err != nil {
		return err
	}
	parsed, err := parseMode(mode)
	if err != nil {
		return err
	}
	if directory {
		if parsed != 0o500 {
			return fmt.Errorf("directory %q mode must be 0500", path)
		}
	} else if parsed != 0o400 && parsed != 0o440 {
		return fmt.Errorf("file %q mode must be 0400 or 0440", path)
	}
	if uid < 0 || gid < 0 {
		return fmt.Errorf("path %q has a negative owner", path)
	}
	if strings.TrimSpace(group) == "" {
		return fmt.Errorf("path %q has an empty group", path)
	}
	return nil
}

func validateRelativePath(path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." {
		return fmt.Errorf("path %q must be a clean relative path", path)
	}
	if strings.HasPrefix(path, ".."+string(filepath.Separator)) || path == ".." {
		return fmt.Errorf("path %q escapes its root", path)
	}
	return nil
}

func confinedPath(root, relative string) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	path := filepath.Join(absoluteRoot, relative)
	prefix := absoluteRoot + string(filepath.Separator)
	if !strings.HasPrefix(path, prefix) {
		return "", errors.New("path escapes its root")
	}
	return path, nil
}

func parseMode(value string) (os.FileMode, error) {
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("parse mode %q: %w", value, err)
	}
	return os.FileMode(parsed), nil
}

func selected(entryGroup, requested string) bool {
	return requested == "" || entryGroup == requested
}

func validateGroup(manifest Manifest, group string) error {
	if group == "" {
		return nil
	}
	directories := 0
	files := 0
	for _, directory := range manifest.Directories {
		if directory.Group == group {
			directories++
		}
	}
	for _, file := range manifest.Files {
		if file.Group == group {
			files++
		}
	}
	if directories == 0 || files == 0 {
		return fmt.Errorf("credential manifest group %q does not contain directories and files", group)
	}
	return nil
}
