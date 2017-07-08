
package main

import (
	"strings"
	"sync"
	"syscall"
	"time"
	"bazil.org/fuse"
)

const (
	RefIO = iota
	RefMeta
)

type Node struct {
	Dnode
	Atime		time.Time
	LastStat	time.Time
	Inode		uint64
	RefCount	[2]int
	Deleted		bool
	Parent		*Node
	Child		map[string]*Node
}

var rootNode = &Node{
	Inode:		1,
	Child:		make(map[string]*Node),
}

var inodeCounter = uint64(1)
var nodeMutex sync.Mutex
var lockRef = 0
var EBUSY = fuse.Errno(syscall.EBUSY)

func (nd *Node) Lock() {
	nodeMutex.Lock()
	lockRef++
	// dbgPrintf("node: Lock %s @ %p ref %d\n", nd.Name, nd, lockRef)
}

func (nd *Node) Unlock() {
	lockRef--
	// dbgPrintf("node: Unlock %s @ %p ref %d\n", nd.Name, nd, lockRef)
	nodeMutex.Unlock()
}

func (nd *Node) addNode(d Dnode) *Node {
	n := nd.Child[d.Name]
	if n != nil {
		n.Dnode = d
		return n
	}
	nn := &Node {
		Inode: inodeCounter,
		Dnode: d,
		Parent: nd,
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

func (de *Node) incIoRef() (err error) {
	for {
		de.Lock()
		if !de.doesMeta() {
			de.RefCount[RefIO]++
			de.Unlock()
			break
		}
		de.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return
}

func (de *Node) decIoRef() {
	de.Lock()
	de.RefCount[RefIO]--
	de.Unlock()
}

// Waits for i/o to cease, then increases metaref.
func (de *Node) incMetaRef() error {
	// first wait for other meta operations
	for {
		if !de.doesMeta() {
			de.RefCount[RefMeta]++
			break
		}
		de.Unlock()
		time.Sleep(10 * time.Millisecond)
		de.Lock()
	}
	// now wait for i/o operations to cease.
	for {
		if !de.doesIO() {
			break
		}
		de.Unlock()
		time.Sleep(10 * time.Millisecond)
		de.Lock()
	}
	// dbgPrintf("node: incMetaRef %s@%p: ref now %d\n", de.Name, de, de.RefCount[RefMeta])
	return nil
}

func (de *Node) incMetaRefThenLock() (err error) {
	de.Lock()
	err = de.incMetaRef()
	return
}

func (de *Node) decMetaRef() {
	de.RefCount[RefMeta]--
	// dbgPrintf("node: decMetaRef %s@%p: ref now %d\n", de.Name, de, de.RefCount[RefMeta])
}

