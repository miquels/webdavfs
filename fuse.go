
package main

import (
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/context"
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

const (
	dirCacheTime   = 10 * time.Second
	statCacheTime  = 1 * time.Second
	attrValidTime  = 1 * time.Minute
	entryValidTime = 1 * time.Minute
)

type FS struct {
	root	*Node
}
var dav *DavClient

func attrSet(v fuse.SetattrValid, f fuse.SetattrValid) bool {
	return (v & f) > 0
}

func flagSet(v fuse.OpenFlags, f fuse.OpenFlags) bool {
	return (v & f) > 0
}

func NewFS(d *DavClient) *FS {
	dav = d
	return &FS{ root: rootNode }
}

func (fs *FS) Root() (fs.Node, error) {
	/*
	dnode, err := dav.Stat("/")
	if err == nil {
		fs.root.Dnode = dnode
	}
	*/
	return fs.root, nil
}

func (nd *Node) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (ret fs.Node, err error) {
	nd.incMetaRefThenLock()
	path := joinPath(nd.getPath(), req.Name)
	nd.Unlock()
	err = dav.Mkcol(path + "/")
	nd.Lock()
	if err == nil {
		now := time.Now()
		nn := Dnode{
			Name: req.Name,
			Mtime: now,
			Ctime: now,
			IsDir: true,
		}
		n := nd.addNode(nn)
		n.Atime = now
		ret = n
	}
	nd.decMetaRef()
	nd.Unlock()
	return
}

func (nd *Node) Rename(ctx context.Context, req *fuse.RenameRequest, destDir fs.Node) (err error) {
	var lock1, lock2 *Node
	var oldPath, newPath string
	destNode := destDir.(*Node)
	first := true

	// Check if paths overlap. If so, only lock the
	// shortest path. If not, lock both.
	//
	// Need to do this in a loop, every time checking if this
	// condition still holds after both paths are locked.
	nd.Lock()
	for {
		srcDirPath := nd.getPath()
		dstDirPath := destNode.getPath()
		oldPath = joinPath(srcDirPath, req.OldName)
		newPath = joinPath(dstDirPath, req.NewName)

		var newLock1, newLock2 *Node
		if srcDirPath == dstDirPath {
			newLock1 = nd
		} else if strings.HasPrefix(srcDirPath, dstDirPath) {
			newLock1 = destNode
		} else if strings.HasPrefix(dstDirPath, srcDirPath) {
			newLock1 = nd
		} else {
			newLock1 = nd
			newLock2 = destNode
		}

		if !first {
			if lock1 == newLock1 && lock2 == newLock2 {
				break
			}
			lock1.decMetaRef()
			if lock2 != nil {
				lock2.decMetaRef()
			}
		}
		first = false

		lock1, lock2 = newLock1, newLock2
		lock1.incMetaRef()
		if lock2 != nil {
			lock2.incMetaRef()
		}
	}

	dbgPrintf("fuse: Rename %s -> %s\n", oldPath, newPath)

	isDir := false
	node := nd.getNode(req.OldName)
	if node == nil {
		// don't have the source node cached- need to
		// find out if it's a dir or not, so stat.
		nd.Unlock()
		var dnode Dnode
		dnode, err = dav.Stat(oldPath)
		isDir = dnode.IsDir
	} else {
		isDir = node.IsDir
		nd.Unlock()
	}

	if err == nil {
		if isDir {
			oldPath += "/"
			newPath += "/"
		}
		err = dav.Move(oldPath, newPath)
	}

	nd.Lock()
	if err == nil {
		nd.moveNode(destNode, req.OldName, req.NewName)
	}
	lock1.decMetaRef()
	if lock2 != nil {
		lock2.decMetaRef()
	}
	nd.Unlock()
	return
}

func (nd *Node) Remove(ctx context.Context, req *fuse.RemoveRequest) (err error) {
	nd.incMetaRefThenLock()
	path := joinPath(nd.getPath(), req.Name)
	nd.Unlock()
	props, err := dav.PropFindWithRedirect(path, 1, nil)
	if err == nil {
		if len(props) != 1 {
			if req.Dir {
				err = fuse.Errno(syscall.ENOTEMPTY)
			} else {
				err = fuse.EIO
			}
		}
		if err == nil {
			isDir := false
			for _, p := range props {
				if p.ResourceType == "collection" {
					isDir = true
				}
			}
			if req.Dir && !isDir {
				err = fuse.Errno(syscall.ENOTDIR)
			}
			if !req.Dir && isDir {
				err = fuse.Errno(syscall.EISDIR)
			}
		}
	}
	if err == nil {
		if req.Dir {
			path += "/"
		}
		err = dav.Delete(path)
	}
	nd.Lock()
	if err == nil {
		nd.delNode(req.Name)
	}
	nd.decMetaRef()
	nd.Unlock()
	return
}

func (nd *Node) Attr(ctx context.Context, attr *fuse.Attr) (err error) {
	if nd.Deleted {
		err = fuse.Errno(syscall.ESTALE)
		return
	}

	nd.incIoRef()

	// see if we have a Dnode cached, if not, call dav.Stat()
	dnode, cached := nd.getCachedDnode("")
	if !cached {
		dbgPrintf("fuse: Getattr %s (%s)\n", nd.Name, nd.getPath())
		dnode, err = dav.Stat(nd.getPath())
		if err == nil {
			nd.lastStatNow()
		}
	} else {
		dbgPrintf("fuse: Getattr %s (%s) (cached)\n", nd.Name, nd.getPath())
	}

	if err == nil {

		// Sanity check.
		if nd.Name != "" && dnode.IsDir != nd.IsDir {
			dbgPrintf("fuse: Getattr %s isdir %v != isdir %v\n", dnode.Name, dnode.IsDir, nd.IsDir)
			nd.lastStatInvalid()
			err = fuse.Errno(syscall.ESTALE)
		} else {
			// All well, build fuse.Attr.
			nd.Dnode = dnode
			mode := os.FileMode(0644)
			if dnode.IsDir {
				mode = os.FileMode(0755 | os.ModeDir)
			}
			if nd.Atime.Before(nd.Mtime) {
				nd.Atime = nd.Mtime
			}
			*attr = fuse.Attr{
				Valid: attrValidTime,
				Size: nd.Size,
				Blocks: (nd.Size + 511) / 512,
				Atime: nd.Atime,
				Mtime: nd.Mtime,
				Ctime: nd.Ctime,
				Crtime: nd.Ctime,
				Mode: os.FileMode(mode),
				Nlink: 1,
				Uid: 0,
				Gid: 0,
				BlockSize: 4096,
			}
			// dbgPrintf("fuse: Getattr: return stat: %+v\n", attr)
		}
	} else {
		// dbgPrintf("fuse: Getattr: stat failed %v\n", err)
	}
	nd.decIoRef()
	return
}

func (nd *Node) Lookup(ctx context.Context, name string) (rn fs.Node, err error) {
	dbgPrintf("fuse: Lookup %s in %s\n", name, nd.Name)
	nd.incIoRef()
	var updateTime time.Time

	// do we have a recent entry in the cache?
	nd.Lock()
	dnode, cached := nd.getCachedDnode(name)
	if cached {
		updateTime = nd.DirCacheTime
	}
	nd.Unlock()

	// if not, call Stat()
	if !cached {
		path := joinPath(nd.getPath(), name)
		dnode, err = dav.Stat(path)
		updateTime = time.Now()
	}

	if err == nil {
		dbgPrintf("fuse: Lookup ok add %s\n", name)
		node := nd.addNode(dnode)
		nd.lastStatAt(updateTime)
		rn = node
	} else {
		dbgPrintf("fuse: Lookup %d failed: %s\n", name, err)
	}
	nd.decIoRef()
	return
}

func (nd *Node) ReadDirAll(ctx context.Context) (dd []fuse.Dirent, err error) {
	nd.incIoRef()
	path := nd.getPath()
	dbgPrintf("fuse: ReadDirAll %s\n", nd.Name)
	dirs, err := dav.Readdir(path, true)
	if err == nil {
		nd.Lock()
		nd.addCachedDnodes(dirs)
		nd.Unlock()
	}
	nd.decIoRef()
	if err != nil {
		return
	}
	for _, d := range dirs {
		tp := fuse.DT_File
		if (d.IsDir) {
			tp =fuse.DT_Dir
		}
		dd = append(dd, fuse.Dirent{
			Name: d.Name,
			Inode: 0,
			Type: tp,
		})
	}
	return
}

func (nd *Node) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (node fs.Node, handle fs.Handle, err error) {
	nd.incMetaRefThenLock()
	path := nd.getPath()
	nd.Unlock()
	trunc := flagSet(req.Flags, fuse.OpenTruncate)
	read  := req.Flags.IsReadWrite() || req.Flags.IsReadOnly()
	write := req.Flags.IsReadWrite() || req.Flags.IsWriteOnly()
	excl  := flagSet(req.Flags, fuse.OpenExclusive)
	dbgPrintf("fuse: Create %s: trunc %v read %v write %v excl %v\n", req.Name, trunc, read, write, excl)
	path = joinPath(path, req.Name)
	created := false
	if trunc {
		// A simple put with no body creates and truncates the
		// file if it's not there.
		created, err = dav.Put(path, []byte{}, true, excl)
	} else {
		// A Put-Range at offset 0 with an empty body
		// creates the file if not present, but doesn't
		// truncate it.
		created, err = dav.PutRange(path, []byte{}, 0, true, excl)
	}
	if err == nil && excl && !created {
		err = fuse.EEXIST
	}
	if err == nil {
		dnode, err := dav.Stat(path)
		if err == nil {
			n := nd.addNode(dnode)
			node = n
			handle = n
		}
	}
	nd.Lock()
	nd.decMetaRef()
	nd.Unlock()
	return
}


func (nd *Node) Forget() {
	// XXX FIXME add some sanity checks here-
	// see if refcnt == 0, subdirs are gone
	nd.Lock()
	nd.forgetNode()
	nd.Unlock()
}

func (nd *Node) ftruncate(ctx context.Context, size uint64) (err error) {
	nd.incMetaRefThenLock()
	path := nd.getPath()
	nd.Unlock()
	if size == 0 {
		if nd.Size > 0 {
			_, err = dav.Put(path, []byte{}, false, false)
		}
	} else if size > nd.Size {
		_, err = dav.PutRange(path, []byte{0}, int64(size - 1), false, false)
	} else if size != nd.Size {
		err = fuse.ERANGE
	}
	nd.Lock()
	if err == nil {
		nd.Size= size
	}
	nd.decMetaRef()
	nd.Unlock()
	return
}

func (nd *Node) Setattr(ctx context.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) (err error) {
	if nd.Deleted {
		err = fuse.Errno(syscall.ESTALE)
		return
	}
	v := req.Valid
	if attrSet(v, fuse.SetattrMode) ||
	   attrSet(v, fuse.SetattrUid) ||
	   attrSet(v, fuse.SetattrGid) {
		return fuse.EPERM
	}

	if attrSet(v, fuse.SetattrSize) {
		// dbgPrintf("fuse: Setattr %s: size %d\n", nd.Name, req.Size)
		err = nd.ftruncate(ctx, req.Size)
		if err != nil {
			return
		}
	}

	nd.Lock()
	if attrSet(v, fuse.SetattrAtime) {
		nd.Atime = req.Atime
	}
	if attrSet(v, fuse.SetattrMtime){
		// XXX FIXME if req.Mtime is around "now", perhaps
		// do a 0-byte range put to "touch" the file.
		nd.Mtime = req.Mtime
	}
	attr := fuse.Attr{
		Valid: attrValidTime,
		Size:	nd.Size,
		Blocks:	nd.Size / 512,
		Atime: nd.Atime,
		Mtime: nd.Mtime,
		Ctime: nd.Ctime,
		Crtime: nd.Ctime,
		Mode: 0644,
		Nlink: 1,
		Uid: 0,
		Gid: 0,
		BlockSize: 4096,
	}
	resp.Attr = attr
	nd.Unlock()
	return
}

func (nf *Node) Fsync(ctx context.Context, req *fuse.FsyncRequest) (err error) {
	if nf.Deleted {
		err = fuse.Errno(syscall.ESTALE)
		return
	}
	return nil
}

func (nf *Node) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) (err error) {
	if nf.Deleted {
		err = fuse.Errno(syscall.ESTALE)
		return
	}
	nf.incIoRef()
	defer nf.decIoRef()
	nf.Lock()
	toRead := int64(nf.Size) - req.Offset
	nf.Unlock()
	if toRead <= 0 {
		resp.Data = []byte{}
		return
	}
	if toRead > int64(req.Size) {
		toRead = int64(req.Size)
	}
	path := nf.getPath()
	data, err := dav.GetRange(path, req.Offset, int(toRead))
	if err == nil {
		resp.Data = data
	}
	return
}

func (nf *Node) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) (err error) {
	if nf.Deleted {
		dbgPrintf("fuse: Write: %s (node @ %p) DELETED\n", nf.Name, nf)
		err = fuse.Errno(syscall.ESTALE)
		return
	}
	if len(req.Data) == 0 {
		resp.Size = 0
		return
	}
	nf.incIoRef()
	path := nf.getPath()
	dbgPrintf("fuse: Write: %s (node @ %p)\n", path, nf)
	_, err = dav.PutRange(path, req.Data, req.Offset, false, false)
	if err == nil {
		resp.Size = len(req.Data)
		sz := uint64(req.Offset) + uint64(len(req.Data))
		nf.Lock()
		if sz > nf.Size {
			nf.Size = sz
		}
		nf.Unlock()
	} else {
		dbgPrintf("fuse: Write: failed: %v\n", err)
	}
	nf.decIoRef()
	return
}

func (nf *Node) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if nf.IsDir {
		return nf, nil
	}
	// truncate if we need to.
	trunc := flagSet(req.Flags, fuse.OpenTruncate)
	read  := req.Flags.IsReadWrite() || req.Flags.IsReadOnly()
	write := req.Flags.IsReadWrite() || req.Flags.IsWriteOnly()
	dbgPrintf("fuse: Open %s: trunc %v read %v write %v\n", nf.Name, trunc, read, write)

	nf.incIoRef()
	path := nf.getPath()

	// See if cache is still valid.
	dnode, err := dav.Stat(path)
	if err == nil {
		nf.Lock()
		nf.Dnode = dnode
		nf.lastStatNow()
		if dnode.Size == nf.Size && dnode.Mtime == nf.Mtime {
			resp.Flags = fuse.OpenKeepCache
		}
		nf.Unlock()
	}
	err = nil

	// This is actually not called, truncating is
	// done by calling Setattr with 0 size.
	if trunc {
		_, err = dav.Put(path, []byte{}, false, false)
		if err == nil {
			nf.Size = 0
		}
	}

	nf.decIoRef()

	if err != nil {
		return nil, err
	}
	return nf, nil
}

