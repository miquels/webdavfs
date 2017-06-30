
package main

import (
	"os"
	"strings"
	"sync"

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

type inodeData struct {
	sync.Mutex
	count	uint64
	inode	map[string]uint64
}
var inodes *inodeData

func init() {
	inodes = &inodeData{
		inode: make(map[string]uint64),
	}
}

func getInode(path string) uint64 {
	if strings.HasSuffix(path, "/") {
		path = path[:len(path)-1]
	}
	inodes.Lock()
	defer inodes.Unlock()
	if d, ok := inodes.inode[path]; ok {
		return d
	}
	inodes.count++
	inodes.inode[path] = inodes.count
	return inodes.count
}

func NewFS(dav *DavClient) *FS {
	return &FS{ dav: dav }
}

func (fs *FS) Root() (fs.Node, error) {
	return &NodeDir{ dav: fs.dav, Path: "/" }, nil
}

func (nd *NodeDir) Attr(ctx context.Context, a *fuse.Attr) (err error) {
	a.Inode = getInode(nd.Path)
	a.Mode = os.ModeDir | 0555
	return
}

func (nd *NodeDir) Lookup(ctx context.Context, name string) (rn fs.Node, err error) {
	path := nd.Path + name
	dnode, err := nd.dav.Stat(path)
	if err != nil {
		return
	}
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
		path := nd.Path + d.Name
		tp := fuse.DT_File
		if (d.IsDir) {
			tp =fuse.DT_Dir
		}
		dd = append(dd, fuse.Dirent{
			Name: d.Name,
			Inode: getInode(path),
			Type: tp,
		})
	}
	return
}

func (nf *NodeFile) Attr(ctx context.Context, a *fuse.Attr) (err error) {
	a.Inode = getInode(nf.Path)
	a.Size = nf.Size
	a.Mode = 0444
	return
}

/*
func (File) ReadAll(ctx context.Context) ([]byte, error) {
	return []byte(greeting), nil
}
*/

