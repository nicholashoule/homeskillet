//go:build linux || darwin

// Running a step approved by --yes on Linux and macOS: in a session of its
// own, without the terminal, passing interrupts on to its process group.

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

// runDetached runs cmd, a step approved by --yes, in a session of its own,
// which has no controlling terminal, so the step cannot open /dev/tty either.
// sudo, doas, and su read passwords from there, and by default remember one
// per terminal for a few minutes, so in such a step they can neither prompt,
// which would hang an unattended run, nor run as root on a password the user
// typed for something else. The kernel enforces this however the step
// reaches them: through sh -c, a script, or eval of hex or base64 text, all
// of which defeat any check of the command's words. Steps approved one at a
// time keep the terminal, as the user has just reviewed each one.
//
// It does not stop sudo configured to need no password (NOPASSWD) or to
// share one cached password across terminals (timestamp_type=global), which
// the system's administrator chose, nor a graphical password prompt, which
// the user sees.
//
// The terminal sends Ctrl-C to its foreground process group, which the step
// has left, so interrupts reaching homeskillet while the step runs are passed
// on to the step's process group. The step leads that group, as Setsid makes
// it, so a negative process ID reaches everything it started; cancellation
// stops the whole group the same way.
//
// A step need not stop for the signal: a non-interactive shell starts its
// background jobs ignoring SIGINT, and a step may catch it. So whatever is
// still running interruptGrace after the first signal is killed, which no
// process can ignore, and the step fails even if it exited successfully, so
// setup stops rather than run the commands and steps after it.
func runDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// A signal the process was started ignoring, as nohup ignores SIGHUP and
	// a shell ignores SIGINT for a background job, stays ignored: Notify
	// would otherwise start delivering it, and the step would die of the
	// very signal the user shut out. The step inherits the ignoring too.
	signals := make(chan os.Signal, 1)
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			signal.Notify(signals, sig)
		}
	}
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
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
				// signal.
				if number, ok := received.(syscall.Signal); ok {
					_ = syscall.Kill(-cmd.Process.Pid, number)
				}
				if interrupted == nil {
					interrupted = received
					kill = time.After(interruptGrace)
				}
			case <-kill:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	<-stopped
	// A signal that arrived as the step finished still counts. Stop first:
	// once it returns nothing more is sent, so the drain below misses none.
	signal.Stop(signals)
	if interrupted == nil {
		select {
		case interrupted = <-signals:
		default:
		}
	}
	if interrupted != nil {
		// The step may have exited while children that ignore the signal
		// go on: with output in files rather than pipes, Wait returns as
		// soon as the leader is gone, before the grace period above fires.
		// The group keeps its ID while any member lives, so it is killed
		// here as well; nothing remains to signal otherwise.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return errors.Join(fmt.Errorf("stopped by %v", interrupted), err)
	}
	return err
}

// interruptGrace is how long a step run under --yes has to stop after an
// interrupt before its process group is killed (see runDetached).
var interruptGrace = 5 * time.Second
