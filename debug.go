
package main

import (
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
	dbgChan <- fmt.Sprintf(format, args...)
}

