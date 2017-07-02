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
	"strconv"
	"strings"
	"syscall"
	"time"
	"bazil.org/fuse"
)

type DavClient struct {
	Url		string
	Username	string
	Password	string
	Methods		map[string]bool
	DavSupport	map[string]bool
	IsSabre		bool
	IsApache	bool
	base		string
	cc		*http.Client
}

type DavError struct {
	Code		int
	Message		string
	Location	string
	Errnum		syscall.Errno
}

type Dnode struct {
	Name		string
	IsDir		bool
	Mtime		time.Time
	Ctime		time.Time
	Size		uint64
}

type Props struct {
	Name		string		`xml:"-"`
	ResourceType_	ResourceType	`xml:"resourcetype"`
	ResourceType	string		`xml:"-"`
	CreationDate	string		`xml:"creationdate"`
	LastModified	string		`xml:"getlastmodified"`
	Etag		string		`xml:"getetag"`
	ContentLength	string		`xml:"getcontentlength"`
}

type ResourceType struct {
	Collection	*struct{}	`xml:"collection"`
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

var mostProps = "<D:prop><D:resourcetype/><D:creationdate/><D:getlastmodified/><D:getetag/><D:getcontentlength/></D:prop>"

var davTimeFormat = "2006-01-02T15:04:05Z"

var davToErrnoMap = map[int]syscall.Errno{
	404:	syscall.ENOENT,
	405:	syscall.EPERM,
	408:	syscall.ETIMEDOUT,
	409:	syscall.ENOENT,
	416:	syscall.ERANGE,
	504:	syscall.ETIMEDOUT,
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
	for l > 1 {
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
	return s
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

func stripHrefPrefix(href string, prefix string) string {
	u, _ := url.ParseRequestURI(href)
	if u == nil {
		return ""
	}
	name := u.Path
	if strings.HasPrefix(name, prefix) {
		name = name[len(prefix):]
	}
	i := strings.Index(name, "/")
	if i >= 0 && i < len(name) - 1 {
		return ""
	}
	if name == "" {
		name = "./"
	}
	return name
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

func (d *DavError) Errno() fuse.Errno {
	return fuse.Errno(d.Errnum)
}

func (d *DavError) Error() string {
	return d.Message
}

func (d *DavClient) buildRequest(method string, path string, b ...interface{}) (req *http.Request, err error) {
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
	req, err = http.NewRequest(method, d.Url + path, body)
	if err != nil {
		return
	}
	if (blen >= 0) {
		req.Header.Set("Content-Length", fmt.Sprintf("%d", blen))
	}
	if d.Username != "" || d.Password != "" {
		req.SetBasicAuth(d.Username, d.Password)
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

		// Override some values from DefaultTransport.
		tr := *(http.DefaultTransport.(*http.Transport))
		tr.MaxIdleConnsPerHost = 4
		tr.DisableCompression = true

		d.cc = &http.Client{
			Timeout: 60 * time.Second,
			Transport: &tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	req, err := d.buildRequest("OPTIONS", "/")
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "*/*")
	resp, err := d.do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
		return
	}

	// Parse headers.
	d.Methods = mapLine(getHeader(resp.Header, "Allow"))
	d.DavSupport = mapLine(getHeader(resp.Header, "Dav"))
	d.IsApache = strings.Index(resp.Header.Get("Server"), "Apache") >= 0
	if d.DavSupport["sabredav-partialupdate"] {
		d.IsSabre = true
	}

	if !d.DavSupport["1"] {
		err = errors.New("not a webdav server")
	}

	return
}

func (d *DavClient) PropFind(path string, depth int, props []string) (ret map[string]*Props, err error) {

	a := append([]string{}, `<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D='DAV:'>`)
	if len(props) == 0 {
		a = append(a, mostProps)
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
	dp := "0"
	if depth > 0 {
		dp = "1"
	}
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Depth", dp)

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

	// fmt.Printf("PROPFIND: %+v\n", string(contents))

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

	ret = make(map[string]*Props)
	for _, respTag := range obj.Responses {
		if respTag.Propstat == nil || respTag.Propstat.Props == nil {
			err = errors.New("XML decode error")
			return
		}
		props := respTag.Propstat.Props
		props.Etag = stripQuotes(props.Etag)
		name := stripHrefPrefix(respTag.Href, prefix)
		if name == "" {
			continue
		}
		props.Name = name
		if props.ResourceType_.Collection != nil {
			props.ResourceType = "collection"
		}
		ret[name] = props
	}
	return
}

func (d *DavClient) PropFindWithRedirect(path string, depth int, props []string) (ret map[string]*Props, err error) {
	ret, err = d.PropFind(path, depth, props)

	// did we get a redirect?
	if daverr, ok := err.(*DavError); ok {
		if daverr.Code / 100 != 3 || daverr.Location == "" {
			return
		}
		fmt.Printf("PropFindWithRedirect: to %s\n", daverr.Location)
		url, err2 := url.ParseRequestURI(daverr.Location)
		if err2 != nil {
			fmt.Printf("Bad location\n")
			return
		}
		// if it's just a "this is a directory" redirect, retry.
		fmt.Printf("Compare %s and %s\n", url.Path, d.base + path + "/")
		if url.Path == d.base + path + "/" {
			fmt.Printf("Retry %s\n", path + "/")
			ret, err = d.PropFind(path + "/", depth, props)
		}
	}
	return
}

func (d *DavClient) Readdir(path string, detail bool) (ret []Dnode, err error) {
	path = addSlash(path)
	fmt.Printf("Readdir %s\n", path)
	props, err := d.PropFind(path, 1, nil)
	if err != nil {
		return
	}
	for name, p := range props {
		dir := strings.HasSuffix(name, "/")
		if dir {
			name = name[:len(name)-1]
		}
		if name == "._.DS_Store" || name == ".DS_Store" {
			continue
		}
		n := Dnode{
			Name: name,
			IsDir: p.ResourceType == "collection",
		}
		if detail {
			n.Mtime = parseTime(p.LastModified)
			n.Ctime = parseTime(p.CreationDate)
			n.Size, _ = strconv.ParseUint(p.ContentLength, 10, 64)
		}
		ret = append(ret, n)
	}
	return
}

func (d *DavClient) Stat(path string) (ret Dnode, err error) {
	fmt.Printf("Stat %s\n", path)
	props, err := d.PropFindWithRedirect(path, 0, nil)
	if err != nil {
		return
	}
	if len(props) != 1 {
		err = errors.New("500 PropFind error")
		return
	}
	for _, p := range props {
		size, _ := strconv.ParseUint(p.ContentLength, 10, 64)
		ret = Dnode{
			Name: p.Name,
			IsDir: p.ResourceType == "collection",
			Mtime: parseTime(p.LastModified),
			Ctime: parseTime(p.CreationDate),
			Size: size,
		} 
		return
	}
	return
}

func (d *DavClient) Get(path string) (data []byte, err error) {
	return d.GetRange(path, -1, -1)
}

func (d *DavClient) GetRange(path string, offset int64, length int) (data []byte, err error) {
	fmt.Printf("READ %s %d %d\n", path, offset, length)
	req, err := d.buildRequest("GET", path)
	if err != nil {
		return
	}
	partial := false
	if (offset >= 0 && length >= 0 ) {
		partial = true
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset + int64(length) - 1))
	}
	fmt.Printf("req header: %+v\n", req.Header)
	resp, err := d.do(req)
	if err != nil {
		fmt.Printf("READ ERROR %s\n", err)
		return
	}
	fmt.Printf("resp header: %+v\n", resp.Header)
	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
		fmt.Printf("READ ERROR %s\n", err)
		return
	}
	if partial && resp.StatusCode != 206 {
		err = davToErrno(&DavError{
			Message: "416 Range Not Satisfiable",
			Code: 416,
		})
		return
	}
	fmt.Printf("resp header: %+v\n", resp.Header)
	defer resp.Body.Close()
	data, err = ioutil.ReadAll(resp.Body)
	fmt.Printf("READ OK %d bytes\n", len(data))
	if len(data) > length {
		data = data[:length]
	}
	return
}

func (d *DavClient) Mkcol(path string) (err error) {
	req, err := d.buildRequest("MKCOL", path)
	if err != nil {
		return
	}
	resp, err := d.do(req)
	fmt.Printf("MKCOL reply %+v\n", resp.Header)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	return
}

func (d *DavClient) Delete(path string) (err error) {
	req, err := d.buildRequest("DELETE", path)
	if err != nil {
		return
	}
	resp, err := d.do(req)
	fmt.Printf("DELETE reply %+v\n", resp.Header)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	return
}

func (d *DavClient) Move(oldPath, newPath string) (err error) {
	fmt.Printf("Move %s -> %s\n", oldPath, newPath)
	req, err := d.buildRequest("MOVE", oldPath)
	if err != nil {
		return
	}
	req.Header.Set("Destination", joinPath(d.Url, newPath))
	resp, err := d.do(req)
	fmt.Printf("MOVE reply %s %+v\n", resp.Status, resp.Header)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	return
}

// https://blog.sphere.chronosempire.org.uk/2012/11/21/webdav-and-the-http-patch-nightmare
func (d *DavClient) apachePutRange(path string, data []byte, offset int64) (created bool, err error) {
	fmt.Printf("ApachePutRange %d %d @ %s\n", offset, len(data), path)
	req, err := d.buildRequest("PUT", path, data)

	end := offset + int64(len(data)) - 1
	if end < 0 {
		end = 0
	}
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/*", offset, end))
	fmt.Printf("PUT req %+v\n", req.Header)

	resp, err := d.do(req)
	fmt.Printf("PUT reply %s %+v\n", resp.Status, resp.Header)
	if err != nil {
		return
	}
	created = resp.StatusCode == 201
	defer resp.Body.Close()
	return
}

// http://sabre.io/dav/http-patch/
func (d *DavClient) sabrePutRange(path string, data []byte, offset int64) (created bool, err error) {
	fmt.Printf("sabrePutRange %d %d @ %s\n", offset, len(data), path)

	req, err := d.buildRequest("PATCH", path, data)
	end := offset + int64(len(data)) - 1

	req.Header.Set("Content-Type", "application/x-sabredav-partialupdate")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
	req.Header.Set("X-Update-Range", fmt.Sprintf("bytes=%d-%d", offset, end))

	resp, err := d.do(req)
	fmt.Printf("PUT reply %s %+v\n", resp.Status, resp.Header)
	if err != nil {
		return
	}
	created = resp.StatusCode == 201
	defer resp.Body.Close()
	return
}

func (d *DavClient) PutRange(path string, data []byte, offset int64) (created bool, err error) {
	if d.IsSabre {
		return d.sabrePutRange(path, data, offset)
	}
	if d.IsApache {
		return d.apachePutRange(path, data, offset)
	}
	err = davToErrno(&DavError{
		Message: "405 Method Not Allowed",
		Code: 405,
	})
	return
}

func (d *DavClient) Put(path string, data []byte) (created bool, err error) {
	fmt.Printf("Put %d @ %s\n", len(data), path)
	req, err := d.buildRequest("PUT", path, data)
	resp, err := d.do(req)
	if err != nil {
		return
	}
	created = resp.StatusCode == 201
	defer resp.Body.Close()
	return
}

