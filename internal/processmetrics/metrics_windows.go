package processmetrics

import (
	"errors"
	"golang.org/x/sys/windows"
	"time"
	"unsafe"
)

var memoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// PROCESS_MEMORY_COUNTERS_EX: SIZE_T fields follow two DWORD fields.
type memoryCounters struct {
	size         uint32
	faults       uint32
	peakWorking  uintptr
	working      uintptr
	peakPaged    uintptr
	paged        uintptr
	peakNonPaged uintptr
	nonPaged     uintptr
	pagefile     uintptr
	peakPagefile uintptr
	private      uintptr
}

func readCounters() (counters, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return counters{}, err
	}
	if err := memoryInfo.Find(); err != nil {
		return counters{}, err
	}
	var memory memoryCounters
	memory.size = uint32(unsafe.Sizeof(memory))
	ok, _, err := memoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&memory)), uintptr(memory.size))
	if ok == 0 {
		if err == nil || err == windows.ERROR_SUCCESS {
			err = errors.New("process memory query failed")
		}
		return counters{}, err
	}
	ticks := func(value windows.Filetime) uint64 { return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime) }
	private := uint64(memory.private)
	return counters{resident: uint64(memory.working), private: &private, cpu: time.Duration(ticks(kernel)+ticks(user)) * 100}, nil
}
