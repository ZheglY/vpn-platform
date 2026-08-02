package staging

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateManifestRejectsEscapingDestination(t *testing.T) {
	uid, gid := testOwner()
	manifest := testManifest(uid, gid)
	manifest.Files[0].Destination = "../private.key"
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest() accepted an escaping destination")
	}
}

func TestVerifyRejectsUnknownGroup(t *testing.T) {
	uid, gid := testOwner()
	if err := Verify(testManifest(uid, gid), t.TempDir(), "missing"); err == nil {
		t.Fatal("Verify() accepted an unknown group")
	}
}

func TestStageAndVerify(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux ownership and mode behavior is verified in the container smoke")
	}
	source := t.TempDir()
	destination := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "private.key"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	uid, gid := testOwner()
	manifest := testManifest(uid, gid)
	t.Cleanup(func() {
		if err := removeStagedTestCredentials(destination, manifest); err != nil {
			t.Errorf("clean staged credentials: %v", err)
		}
	})
	if err := Stage(manifest, source, destination, "service"); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if err := Verify(manifest, destination, "service"); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := removeStagedTestCredentials(destination, manifest); err != nil {
		t.Fatalf("remove staged credentials after verification: %v", err)
	}
}

func TestStageRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is not portable to the Windows test runner")
	}
	source := t.TempDir()
	destination := t.TempDir()
	target := filepath.Join(source, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(source, "private.key")); err != nil {
		t.Fatal(err)
	}
	uid, gid := testOwner()
	if err := Stage(testManifest(uid, gid), source, destination, "service"); err == nil {
		t.Fatal("Stage() accepted a symbolic link")
	}
}

func testManifest(uid, gid int) Manifest {
	return Manifest{
		Version: 1,
		Directories: []Directory{
			{Path: "service", Mode: "0500", UID: uid, GID: gid, Group: "service"},
		},
		Files: []File{
			{Source: "private.key", Destination: "service/private.key", Mode: "0400", UID: uid, GID: gid, Group: "service"},
		},
	}
}

func removeStagedTestCredentials(root string, manifest Manifest) error {
	for _, directory := range manifest.Directories {
		path := filepath.Join(root, directory.Path)
		if err := os.Chmod(path, 0o700); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.RemoveAll(root)
}
