//go:build windows

package staging

import (
	"errors"
	"os"
)

func fileOwner(os.FileInfo) (int, int, error) {
	return 0, 0, errors.New("credential ownership verification requires Linux")
}
