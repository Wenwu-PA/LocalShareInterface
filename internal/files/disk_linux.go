package files

import "golang.org/x/sys/unix"

func freeSpace(directory string) (uint64, uint64, error) {
	var stat unix.Statfs_t
	e := unix.Statfs(directory, &stat)
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize), e
}
