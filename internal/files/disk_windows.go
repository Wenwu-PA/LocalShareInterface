package files

import "golang.org/x/sys/windows"

func freeSpace(directory string) (uint64, uint64, error) {
	p, e := windows.UTF16PtrFromString(directory)
	if e != nil {
		return 0, 0, e
	}
	var available, total, free uint64
	e = windows.GetDiskFreeSpaceEx(p, &available, &total, &free)
	return available, total, e
}
