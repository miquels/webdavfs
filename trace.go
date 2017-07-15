
package main

import (
	"errors"
	"encoding/json"
	"fmt"
	"os"
	"net/http"
	"sort"
	"strings"
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

var traceChan = make(chan string, 8)
func init () {
	go func() {
		for {
			line := <-traceChan
			os.Stdout.Write([]byte(line))
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
	dbgChan <- s
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

func traceOpts(opt string) (err error)  {
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
		}
	}
	return
}

