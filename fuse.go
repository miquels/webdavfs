
package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/net/context"
	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

type FS struct {
	dav *DavClient
}

type NodeDir struct {
	Dnode
	Path	string
	dav	*DavClient
}

type NodeFile struct {
	Dnode
	Path	string
	dav	*DavClient
}

func attrSet(v fuse.SetattrValid, f fuse.SetattrValid) bool {
	return (v & f) > 0
}

func flagSet(v fuse.OpenFlags, f fuse.OpenFlags) bool {
	return (v & f) > 0
}

func NewFS(dav *DavClient) *FS {
	return &FS{ dav: dav }
}

func (fs *FS) Root() (fs.Node, error) {
	return &NodeDir{ dav: fs.dav, Path: "/" }, nil
}

func (nd *NodeDir) Attr(ctx context.Context, a *fuse.Attr) (err error) {
	fmt.Printf("nodedir.Attr %s\n", nd.Path)
	a.Inode = 0 // getInodeNum(nd.Path, true)
	a.Mode = os.ModeDir | 0755
	a.Ctime = nd.Ctime
	a.Mtime = nd.Mtime
	return
}

func (nd *NodeDir) Lookup(ctx context.Context, name string) (rn fs.Node, err error) {
	path := joinPath(nd.Path, name)
	dnode, err := nd.dav.Stat(path)
	if err != nil {
		fmt.Printf("Lookup %s failed: %s\n", path, err)
		return
	}
	fmt.Printf("Lookup %s ok\n", path)
	if dnode.IsDir {
		rn = &NodeDir{ Dnode: dnode, dav: nd.dav, Path: path }
	} else {
		rn = &NodeFile{ Dnode: dnode, dav: nd.dav, Path: path }
	}
	return
}

func (nd *NodeDir) ReadDirAll(ctx context.Context) (dd []fuse.Dirent, err error) {
	dirs, err := nd.dav.Readdir(nd.Path, false)
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
			Inode: 0, // getInodeNum(path, d.IsDir),
			Type: tp,
		})
	}
	return
}

func (nf *NodeFile) Attr(ctx context.Context, a *fuse.Attr) (err error) {
	fmt.Printf("nodefile.Attr %s\n", nf.Path)
	a.Inode = 0 // getInodeNum(nf.Path, false)
	a.Mode = 0644
	a.Size = nf.Size
	a.Ctime = nf.Ctime
	a.Mtime = nf.Mtime
	return
}

func (nf *NodeFile) Fsync(ctx context.Context, req *fuse.FsyncRequest) error {
	return nil
}

func (nf *NodeFile) Read(ctx context.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) (err error) {
	data, err := nf.dav.GetRange(nf.Path, req.Offset, req.Size)
	if err == nil {
		resp.Data = data
	}
	return
}

func (nf *NodeFile) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) (err error) {
	err = nf.dav.PutRange(nf.Path, req.Data, req.Offset)
	if err == nil {
		resp.Size = len(req.Data)
		sz := uint64(req.Offset) + uint64(len(req.Data))
		if sz > nf.Size {
			nf.Size = sz
		}
	}
	return
}

func (nf *NodeFile) Open(ctx context.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	dnode, err := nf.dav.Stat(nf.Path)
	fmt.Printf("Open: create %v, stat result %v\n", flagSet(req.Flags, fuse.OpenCreate), err)
	if err == nil && dnode.Size == nf.Size && dnode.Mtime == nf.Mtime {
		resp.Flags = fuse.OpenKeepCache
	}
	if err != nil && flagSet(req.Flags, fuse.OpenCreate) {
		err = nf.dav.PutRange(nf.Path, []byte{}, 0)
	}
	if err != nil {
		return nil, err
	}
	return nf, nil
}

func (nf *NodeFile) Setattr(ctx context.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) (err error) {
	v := req.Valid
	if attrSet(v, fuse.SetattrMode) ||
	   attrSet(v, fuse.SetattrUid) ||
	   attrSet(v, fuse.SetattrGid) {
		return fuse.EPERM
	}
	if attrSet(v, fuse.SetattrSize) {
		nf.Size = req.Size
	}
	// if attrSet(v, fuse.SetattrAtime) {
	// 	nf.Atime = req.Atime
	// }
	if attrSet(v, fuse.SetattrMtime){
		nf.Mtime = req.Mtime
	}
	attr := fuse.Attr{
		Size:	nf.Size,
		Blocks:	nf.Size / 512,
		// Atime: nf.Atime,
		Mtime: nf.Mtime,
		Ctime: nf.Ctime,
		Mode: 0644,
		Nlink: 1,
		Uid: 0,
		Gid: 0,
		BlockSize: 4096,
	}
	resp.Attr = attr
	return
}

func (nd *NodeDir) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (node fs.Node, handle fs.Handle, err error) {
	path := joinPath(nd.Path, req.Name)
	err = nd.dav.PutRange(path, []byte{}, 0)
	if err != nil {
		return
	}
	dnode, err := nd.dav.Stat(path)
	if err != nil {
		return
	}
	n := &NodeFile{
		Dnode: dnode,
		Path: path,
		dav: nd.dav,
	}
	node = n
	handle = n
	return
}

func (nd *NodeDir) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (ret fs.Node, err error) {
	path := nd.Path + req.Name + "/"
	err = nd.dav.Mkcol(path)
	if err == nil {
		now := time.Now()
		rnd := &NodeDir{
			dav: nd.dav,
			Path: path,
		}
		rnd.Name = req.Name
		rnd.Mtime = now
		rnd.Ctime = now
		rnd.IsDir = true
		ret = rnd
	}
	return
}

func (nd *NodeDir) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) (err error) {
	oldPath := joinPath(nd.Path, req.OldName)
	newPath := joinPath(newDir.(*NodeDir).Path, req.NewName)

	dnode, err := nd.dav.Stat(oldPath)
	if err != nil {
		return
	}
	if dnode.IsDir {
		oldPath += "/"
		newPath += "/"
	}
	err = nd.dav.Move(oldPath, newPath)
	return
}

func (nd *NodeDir) Remove(ctx context.Context, req *fuse.RemoveRequest) (err error) {
	path := joinPath(nd.Path, req.Name)
	err = nd.dav.Delete(path)
	return
}

