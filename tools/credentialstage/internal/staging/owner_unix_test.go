//go:build !windows

package staging

import "os"

func testOwner() (int, int) {
	return os.Getuid(), os.Getgid()
}
