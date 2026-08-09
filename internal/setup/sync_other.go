//go:build !darwin && !linux && !windows

package setup

func syncDirectory(string) error { return errExclusiveRenameUnsupported }
