
package main

import (
	"errors"
	"encoding/json"
	"fmt"
	"os"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	T_WEBDAV	= 1 << iota
	T_HTTP_REQUEST
	T_HTTP_HEADERS
	T_FUSE
)

var traceOptions = uint32(0)
var timeFmt = "2006-01-02 15:04:05"
var traceFile *os.File

var traceChan = make(chan string, 8)

func startLogger (file *os.File, fileName string) {
	go func() {
		for {
			line := <-traceChan

			if file == traceFile {
				// first check if file was unlinked-
				// if so, stop logging.
				var fi, fi2 syscall.Stat_t
				err := syscall.Fstat(int(file.Fd()), &fi)
				if fi.Nlink == 0 {
					traceOptions = 0
					syscall.Ftruncate(int(file.Fd()), 0)
					file, _, _ = openDevNull()
				} else {
					// or, see if file was renamed.
					// if so, reopen.
					err = syscall.Stat(fileName, &fi2)
					if err != nil || fi.Ino != fi2.Ino {
						fh, err := unprivOpenFile(fileName,
							os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
						if err == nil {
							file = fh
						}
					}
				}
			}

			if file != nil {
				file.Write([]byte(line))
			}
		}
	}()
}

func trace(flags uint32) bool {
	return (traceOptions & flags) > 0
}

func tPrintf(format string, args ...interface{}) {
	t := time.Now().Format(timeFmt) + " "
	s := fmt.Sprintf(format, args...)
	if strings.Index(s, "\n") < 0 {
		s = t + s + "\n"
	} else {
		lines := strings.Split(s, "\n")
		l2 := []string{}
		for _, l := range lines {
			l2 = append(l2, t + l + "\n")
		}
		s = strings.Join(l2, "")
	}
	traceChan <- s
}

func tJson(obj interface{}) string {
	r, err := json.Marshal(obj)
	if err == nil {
		return string(r)
	}
	return fmt.Sprintf("%+v", obj)
}

func tHeaders(hdrs http.Header, prefix string) string {
	h := []string{}
	r := []string{}
	for n := range hdrs {
		h = append(h, n)
	}
	sort.Strings(h)
	for _, m := range h {
		r = append(r, prefix + m + ": " + strings.Join(hdrs[m], "\n") + "\n")
	}
	return strings.Join(r, "")
}

func unprivOpenFile(name string, flag int, perm os.FileMode) (fh *os.File, err error) {
	ruid := syscall.Getuid()
	euid := syscall.Geteuid()
	if ruid != euid {
		defer runtime.UnlockOSThread()
		runtime.LockOSThread()
		err = syscall.Setreuid(euid, ruid)
		if err != nil {
			return
		}
	}
	fh, err = os.OpenFile(name, flag, perm)
	if ruid != euid {
		err2 := syscall.Setreuid(ruid, euid)
		if err2 != nil {
			if err == nil {
				fh.Close()
			}
			err = err2
		}
	}
	return
}

func traceredirectStdoutErr() {
	if traceFile == nil {
		return
	}
	syscall.Dup2(int(traceFile.Fd()), 1)
	syscall.Dup2(int(traceFile.Fd()), 2)
}

func traceOpts(opt string, fn string) (err error)  {
	if opt == "" {
		return
	}
	opts := strings.Split(opt, ",")
	for _, o := range(opts) {
		switch o {
		case "webdav":
			traceOptions |= T_WEBDAV
		case "httpreq":
			traceOptions |= T_HTTP_REQUEST
		case "httphdr":
			traceOptions |= T_HTTP_HEADERS
		case "fuse":
			traceOptions |= T_FUSE
		default:
			err = errors.New("unknown trace option: " + o)
			return
		}
	}
	if traceOptions == 0 || fn == "" {
		startLogger(os.Stdout, "stdout")
		return
	}
	traceFile, err = unprivOpenFile(fn, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err == nil {
		startLogger(traceFile, fn)
	} else {
		startLogger(os.Stdout, "stdout")
	}
	return
}

