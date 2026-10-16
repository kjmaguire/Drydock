//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Not defined in x/sys/windows v0.48.0; value derived from WinBase.h: ProcThreadAttributeJobList(13) | PROC_THREAD_ATTRIBUTE_INPUT(0x20000)
const procThreadAttributeJobList = 0x0002000D

func main() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		panic(err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		panic(err)
	}
	al, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		panic(err)
	}
	defer al.Delete()
	h := job
	if err := al.Update(procThreadAttributeJobList, unsafe.Pointer(&h), unsafe.Sizeof(h)); err != nil {
		panic(err)
	}
	si := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))}, ProcThreadAttributeList: al.List()}
	var pi windows.ProcessInformation
	cmd, _ := windows.UTF16PtrFromString("cmd.exe /c exit 0")
	err = windows.CreateProcess(nil, cmd, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_SUSPENDED, nil, nil, &si.StartupInfo, &pi)
	fmt.Println(err)
	_ = windows.AssignProcessToJobObject
}
