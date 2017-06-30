package main;

import (
//	"fmt"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type DavClient struct {
	Url		string
	Username	string
	Password	string
	base		string
	cc		*http.Client
}

type Dnode struct {
	Name		string
	IsDir		bool
	Mtime		int64
	Ctime		int64
	Size		uint64
}

type Props struct {
	Name		string		`xml:"-"`
	ResourceType	string		`xml:"-"`
	CreationDate	string		`xml:"creationdate"`
	LastModified	string		`xml:"getlastmodified"`
	Etag		string		`xml:"getetag"`
	ContentLength	string		`xml:"getcontentlength"`
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

func statusIsValid(resp *http.Response) bool {
	return resp.StatusCode / 100 == 2
}

func stripQuotes(s string) string {
	l := len(s)
	if l > 1 && s[0] == '"' && s [l-1] == '"' {
		return s[1:l-1]
	}
	return s
}

func parseTime (s string) int64 {
	var t time.Time
	var err error
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		t, err = time.Parse(davTimeFormat, s)
	} else {
		t, err = http.ParseTime(s)
	}
	if err != nil {
		return 0
	}
	return t.Unix()
}

func (d *DavClient) buildRequest(method string, path string, b ...interface{}) (req *http.Request, err error) {
	var body io.Reader
	if len(b) > 0 {
		switch v := b[0].(type) {
		case string:
			body = strings.NewReader(v)
		case []byte:
			body = bytes.NewReader(v)
		default:
			body = v.(io.Reader)
		}
	}
	if method == "OPTIONS" && path == "*" {
		req, err = http.NewRequest(method, d.Url + "/", body)
		if req != nil {
			req.URL.Path = "*"
		}
	} else {
		req, err = http.NewRequest(method, d.Url + path, body)
	}
	if err != nil {
		return
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

func (d *DavClient) do(req *http.Request) (*http.Response, error) {
	return d.cc.Do(req)
}

func (d *DavClient) Mount() (err error) {
	if d.cc == nil {
		d.cc = &http.Client{}
		if !strings.HasSuffix(d.Url, "/") {
			d.Url += "/"
		}
		var u *url.URL
		u, err = url.ParseRequestURI(d.Url)
		if err != nil {
			return
		}
		d.base = u.Path
		if strings.HasSuffix(d.Url, "/") {
			d.Url = d.Url[:len(d.Url)-1]
		}
	}
	resp, err := d.request("OPTIONS", "*")
	if err != nil {
		return
	}
	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
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

	// fmt.Printf("XXX c is %+v\n", string(contents))

	obj := MultiStatus{}
	err = xml.Unmarshal(contents, &obj)
	if err != nil {
		return
	}
	if obj.Responses == nil || len(obj.Responses) == 0 {
		err = errors.New("XML decode error")
		return
	}

	ret = make(map[string]*Props)
	u := &url.URL{}
	for _, respTag := range obj.Responses {
		if respTag.Propstat == nil || respTag.Propstat.Props == nil {
			err = errors.New("XML decode error")
			return
		}
		props := respTag.Propstat.Props
		props.Etag = stripQuotes(props.Etag)

		var u2 *url.URL
		u2, err = u.Parse(respTag.Href)
		if u2 == nil {
			continue
		}
		name := u2.Path
		if len(d.base) > 0 && strings.HasPrefix(name, d.base) {
			name = name[len(d.base):]
		}
		i := strings.Index(name, "/")
		if i >= 0 && i < len(name) - 1 {
			continue
		}
		if name == "" {
			name = "./"
		}
		props.Name = name
		if strings.HasSuffix(name, "/") {
			props.ResourceType = "collection"
		}
		ret[name] = props
	}
	return
}

func (d *DavClient) Readdir(path string, detail bool) (ret []Dnode, err error) {
	props, err := d.PropFind(path, 1, nil)
	if err != nil {
		return
	}
	for name, p := range props {
		dir := strings.HasSuffix(name, "/")
		if dir {
			name = name[:len(name)-1]
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
	props, err := d.PropFind(path, 0, nil)
	if err != nil {
		return
	}
	var p *Props
	var ok bool
	if p, ok = props[""]; !ok {
		err = errors.New("404 Not Found")
		return
	}
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

