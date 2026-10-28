// Running a step approved by --yes on Windows: in a process group and a job
// object of its own, so an interrupt reaches the step and, failing that,
// everything it started is stopped.

package setup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObject       = kernel32.NewProc("TerminateJobObject")
	generateConsoleCtrlEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

// runDetached runs cmd, a step approved by --yes, with no input (cmd.Stdin
// is nil), in a process group of its own, and in a job object, which every
// process it starts joins too.
//
// Unlike on Linux and macOS, the step keeps the console. What Unix guards
// against there, sudo using a password the user typed for something else,
// does not arise: Windows's sudo asks through User Account Control, a dialog
// the user sees and answers each time.
//
// A process group of its own does not receive the Ctrl-C the console sends
// homeskillet, so an interrupt is passed on as Ctrl-Break, which console
// programs treat as Ctrl-C. Whatever is still running interruptGrace after the
// first one, and every process the step started, is stopped through the job,
// and the step fails even if it exited successfully, so setup stops rather
// than run the commands and steps after it. Cancellation stops the job the
// same way.
func runDetached(cmd *exec.Cmd) error {
	job, _, err := createJobObject.Call(0, 0)
	if job == 0 {
		return fmt.Errorf("creating a job object: %w", err)
	}
	defer syscall.CloseHandle(syscall.Handle(job)) //nolint:errcheck // closing the job lets its processes run on; nothing is lost
	stop := func() error {
		if succeeded, _, err := terminateJobObject.Call(job, 1); succeeded == 0 {
			return err
		}
		return nil
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = stop
	// As on Unix, a signal the process was started ignoring stays ignored.
	signals := make(chan os.Signal, 1)
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !signal.Ignored(sig) {
			signal.Notify(signals, sig)
		}
	}
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}
	// The step is put in the job as soon as it starts. It is still loading
	// then, long before it can start anything of its own.
	if err := assign(job, cmd.Process.Pid); err != nil {
		return errors.Join(fmt.Errorf("putting the step in a job object: %w", err), cmd.Process.Kill(), cmd.Wait())
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	var interrupted os.Signal
	go func() {
		defer close(stopped)
		var kill <-chan time.Time
		for {
			select {
			case received := <-signals:
				// The step may already have exited, leaving nothing to
				// interrupt.
				const ctrlBreak = 1 // CTRL_BREAK_EVENT
				_, _, _ = generateConsoleCtrlEvent.Call(ctrlBreak, uintptr(cmd.Process.Pid))
				if interrupted == nil {
					interrupted = received
					kill = time.After(interruptGrace)
				}
			case <-kill:
				_ = stop()
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	close(done)
	<-stopped
	signal.Stop(signals)
	if interrupted == nil {
		select {
		case interrupted = <-signals:
		default:
		}
	}
	if interrupted != nil {
		// Processes the step started may still run after it exits.
		_ = stop()
		return errors.Join(fmt.Errorf("stopped by %v", interrupted), err)
	}
	return err
}

// assign puts the process pid in job.
func assign(job uintptr, pid int) error {
	const access = 0x0100 | 0x0001                                  // PROCESS_SET_QUOTA | PROCESS_TERMINATE
	process, err := syscall.OpenProcess(access, false, uint32(pid)) //nolint:gosec // G115: process IDs fit in a DWORD
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process) //nolint:errcheck // a handle opened only for this call
	if succeeded, _, err := assignProcessToJobObject.Call(job, uintptr(process)); succeeded == 0 {
		return err
	}
	return nil
}

// interruptGrace is how long a step run under --yes has to stop after an
// interrupt before its job is stopped (see runDetached).
var interruptGrace = 5 * time.Second
