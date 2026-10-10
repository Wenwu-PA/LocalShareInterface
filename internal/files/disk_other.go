//go:build !windows && !linux

package files

import "errors"

func freeSpace(directory string) (uint64, uint64, error) {
	return 0, 0, errors.New("проверка свободного места поддерживает Windows и Linux")
}
