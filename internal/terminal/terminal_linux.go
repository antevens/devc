package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/term"
)

// drainTimeout is how long output is read after the command exits, when
// processes it started still hold the pseudo-terminal open.
const drainTimeout = time.Second

// winsize is the argument of TIOCSWINSZ.
type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// Run runs cmd on a new pseudo-terminal the size of in, copying input from
// in and output to out. A terminal in is put in raw mode while cmd runs.
// It returns the error of cmd.
func Run(cmd *exec.Cmd, in *os.File, out io.Writer) error {
	master, slave, err := open()
	if err != nil {
		return err
	}
	defer master.Close()

	fd := int(in.Fd())
	resize(fd, master)
	if term.IsTerminal(fd) {
		state, err := term.MakeRaw(fd)
		if err != nil {
			slave.Close()
			return err
		}
		defer term.Restore(fd, state)
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	stop := make(chan struct{})
	defer func() {
		signal.Stop(winch)
		close(stop)
	}()
	go func() {
		for {
			select {
			case <-winch:
				resize(fd, master)
			case <-stop:
				return
			}
		}
	}()

	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	err = cmd.Start()
	slave.Close()
	if err != nil {
		return err
	}

	// The input copy ends at the first read after cmd exits.
	go func() { _, _ = io.Copy(master, in) }()
	output := make(chan error, 1)
	go func() { output <- copyOutput(out, master) }()

	err = cmd.Wait()
	var outErr error
	select {
	case outErr = <-output:
	case <-time.After(drainTimeout):
		master.Close()
		outErr = <-output
	}
	if err != nil {
		return err
	}
	return outErr
}

// copyOutput copies the output of the pseudo-terminal to out until every
// process has closed it.
func copyOutput(out io.Writer, master *os.File) error {
	_, err := io.Copy(out, master)
	if errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

// open opens a new pseudo-terminal.
func open() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open pseudo-terminal: %w", err)
	}
	var unlock int32
	if err := ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("unlock pseudo-terminal: %w", err)
	}
	var n uint32
	if err := ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("get pseudo-terminal number: %w", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("open pseudo-terminal: %w", err)
	}
	return master, slave, nil
}

// resize gives the pseudo-terminal the size of the terminal fd, if it is
// one.
func resize(fd int, master *os.File) {
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return
	}
	ws := winsize{rows: uint16(rows), cols: uint16(cols)}
	_ = ioctl(master, syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
}

// ioctl runs an ioctl on f without putting f in blocking mode.
func ioctl(f *os.File, req uint, arg unsafe.Pointer) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
