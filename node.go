
package main

import (
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

const (
	RefIO = iota
	RefMeta
)

type Node struct {
	Dnode
	Atime		time.Time
	LastStat	time.Time
	DirCache	map[string]Dnode
	DirCacheTime	time.Time
	Inode		uint64
	RefCount	[2]int
	Deleted		bool
	Parent		*Node
	Child		map[string]*Node
	InUse		bool
}

var rootNode = &Node{
	Inode:		1,
	Child:		make(map[string]*Node),
}

var EBUSY = fuse.Errno(syscall.EBUSY)
var nodeMutex sync.Mutex
var lockRef = 0
var lockTimer *time.Timer

func (nd *Node) Lock() {
	nodeMutex.Lock()
	if trace(T_LOCK) {
		name := nd.Name
		stack := debug.Stack()
		lockTimer = time.AfterFunc(2 * time.Second, func() {
			tPrintf("LOCKERR (%s) Lock held longer than 2 seconds:\n%s",
				name, stack)
		})
	}
	lockRef++
	// dbgPrintf("node: Lock %s @ %p ref %d\n", nd.Name, nd, lockRef)
}

func (nd *Node) Unlock() {
	if trace(T_LOCK) {
		if lockRef != 1 {
			tPrintf("LOCKERR unlock: lockRef %d != 1\n%s",
				lockRef, debug.Stack())
		}
		if lockTimer == nil {
			tPrintf("LOCKERR unlock: lockTimer == nil\n%s",
				debug.Stack())
		} else {
			lockTimer.Stop()
			lockTimer = nil
		}
	}
	lockRef--
	// dbgPrintf("node: Unlock %s @ %p ref %d\n", nd.Name, nd, lockRef)
	nodeMutex.Unlock()
}

func (nd *Node) addNode(d Dnode, really bool) *Node {
	n := nd.Child[d.Name]
	if n != nil {
		if really {
			n.InUse = true
		}
		n.LastStat = time.Now()
		n.Dnode = d
		return n
	}
	nn := &Node {
		Inode: fs.GenerateDynamicInode(nd.Inode, d.Name),
		Dnode: d,
		Parent: nd,
		InUse: really,
		LastStat: time.Now(),
	}
	if d.IsDir {
		nn.Child = map[string]*Node{}
	}
	nd.Child[d.Name] = nn
	// dbgPrintf("node: addNode %s @ %p to %s @ %p\n", nn.Name, nn, nd.Name, nd)
	return nn
}

func (nd *Node) delNode(name string) {
	n := nd.getNode(name)
	if n != nil {
		// dbgPrintf("node: delNode %s @ %p from %s @ %p\n", n.Name, n, nd.Name, nd)
		n.Name = n.Name + " (deleted)"
		n.Deleted = true
		delete(nd.Child, name)
	} else {
		// dbgPrintf("node: delNode %s from %s @ %p: not found\n", name, nd.Name, nd)
	}
}

func (nd *Node) forgetNode() {
	if nd.Parent != nil {
		// dbgPrintf("node: forgetNode %s @ %p from %s %p\n", nd.Name, nd, nd.Parent.Name, nd.Parent)
		// paranoia - check should always succeed.
		parent := nd.Parent
		if parent != nil && parent.Child[nd.Name] == nd {
			delete(nd.Parent.Child, nd.Name)
		}
	} else {
		// dbgPrintf("node: forgetNode %s @ %p (no parent)\n", nd.Name, nd)
	}
}

func (nd *Node) moveNode(dest *Node, oldName string, newName string) {
	dest.delNode(newName)
	cn := nd.getNode(oldName)
	// dbgPrintf("node: moveNode %s@%p/%s@%p -> %s@%p/%s\n", nd.getPath(), nd, oldName, cn, dest.getPath(), dest, newName)
	if cn != nil {
		delete(nd.Child, oldName)
		cn.Name = newName
		cn.Parent = dest
		dest.Child[newName] = cn
	}
}

func (nd *Node) getNode(name string) *Node {
	if nd.Child != nil {
		return nd.Child[name]
	}
	return nil
}

func (nd *Node) deleteUnusedChildren() {
	for name, nn := range nd.Child {
		nn.deleteUnusedChildren()
		if !nn.InUse && len(nn.Child) == 0 {
			delete(nd.Child, name)
		}
	}
}

func (nd *Node) invalidateThisNode() {
	nd.deleteUnusedChildren()
	if !nd.InUse && len(nd.Child) == 0 {
		nd.Forget()
	}
}

func (nd *Node) invalidateNode(name string) {
	nn := nd.Child[name]
	if nn != nil {
		nn.invalidateThisNode()
	}
}

func lookupNode(path string) (de *Node) {
	d := rootNode
	if path != "/" {
		pelem := strings.Split(path[1:], "/")
		for _, n := range(pelem) {
			if d.Child == nil || d.Child[n] == nil {
				return
			}
			d = d.Child[n]
		}
	}
	de = d
	return
}

func (de *Node) getPath() string {
	if de.Parent == nil {
		return "/"
	}
	a := make([]string, 0, 16)
	for d := de; d != nil && d.Parent != nil; d = d.Parent {
		a = append(a, d.Name)
	}
	// reverse
	for i, j := 0, len(a) - 1; i < j; i, j = i + 1, j - 1 {
		a[i], a[j] = a[j], a[i]
	}
	path := "/" + strings.Join(a, "/")
	// dbgPrintf("node: getPath %p -> %s\n", de, path)
	return path
}

// no IO operations must be going on at this node or above.
// perhaps use a global refcount as well - faster
func (de *Node) doesIO() bool {
	if de.RefCount[RefIO] > 0 {
		return true
	}
	for _, d := range de.Child {
		if d.doesIO() {
			return true
		}
	}
	return false
}

// no meta operations can be going on at this node or below.
// perhaps use a global refcount as well - faster
func (de *Node) doesMeta() bool {
	for d := de; d != nil; d = d.Parent {
		if d.RefCount[RefMeta] > 0 {
			return true
		}
	}
	return false
}

func (de *Node) incIoRef(id fuse.RequestID) (err error) {
	count := 0
	for {
		de.Lock()
		if !de.doesMeta() {
			de.RefCount[RefIO]++
			de.Unlock()
			break
		}
		de.Unlock()
		time.Sleep(10 * time.Millisecond)
		if trace(T_LOCK) {
			count++
			if count > 300 {
				tPrintf("%d LOCKERR incIoRef(%s) locked for 3 secs", id, de.Name)
				count = 0
			}
		}
	}
	return
}

func (de *Node) decIoRef() {
	de.Lock()
	de.RefCount[RefIO]--
	de.Unlock()
}

// Waits for i/o to cease, then increases metaref.
func (de *Node) incMetaRef(id fuse.RequestID) error {
	// first wait for other meta operations
	count := 0
	for {
		if !de.doesMeta() {
			de.RefCount[RefMeta]++
			break
		}
		de.Unlock()
		time.Sleep(10 * time.Millisecond)
		if trace(T_LOCK) {
			count++
			if count > 200 {
				tPrintf("%d LOCKERR incMetaRef(%s) metawait locked for 2 secs", id, de.Name)
				count = 0
			}
		}
		de.Lock()
	}
	// now wait for i/o operations to cease.
	count = 0
	for {
		if !de.doesIO() {
			break
		}
		de.Unlock()
		time.Sleep(10 * time.Millisecond)
		if trace(T_LOCK) {
			count++
			if count > 200 {
				tPrintf("%d LOCKERR incMetaRef(%s) iowait locked for 2 secs", id, de.Name)
				count = 0
			}
		}
		de.Lock()
	}
	// dbgPrintf("node: incMetaRef %s@%p: ref now %d\n", de.Name, de, de.RefCount[RefMeta])
	return nil
}

func (de *Node) incMetaRefThenLock(id fuse.RequestID) (err error) {
	de.Lock()
	err = de.incMetaRef(id)
	return
}

func (de *Node) decMetaRef() {
	de.RefCount[RefMeta]--
	// dbgPrintf("node: decMetaRef %s@%p: ref now %d\n", de.Name, de, de.RefCount[RefMeta])
}

