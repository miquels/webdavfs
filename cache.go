package main

import (
	"time"
)

func (nd *Node) getCachedDnode(name string) (dnode Dnode, cached bool) {
	now := time.Now()

	// empty is "me"
	if name == "" {
		// recently statted?
		if nd.LastStat.Add(statCacheTime).After(now) {
			dnode = nd.Dnode
			cached = true
			return
		}
		// try directory cache
		name = nd.Name
		nd = nd.Parent
		if nd == nil {
			return
		}
	}

	// do we have a recent directory lookup cache?
	if nd.DirCache != nil {
		if nd.DirCacheTime.Add(dirCacheTime).Before(time.Now()) {
			dbgPrintf("fuse: getCachedDnode: %s: dircache stale\n", name)
			nd.DirCache = nil
		} else {
			dnode, cached = nd.DirCache[name]
			dbgPrintf("fuse: getCachedDnode: %s: dircache: %v\n", name, cached)
		}
	} else {
		dbgPrintf("fuse: getCachedDnode: %s: (no dircache)\n", name)
	}
	return
}

func (nd* Node) lastStatNow() {
	nd.LastStat = time.Now()
}

func (nd* Node) lastStatAt(t time.Time) {
	nd.LastStat = t
}

func (nd* Node) lastStatInvalid() {
	nd.LastStat = time.Time{}
}

func (nd *Node) addCachedDnodes(dirs []Dnode) {
	nd.DirCache = map[string]Dnode{}
	for _, d := range dirs {
		nd.DirCache[d.Name] = d
	}
	nd.DirCacheTime = time.Now()
}

