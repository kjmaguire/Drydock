//go:build windows

package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type manager struct{}

func newManager() Manager { return manager{} }

var (
	appJobOnce sync.Once
	appJobErr  error
)

// EnterAppJob puts the Drydock process into a KILL_ON_JOB_CLOSE job so that every descendant dies with the app
// (decision D7). If the process is already in a job that forbids nesting the error is returned and logged by the
// caller; per-agent jobs still apply.
func EnterAppJob() error {
	appJobOnce.Do(func() {
		job, err := newKillOnCloseJob()
		if err != nil {
			appJobErr = err
			return
		}
		cur, _ := windows.GetCurrentProcess()
		if err := windows.AssignProcessToJobObject(job, cur); err != nil {
			appJobErr = fmt.Errorf("assign app to job: %w", err)
			windows.CloseHandle(job)
			return
		}
		// Intentionally never closed: the handle lives until the process exits, which kills the job.
	})
	return appJobErr
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

type winProc struct {
	cmd    *exec.Cmd
	job    windows.Handle
	jobMu  sync.Mutex
	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
	exit   chan ExitInfo
	done   chan struct{}
	killed bool
}

func (manager) Start(ctx context.Context, spec Spec) (Proc, error) {
	path, err := exec.LookPath(spec.Path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	// CREATE_SUSPENDED so the process is in its job before it can spawn anything; CREATE_NEW_PROCESS_GROUP so a
	// console Ctrl-Break can target it if a console ever exists.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	closeAll := func() {
		for _, f := range []*os.File{inR, inW, outR, outW, errR, errW} {
			f.Close()
		}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW

	job, err := newKillOnCloseJob()
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create job: %w", err)
	}
	if err := cmd.Start(); err != nil {
		closeAll()
		windows.CloseHandle(job)
		return nil, err
	}
	inR.Close()
	outW.Close()
	errW.Close()

	if err := assignAndResume(cmd.Process.Pid, job); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		inW.Close()
		outR.Close()
		errR.Close()
		windows.CloseHandle(job)
		return nil, err
	}

	p := &winProc{cmd: cmd, job: job, stdin: inW, stdout: outR, stderr: errR,
		exit: make(chan ExitInfo, 1), done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		ei := ExitInfo{Code: cmd.ProcessState.ExitCode()}
		p.jobMu.Lock()
		ei.Killed = p.killed
		// The direct child has exited; release the job so any stragglers are reaped too.
		p.closeJobLocked()
		p.jobMu.Unlock()
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			ei.Err = err
		}
		p.exit <- ei
		close(p.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = p.Kill(true)
		case <-p.done:
		}
	}()
	return p, nil
}

// assignAndResume assigns the suspended process to job and resumes its primary thread.
func assignAndResume(pid int, job windows.Handle) error {
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		return fmt.Errorf("assign to job: %w", err)
	}
	return resumeMainThread(uint32(pid))
}

func resumeMainThread(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("thread snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	if err := windows.Thread32First(snap, &te); err != nil {
		return fmt.Errorf("thread32first: %w", err)
	}
	resumed := false
	for {
		if te.OwnerProcessID == pid {
			th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
			if err == nil {
				_, err = windows.ResumeThread(th)
				windows.CloseHandle(th)
				if err != nil {
					return fmt.Errorf("resume thread: %w", err)
				}
				resumed = true
			}
		}
		if err := windows.Thread32Next(snap, &te); err != nil {
			break
		}
	}
	if !resumed {
		return errors.New("proc: no thread found to resume")
	}
	return nil
}

func (p *winProc) closeJobLocked() {
	if p.job != 0 {
		windows.CloseHandle(p.job) // KILL_ON_JOB_CLOSE terminates every process still in the job
		p.job = 0
	}
}

func (p *winProc) PID() int              { return p.cmd.Process.Pid }
func (p *winProc) Stdin() io.WriteCloser { return p.stdin }
func (p *winProc) Stdout() io.Reader     { return p.stdout }
func (p *winProc) Stderr() io.Reader     { return p.stderr }
func (p *winProc) Wait() <-chan ExitInfo { return p.exit }

// Signal: Windows has no signals. Interrupt tries CTRL_BREAK_EVENT, which only works when this process has a
// console (it does not in the packaged GUI app). Callers fall back to protocol-level cancel on error.
func (p *winProc) Signal(sig Signal) error {
	switch sig {
	case Interrupt:
		if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(p.cmd.Process.Pid)); err != nil {
			return fmt.Errorf("%w: %v", ErrNotSupported, err)
		}
		return nil
	case Terminate:
		return p.Kill(false)
	}
	return ErrNotSupported
}

func (p *winProc) Kill(tree bool) error {
	p.jobMu.Lock()
	p.killed = true
	if tree {
		p.closeJobLocked()
		p.jobMu.Unlock()
		return nil
	}
	p.jobMu.Unlock()
	return p.cmd.Process.Kill()
}
