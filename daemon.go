
package main

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

var isDaemonEnv = "GO_IS_DAEMONIZED"

func getDevNullFile(minFd int) (file *os.File, err error) {
	fd := -1
	for fd < minFd {
		file, err = os.OpenFile("/dev/null", os.O_RDWR, 0666)
		if err != nil {
			return
		}
		fd = int(file.Fd())
	}
	return
}

func copyIO(wg *sync.WaitGroup, from *os.File, to *os.File) {
	defer wg.Done()
	for {
		w, _ := io.Copy(to, from)
		if w <= 0 {
			return
		}
	}
}

func relaySignals(pid int) {
	c := make(chan os.Signal, 8)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	for {
		s := <-c
		s2 := s.(syscall.Signal)
		syscall.Kill(pid, s2)
	}
}

// Fork, then execute this binary again. This function will relay
// I/O to stdout / stderr, and SIGHUP/INT/QUIT/TERM signals. When
// the child calls Detach() we exit.
func Daemonize() error {
	devnull, err := getDevNullFile(3)
	if err != nil {
		return err
	}
	var rout, wout, rerr, werr *os.File
	rout, wout, err = os.Pipe()
	if err == nil {
		rerr, werr, err = os.Pipe()
	}
	if err != nil {
		return err
	}

	// now re-exec ourselves.
	binary, err := exec.LookPath(os.Args[0])
	if err != nil {
		return err
	}
	attrs := os.ProcAttr{
		Files: []*os.File{ devnull, wout, werr },
		Sys: &syscall.SysProcAttr{ Setsid: true },
	}
	os.Setenv(isDaemonEnv, "YES")
	proc, err := os.StartProcess(binary, os.Args, &attrs)
	os.Unsetenv(isDaemonEnv)
	if err != nil {
		return err
	}
	wout.Close()
	werr.Close()

	// and copy io from daemon.
	var wg sync.WaitGroup
	wg.Add(2)
	go copyIO(&wg, rout, os.Stdout)
	go copyIO(&wg, rerr, os.Stderr)
	go relaySignals(proc.Pid)
	wg.Wait()

	/// get exit status (if any)
	status := 0
	var wstatus syscall.WaitStatus
	_, err = syscall.Wait4(proc.Pid, &wstatus, syscall.WNOHANG, nil)
	if err == nil {
		if wstatus.Signaled() {
			status = 128 + int(wstatus.Signal())
		} else {
			status = wstatus.ExitStatus()
		}
	}
	os.Exit(status)
	return nil
}

// Returns true when this process is a daemonized child.
func IsDaemon() (bool) {
	return os.Getenv(isDaemonEnv) != ""
}

// Tell parent to exit.
func Detach() error {
	fh, err := getDevNullFile(3)
	if err != nil {
		return err
	}
	fd := int(fh.Fd())
	syscall.Dup2(fd, 1)
	syscall.Dup2(fd, 2)
	fh.Close()
	return nil
}

