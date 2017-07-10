
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

func relaySignals(wg *sync.WaitGroup, pid int, quit chan bool) {
	defer wg.Done()
	c := make(chan os.Signal, 8)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	for {
		select {
		case s := <-c:
			s2 := s.(syscall.Signal)
			syscall.Kill(pid, s2)
		case <-quit:
			return
		}
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

	// start goroutines to copy I/O and signals
	var wg1 sync.WaitGroup
	wg1.Add(2)
	go copyIO(&wg1, rout, os.Stdout)
	go copyIO(&wg1, rerr, os.Stderr)

	var wg2 sync.WaitGroup
	wg2.Add(1)
	quit := make(chan bool)
	go relaySignals(&wg2, proc.Pid, quit)

	wg1.Wait()
	quit <- true
	wg2.Wait()

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

