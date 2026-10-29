//go:build windows

package proc

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UNVERIFIED on Windows: cross-compiled and vetted only. golang.org/x/sys/windows has no wrapper for IsProcessInJob, so it is
// called through kernel32.dll.
var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// jobs maps the pid of a process Drydock started to its *winProc, whose job holds the process and everything it starts.
var jobs sync.Map

// InTree asks the root's Job Object when Drydock started root (IsProcessInJob), and otherwise follows the parents
// (CreateToolhelp32Snapshot).
func InTree(root, pid int) (bool, error) {
	if root <= 0 || pid <= 0 {
		return false, nil
	}
	if pid == root {
		return true, nil
	}
	if v, ok := jobs.Load(root); ok {
		p := v.(*winProc)
		p.jobMu.Lock()
		defer p.jobMu.Unlock()
		if p.job == 0 {
			return false, nil // the run's process has ended: nothing belongs to it any more
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return false, nil // gone, or not ours to open
		}
		defer windows.CloseHandle(h)
		var in int32
		if err := procIsProcessInJob.Find(); err != nil {
			return false, err
		}
		r, _, callErr := procIsProcessInJob.Call(uintptr(h), uintptr(p.job), uintptr(unsafe.Pointer(&in)))
		if r == 0 {
			return false, callErr
		}
		return in != 0, nil
	}
	parents, err := parentTable()
	if err != nil {
		return false, err
	}
	return inTreeByParents(root, pid, func(p int) (int, error) {
		if pp, ok := parents[p]; ok {
			return pp, nil
		}
		return 0, errNoParent
	}), nil
}

// parentTable is every process's parent from one snapshot.
func parentTable() (map[int]int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	out := map[int]int{}
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, err
	}
	for {
		out[int(pe.ProcessID)] = int(pe.ParentProcessID)
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return out, nil
}
