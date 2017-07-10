
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

var dbgChan = make(chan string, 8)
func init () {
	go func() {
		for {
			line := <-dbgChan
			os.Stdout.Write([]byte(line))
		}
	}()
}

func dbgPrintf(format string, args ...interface{}) {
	if opts.Debug {
		dbgChan <- fmt.Sprintf(format, args...)
	}
}

func dbgJson(obj interface{}) string {
	r, err := json.Marshal(obj)
	if err == nil {
		return string(r)
	}
	return fmt.Sprintf("%+v", obj)
}

