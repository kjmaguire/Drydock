//go:build windows

// jobtree proves (or disproves) that Job Objects reap whole process trees, in the ways Drydock plans to use them.
// Modes:
//
//	baseline   - plain os/exec + Process.Kill: expect orphans (documents why Job Objects are needed)
//	joblist    - CreateProcess with PROC_THREAD_ATTRIBUTE_JOB_LIST (0x2000D), then close job handle
//	assign     - CREATE_SUSPENDED, AssignProcessToJobObject, ResumeThread, then close job handle
//	selfjob    - put this process in a KILL_ON_JOB_CLOSE job first, spawn with os/exec, close job handle
//	nested     - selfjob plus a per-agent child job assigned after start (nested jobs, Windows 8+)
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const procThreadAttributeJobList = 0x0002000D // derived from WinBase.h; not in x/sys/windows

func newKillOnCloseJob() windows.Handle {
	job, err := windows.CreateJobObject(nil, nil)
	must(err)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	must(err)
	return job
}

func must(err error) {
	if err != nil {
		fmt.Println("FATAL:", err)
		os.Exit(2)
	}
}

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

func inJob(h windows.Handle) bool {
	var res int32
	r, _, _ := procIsProcessInJob.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&res)))
	return r != 0 && res != 0
}

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

func readPIDs(path string) []int {
	b, _ := os.ReadFile(path)
	var out []int
	for _, l := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(l); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func main() {
	mode := os.Args[1]
	fixture := os.Args[2]
	dir, _ := os.MkdirTemp("", "jobtree")
	pidfile := filepath.Join(dir, "pids.txt")

	cur, _ := windows.GetCurrentProcess()
	fmt.Printf("mode=%s self_in_job_at_start=%v\n", mode, inJob(cur))

	var rootPID int
	var job windows.Handle
	var killTree func()

	switch mode {
	case "baseline":
		c := exec.Command(fixture, "parent", pidfile)
		must(c.Start())
		rootPID = c.Process.Pid
		killTree = func() { _ = c.Process.Kill() }
	case "joblist":
		job = newKillOnCloseJob()
		al, err := windows.NewProcThreadAttributeList(1)
		must(err)
		h := job
		must(al.Update(procThreadAttributeJobList, unsafe.Pointer(&h), unsafe.Sizeof(h)))
		si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))}, ProcThreadAttributeList: al.List()}
		var pi windows.ProcessInformation
		cmd, _ := windows.UTF16PtrFromString(fmt.Sprintf(`"%s" parent "%s"`, fixture, pidfile))
		must(windows.CreateProcess(nil, cmd, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &si.StartupInfo, &pi))
		rootPID = int(pi.ProcessId)
		fmt.Printf("root_in_job=%v\n", inJob(pi.Process))
		killTree = func() { windows.CloseHandle(job); job = 0 }
	case "assign":
		job = newKillOnCloseJob()
		var si windows.StartupInfo
		si.Cb = uint32(unsafe.Sizeof(si))
		var pi windows.ProcessInformation
		cmd, _ := windows.UTF16PtrFromString(fmt.Sprintf(`"%s" parent "%s"`, fixture, pidfile))
		must(windows.CreateProcess(nil, cmd, nil, nil, false, windows.CREATE_SUSPENDED, nil, nil, &si, &pi))
		must(windows.AssignProcessToJobObject(job, pi.Process))
		_, err := windows.ResumeThread(pi.Thread)
		must(err)
		rootPID = int(pi.ProcessId)
		killTree = func() { windows.CloseHandle(job); job = 0 }
	case "selfjob", "nested":
		job = newKillOnCloseJob()
		must(windows.AssignProcessToJobObject(job, cur))
		c := exec.Command(fixture, "parent", pidfile)
		must(c.Start())
		rootPID = c.Process.Pid
		killTree = func() { windows.CloseHandle(job); job = 0 }
		if mode == "nested" {
			child := newKillOnCloseJob()
			// assign root to a child job after start; its own grandchildren inherit whichever job the root is in
			ph, perr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(rootPID))
			must(perr)
			aerr := windows.AssignProcessToJobObject(child, ph)
			fmt.Printf("assign_root_to_nested_job_err=%v\n", aerr)
			killTree = func() { windows.CloseHandle(child) } // kills only the nested job's tree; outer job remains
		}
	default:
		must(fmt.Errorf("unknown mode"))
	}

	// wait for the fixture to record its tree (root + 2 children + 2 grandchildren)
	var pids []int
	for i := 0; i < 50; i++ {
		pids = readPIDs(pidfile)
		if len(pids) >= 5 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Printf("root=%d recorded_pids=%v\n", rootPID, pids)
	for _, p := range pids {
		if !alive(p) {
			fmt.Printf("WARN pid %d not alive before kill\n", p)
		}
	}

	killTree()
	time.Sleep(1500 * time.Millisecond)

	orphans := 0
	for _, p := range pids {
		if alive(p) {
			orphans++
			fmt.Printf("ORPHAN pid=%d\n", p)
			// clean up so later steps are not affected
			if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(p)); err == nil {
				_ = windows.TerminateProcess(h, 1)
				windows.CloseHandle(h)
			}
		}
	}
	fmt.Printf("RESULT mode=%s tree_size=%d orphans=%d\n", mode, len(pids), orphans)
	if mode != "baseline" && (orphans > 0 || len(pids) < 5) {
		os.Exit(1)
	}
}
