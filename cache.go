package main

import (
	"time"
)

func (nd *Node) statInfoFresh() bool {
	now := time.Now()
	return nd.LastStat.Add(statCacheTime).After(now)
}

func (nd* Node) statInfoTouch() {
	nd.LastStat = time.Now()
}

