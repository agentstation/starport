//go:build darwin || linux

package setup

import "os"

func syncDirectory(path string) (err error) {
	directory, err := os.Open(path) // #nosec G304 -- setup supplies validated managed directory paths.
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := directory.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return directory.Sync()
}
