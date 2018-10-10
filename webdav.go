package main;

import (
	"fmt"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"bazil.org/fuse"
)

type davEmpty struct {}
type davSem chan davEmpty

type DavClient struct {
	Url		string
	Username	string
	Password	string
	Cookie		string
	Methods		map[string]bool
	DavSupport	map[string]bool
	IsSabre		bool
	IsApache	bool
	PutDisabled	bool
	MaxConns	int
	MaxIdleConns	int
	base		string
	cc		*http.Client
	davSem		davSem
}

type DavError struct {
	Code		int
	Message		string
	Location	string
	Errnum		syscall.Errno
}

type Dnode struct {
	Name		string
	Target		string
	IsDir		bool
	IsLink		bool
	Mtime		time.Time
	Ctime		time.Time
	Size		uint64
}

type Props struct {
	Name		string		`xml:"-"`
	ResourceType_	ResourceType	`xml:"resourcetype"`
	RefTarget_	RefTarget	`xml:"reftarget"`
	ResourceType	string		`xml:"-"`
	RefTarget	string		`xml:"-"`
	CreationDate	string		`xml:"creationdate"`
	LastModified	string		`xml:"getlastmodified"`
	Etag		string		`xml:"getetag"`
	ContentLength	string		`xml:"getcontentlength"`
	SpaceUsed	string		`xml:"quota-used-bytes"`
	SpaceFree	string		`xml:"quota-available-bytes"`
}

type ResourceType struct {
	Collection	*struct{}	`xml:"collection"`
	RedirectRef	*struct{}	`xml:"redirectref"`
}

type RefTarget struct {
	Href		*string		`xml:"href"`
}

type Propstat struct {
	Props		*Props		`xml:"prop"`
}

type Response struct {
	Href		string		`xml:"href"`
	Propstat	*Propstat	`xml:"propstat"`
}

type MultiStatus struct {
	Responses	[]Response	`xml:"response"`
}

var mostProps = "<D:resourcetype/><D:creationdate/><D:getlastmodified/><D:getetag/><D:getcontentlength/>"

var davTimeFormat = "2006-01-02T15:04:05Z"

var davToErrnoMap = map[int]syscall.Errno{
	403:	syscall.EACCES,
	404:	syscall.ENOENT,
	405:	syscall.EACCES,
	408:	syscall.ETIMEDOUT,
	409:	syscall.ENOENT,
	416:	syscall.ERANGE,
	504:	syscall.ETIMEDOUT,
}

var userAgent string

func init() {
	userAgent = fmt.Sprintf("fuse-webdavfs/0.1 (Go) %s (%s)", runtime.GOOS, runtime.GOARCH)
}

func davToErrno(err *DavError) (*DavError) {
	if fe, ok := davToErrnoMap[err.Code]; ok {
		err.Errnum = fe
		return err
	}
	err.Errnum = syscall.EIO
	return err
}

func statusIsValid(resp *http.Response) bool {
	return resp.StatusCode / 100 == 2
}

func statusIsRedirect(resp *http.Response) bool {
	return resp.StatusCode / 100 == 3
}

func stripQuotes(s string) string {
	l := len(s)
	if l > 1 && s[0] == '"' && s [l-1] == '"' {
		return s[1:l-1]
	}
	return s
}

func stripLastSlash(s string) string {
	l := len(s)
	for l > 0 {
		if s[l-1] != '/' {
			return s[:l]
		}
		l--
	}
	return s
}

func addSlash(s string) string {
	if len(s) > 0 && s[len(s)-1] != '/' {
		s += "/"
	}
	return s
}

func dirName(s string) string {
	s = stripLastSlash(s)
	i := strings.LastIndex(s, "/")
	if i > 0 {
		return s[:i]
	}
	return "/"
}

func parseTime (s string) (t time.Time) {
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		t, _ = time.Parse(davTimeFormat, s)
	} else {
		t, _ = http.ParseTime(s)
	}
	return
}

func joinPath(s1, s2 string) string {
	if (len(s1) > 0 && s1[len(s1)-1] == '/') ||
	   (len(s2) > 0 && s2[0] == '/') {
		return s1 + s2
	}
	return s1 + "/" + s2
}

func stripHrefPrefix(href string, prefix string) (string, bool) {
	u, _ := url.ParseRequestURI(href)
	if u == nil {
		return "", false
	}
	name := u.Path
	if strings.HasPrefix(name, prefix) {
		name = name[len(prefix):]
	}
	i := strings.Index(name, "/")
	if i >= 0 && i < len(name) - 1 {
		return "", false
	}
	return name, true
}

func mapLine(s string) (m map[string]bool) {
	m = make(map[string]bool)
	elems := strings.Split(s, ",")
	for _, e := range elems {
		e = strings.TrimSpace(e)
		if e != "" {
			m[e] = true
		}
	}
	return
}

func getHeader(h http.Header, key string) string {
	key = http.CanonicalHeaderKey(key)
	return strings.Join(h[key], ",")
}

func drainBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	b := make([]byte, 256)
	var err error
	for err != io.EOF {
		_, err = resp.Body.Read(b)
	}
	resp.Body.Close()
	resp.Body = nil
}

func (d *DavError) Errno() fuse.Errno {
	return fuse.Errno(d.Errnum)
}

func (d *DavError) Error() string {
	return d.Message
}

func (d *DavClient) semAcquire() {
	if d.MaxConns > 0 {
		d.davSem <- davEmpty{}
	}
}

func (d *DavClient) semRelease() {
	if d.MaxConns > 0 {
		<-d.davSem
	}
	return
}

func (d *DavClient) buildRequest(method string, path string, b ...interface{}) (req *http.Request, err error) {
	if len(path) == 0 || path[0] != '/' {
		err = errors.New("path does not start with /")
		return
	}
	var body io.Reader
	blen := 0
	if len(b) > 0 && b[0] != nil {
		switch v := b[0].(type) {
		case string:
			body = strings.NewReader(v)
			blen = len(v)
		case []byte:
			body = bytes.NewReader(v)
			blen = len(v)
		default:
			body = v.(io.Reader)
			blen = -1
		}
	}
	u := url.URL{ Path: path }
	req, err = http.NewRequest(method, d.Url + u.EscapedPath(), body)
	if err != nil {
		return
	}
	if (blen >= 0) {
		req.Header.Set("Content-Length", fmt.Sprintf("%d", blen))
	}
	if d.Username != "" || d.Password != "" {
		req.SetBasicAuth(d.Username, d.Password)
	}
	if d.Cookie != "" {
		req.Header.Set("Cookie", d.Cookie)
	}
	return
}

func (d *DavClient) request(method string, path string, b ...interface{}) (*http.Response, error) {
	req, err := d.buildRequest(method, path, b...)
	if err != nil {
		return nil, err
	}
	return d.do(req)
}

func (d *DavClient) do(req *http.Request) (resp *http.Response, err error) {
	req.Header.Set("User-Agent", userAgent)

	if trace(T_HTTP_REQUEST) {
		tPrintf("%s %s HTTP/1.1", req.Method, req.URL.String())
		if trace(T_HTTP_HEADERS) {
			tPrintf("%s", tHeaders(req.Header, " "))
		}
		defer func() {
			if err != nil {
				tPrintf("%s request error: %v", req.Method, err)
			} else {
				tPrintf("%s %s", resp.Proto, resp.Status)
				if trace(T_HTTP_HEADERS) {
					tPrintf("%s", tHeaders(resp.Header, " "))
				}
			}
		}()
	}

	resp, err = d.cc.Do(req)
	if err == nil && !statusIsValid(resp) {
		err = davToErrno(&DavError{
			Message: resp.Status,
			Code: resp.StatusCode,
			Location: resp.Header.Get("Location"),
		})
	}
	return
}

func (d *DavClient) Mount() (err error) {
	if d.cc == nil {
		d.Url = stripLastSlash(d.Url)
		var u *url.URL
		u, err = url.ParseRequestURI(d.Url)
		if err != nil {
			return
		}
		d.base = u.Path

		if d.MaxConns > 0 {
			d.davSem = make(davSem, d.MaxConns)
		}
		// Override some values from DefaultTransport.
		tr := *(http.DefaultTransport.(*http.Transport))
		tr.MaxIdleConnsPerHost = d.MaxIdleConns
		tr.DisableCompression = true

		d.cc = &http.Client{
			Timeout: 60 * time.Second,
			Transport: &tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("400 Will not follow redirect")
			},
		}
	}
	req, err := d.buildRequest("OPTIONS", "/")
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "*/*")
	resp, err := d.do(req)
	defer drainBody(resp)
	if err != nil {
		return
	}
	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
		return
	}

	// Parse headers.
	d.Methods = mapLine(getHeader(resp.Header, "Allow"))
	d.DavSupport = mapLine(getHeader(resp.Header, "Dav"))

	// Is this apache with mod_dav?
	isApache := strings.Index(resp.Header.Get("Server"), "Apache") >= 0
	if isApache && d.DavSupport["<http://apache.org/dav/propset/fs/1>"] {
		d.IsApache = true
	}

	// Does this server supoort sabredav-partialupdate ?
	if d.DavSupport["sabredav-partialupdate"] {
		d.IsSabre = true
	}

	if !d.DavSupport["1"] {
		err = errors.New("not a webdav server")
	}

	// check if it exists and is a directory.
	if err == nil {
		var dnode Dnode
		dnode, err = d.Stat("/")
		if err == nil && !dnode.IsDir {
			err = errors.New(d.Url + " is not a directory")
		}
	}

	return
}

func (d *DavClient) PropFind(path string, depth int, props []string) (ret []*Props, err error) {

	d.semAcquire()
	defer d.semRelease()

	if trace(T_WEBDAV) {
		tPrintf("Propfind(%s, %d, %v)", path, depth, props)
		defer func() {
			if err != nil {
				tPrintf("Propfind: %v", err)
				return
			}
			tPrintf("Propfind: returns %v", tJson(ret))
		}()
	}

	a := append([]string{}, `<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D='DAV:'>`)
	if len(props) == 0 {
		a = append(a, "<D:prop>")
		a = append(a, mostProps)
		if d.DavSupport["redirectrefs"] {
			a = append(a, "<D:reftarget/>")
		}
		a = append(a, "</D:prop>")
	} else if len(props) == 1 && props[0] == "allprop" {
		a = append(a, "<D:allprop/>")
	} else {
		a = append(a, "<D:prop>")
		for _, s := range props {
			a = append(a, "<D:" + s + "/>")
		}
		a = append(a, "</D:prop>")
	}
	a = append(a, `</D:propfind>`)
	x := strings.Join(a, "")

	req, err := d.buildRequest("PROPFIND", path, x)
	if err != nil {
		return
	}
	dp := "0"
	if depth > 0 {
		dp = "1"
	}
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Depth", dp)
	if d.DavSupport["redirectrefs"] {
		req.Header.Set("Apply-To-Redirect-Ref", "T")
	}
	resp, err := d.do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
		return
	}

	contents, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	obj := MultiStatus{}
	err = xml.Unmarshal(contents, &obj)
	if err != nil {
		return
	}
	if obj.Responses == nil || len(obj.Responses) == 0 {
		err = errors.New("XML decode error")
		return
	}

	prefix := d.base + path
	if depth == 0 {
		prefix = dirName(prefix)
		if prefix != "/" {
			prefix += "/"
		}
	}

	for _, respTag := range obj.Responses {
		if respTag.Propstat == nil || respTag.Propstat.Props == nil {
			err = errors.New("XML decode error")
			return
		}
		props := respTag.Propstat.Props
		props.Etag = stripQuotes(props.Etag)

		// make sure collection hrefs end in '/'
		if props.ResourceType_.Collection != nil {
			props.ResourceType = "collection"
			respTag.Href = addSlash(respTag.Href)
		}
		name, ok := stripHrefPrefix(respTag.Href, prefix)
		if !ok {
			continue
		}
		// maybe a symlink.
		if props.ResourceType_.RedirectRef != nil {
			h := props.RefTarget_.Href
			if h == nil {
				continue
			}
			props.ResourceType = "redirectref"
			props.RefTarget = *h
		}
		props.Name = name
		ret = append(ret, props)
	}
	return
}

func (d *DavClient) PropFindWithRedirect(path string, depth int, props []string) (ret []*Props, err error) {
	ret, err = d.PropFind(path, depth, props)

	// did we get a redirect?
	if daverr, ok := err.(*DavError); ok {
		if daverr.Code / 100 != 3 || daverr.Location == "" {
			return
		}
		url, err2 := url.ParseRequestURI(daverr.Location)
		if err2 != nil {
			return
		}
		// if it's just a "this is a directory" redirect, retry.
		if url.Path == d.base + path + "/" {
			ret, err = d.PropFind(path + "/", depth, props)
		}
	}
	return
}

func (d *DavClient) Readdir(path string, detail bool) (ret []Dnode, err error) {

	if trace(T_WEBDAV) {
		tPrintf("Readdir(%s, %v", path, detail)
		defer func() {
			if err != nil {
				tPrintf("Readdir: %v", err)
				return
			}
			tPrintf("Readdir: returns %v", tJson(ret))
		}()
	}

	path = addSlash(path)
	props, err := d.PropFind(path, 1, nil)
	if err != nil {
		return
	}
	for _, p := range props {
		name := stripLastSlash(p.Name)
		if name == "" || name == "/" {
			name = "."
		}
		if strings.Index(name, "/") >= 0 {
			continue
		}
		if name == "._.DS_Store" || name == ".DS_Store" {
			continue
		}
		n := Dnode{
			Name: name,
			IsDir: p.ResourceType == "collection",
			IsLink: p.ResourceType == "redirectref",
			Target: p.RefTarget,
		}
		if detail {
			n.Mtime = parseTime(p.LastModified)
			n.Ctime = parseTime(p.CreationDate)
			if n.IsLink {
				n.Size = uint64(len(n.Target))
			} else {
				n.Size, _ = strconv.ParseUint(p.ContentLength, 10, 64)
			}
		}
		ret = append(ret, n)
	}
	return
}

func (d *DavClient) Stat(path string) (ret Dnode, err error) {

	if trace(T_WEBDAV) {
		tPrintf("Stat(%s)", path)
		defer func() {
			if err != nil {
				tPrintf("Stat: %v", err)
				return
			}
			tPrintf("Readdir: returns %v", tJson(ret))
		}()
	}

	props, err := d.PropFindWithRedirect(path, 0, nil)
	if err != nil {
		return
	}
	if len(props) != 1 {
		err = errors.New("500 PropFind error")
		return
	}
	p := props[0]
	size, _ := strconv.ParseUint(p.ContentLength, 10, 64)
	ret = Dnode{
		Name: stripLastSlash(p.Name),
		IsDir: p.ResourceType == "collection",
		Mtime: parseTime(p.LastModified),
		Ctime: parseTime(p.CreationDate),
		Size: size,
	} 
	return
}

func (d *DavClient) Get(path string) (data []byte, err error) {
	if trace(T_WEBDAV) {
		tPrintf("Get(%s)", path)
		defer func() {
			if err != nil {
				tPrintf("Get: %v", err)
				return
			}
			tPrintf("Get: returns %d bytes", len(data))
		}()
	}

	return d.GetRange(path, -1, -1)
}

func (d *DavClient) GetRange(path string, offset int64, length int) (data []byte, err error) {
	d.semAcquire()
	defer d.semRelease()

	if trace(T_WEBDAV) && length >= 0 {
		tPrintf("GetRange(%s, %d, %d)", path, offset, length)
		defer func() {
			if err != nil {
				tPrintf("GetRange: %v", err)
				return
			}
			tPrintf("GetRange: returns %d bytes", len(data))
		}()
	}
	req, err := d.buildRequest("GET", path)
	if err != nil {
		return
	}
	partial := false
	if (offset >= 0 && length >= 0 ) {
		partial = true
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset + int64(length) - 1))
	}
	resp, err := d.do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
		return
	}
	if partial && resp.StatusCode != 206 {
		err = davToErrno(&DavError{
			Message: "416 Range Not Satisfiable",
			Code: 416,
		})
		return
	}
	data, err = ioutil.ReadAll(resp.Body)
	if len(data) > length {
		data = data[:length]
	}
	return
}

func (d *DavClient) Mkcol(path string) (err error) {
	d.semAcquire()
	defer d.semRelease()

	if trace(T_WEBDAV) {
		tPrintf("Mkcol(%s)", path)
		defer func() {
			if err != nil {
				tPrintf("Mkcol: %v", err)
				return
			}
			tPrintf("Mkcol: OK")
		}()
	}
	req, err := d.buildRequest("MKCOL", path)
	if err != nil {
		return
	}
	resp, err := d.do(req)
	defer drainBody(resp)
	if err != nil {
		return
	}
	return
}

func (d *DavClient) Delete(path string) (err error) {
	d.semAcquire()
	defer d.semRelease()

	if trace(T_WEBDAV) {
		tPrintf("Delete(%s)", path)
		defer func() {
			if err != nil {
				tPrintf("Delete: %v", err)
				return
			}
			tPrintf("Delete: OK")
		}()
	}
	req, err := d.buildRequest("DELETE", path)
	if err != nil {
		return
	}
	resp, err := d.do(req)
	defer drainBody(resp)
	if err != nil {
		return
	}
	return
}

func (d *DavClient) Move(oldPath, newPath string) (err error) {
	d.semAcquire()
	defer d.semRelease()

	if trace(T_WEBDAV) {
		tPrintf("Move(%s, %s)", oldPath, newPath)
		defer func() {
			if err != nil {
				tPrintf("Move: %v", err)
				return
			}
			tPrintf("Move: OK")
		}()
	}
	req, err := d.buildRequest("MOVE", oldPath)
	if err != nil {
		return
	}
	if oldPath[len(oldPath)-1] == '/' {
		req.Header.Set("Overwrite", "F")
	} else {
		req.Header.Set("Overwrite", "T")
	}
	req.Header.Set("Destination", joinPath(d.Url, newPath))
	resp, err := d.do(req)
	defer drainBody(resp)
	if err != nil {
		return
	}
	if resp.StatusCode == 207 {
		// multipart response means there were errors.
		err = davToErrno(&DavError{
			Message: "500 unexpected error during MOVE",
			Code: 500,
		})
	}
	return
}

// https://blog.sphere.chronosempire.org.uk/2012/11/21/webdav-and-the-http-patch-nightmare
func (d *DavClient) apachePutRange(path string, data []byte, offset int64, create bool, excl bool) (created bool, err error) {
	if trace(T_WEBDAV) {
		tPrintf("apachePutRange(%s, %d, %d, %v, %v)", path, len(data), offset, create, excl)
		defer func() {
			if err != nil {
				tPrintf("apachePutRange: %v", err)
				return
			}
			tPrintf("apachePutRange: OK, created: %v", created)
		}()
	}
	req, err := d.buildRequest("PUT", path, data)

	end := offset + int64(len(data)) - 1
	if end < offset {
		end = offset
	}
	if create {
		if excl {
			req.Header.Set("If-None-Match", "*")
		}
	} else {
		req.Header.Set("If-Match", "*")
	}
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/*", offset, end))

	resp, err := d.do(req)
	defer drainBody(resp)
	if err != nil {
		return
	}
	created = resp.StatusCode == 201
	return
}

// http://sabre.io/dav/http-patch/
func (d *DavClient) sabrePutRange(path string, data []byte, offset int64, create bool, excl bool) (created bool, err error) {

	if trace(T_WEBDAV) {
		tPrintf("sabrePutRange(%s, %d, %d, %v, %v)", path, len(data), offset, create, excl)
		defer func() {
			if err != nil {
				tPrintf("sabrePutRange: %v", err)
				return
			}
			tPrintf("sabrePutRange: OK, created: %v", created)
		}()
	}

	req, err := d.buildRequest("PATCH", path, data)

	if create {
		if excl {
			req.Header.Set("If-None-Match", "*")
		}
	} else {
		req.Header.Set("If-Match", "*")
	}
	req.Header.Set("Content-Type", "application/x-sabredav-partialupdate")
	req.Header.Set("X-Update-Range", fmt.Sprintf("bytes=%d-", offset))

	resp, err := d.do(req)
	defer drainBody(resp)
	if err != nil {
		return
	}
	created = resp.StatusCode == 201
	return
}

func (d *DavClient) PutRange(path string, data []byte, offset int64, create bool, excl bool) (created bool, err error) {
	d.semAcquire()
	defer d.semRelease()
	if d.IsSabre {
		return d.sabrePutRange(path, data, offset, create, excl)
	}
	if d.IsApache {
		return d.apachePutRange(path, data, offset, create, excl)
	}
	err = davToErrno(&DavError{
		Message: "405 Method Not Allowed",
		Code: 405,
	})
	return
}

func (d *DavClient) CanPutRange() bool {
	return (d.IsSabre || d.IsApache) && !d.PutDisabled
}

func (d *DavClient) Put(path string, data []byte, create bool, excl bool) (created bool, err error) {
	d.semAcquire()
	defer d.semRelease()

	if !d.CanPutRange() {
		err = davToErrno(&DavError{
			Message: "405 Method Not Allowed",
			Code: 405,
		})
		return
	}

	req, err := d.buildRequest("PUT", path, data)
	if create {
		if excl {
			req.Header.Set("If-None-Match", "*")
		}
	} else {
		req.Header.Set("If-Match", "*")
	}
	resp, err := d.do(req)
	if err != nil {
		return
	}
	created = resp.StatusCode == 201
	defer resp.Body.Close()
	return
}

