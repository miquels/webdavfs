
package main

import (
	"errors"
	"strconv"
	"strings"
)

type MountOptions struct {
	AllowRoot		bool
	AllowOther		bool
	DefaultPermissions	bool
	NoDefaultPermissions	bool
	ReadOnly		bool
	ReadWrite		bool
	Uid			uint32
	Gid			uint32
	Mode			uint32
	Cookie			string
	Password		string
	Username		string
	AsyncRead		bool
	NonEmpty		bool
	MaxConns		uint32
	MaxIdleConns		uint32
}

func parseUInt32(v string, base int, name string, loc *uint32) (err error) {
	n, err := strconv.ParseUint(v , base, 32)
	if err == nil {
		*loc = uint32(n)
	}
	return
}

func parseMountOptions(n string, sloppy bool) (mo MountOptions, err error) {
	if n == "" {
		return
	}

	for _, kv := range strings.Split(n, ",") {
		a := strings.SplitN(kv, "=", 2)
		v := ""
		if len(a) > 1 {
			v = a[1]
		}
		switch a[0] {
		case "allow_root":
			mo.AllowRoot = true
		case "allow_other":
			mo.AllowOther = true
		case "default_permissions":
			mo.DefaultPermissions = true
		case "no_default_permissions":
			mo.NoDefaultPermissions = true
		case "ro":
			mo.ReadOnly = true
		case "rw":
			mo.ReadWrite = true
		case "uid":
			err = parseUInt32(v, 10, "uid", &mo.Uid)
		case "gid":
			err = parseUInt32(v, 10, "gid", &mo.Gid)
		case "mode":
			err = parseUInt32(v, 8, "mode", &mo.Mode)
		case "cookie":
			mo.Cookie = v
		case "password":
			mo.Password = v
		case "username":
			mo.Username = v
		case "async_read":
			mo.AsyncRead = true
		case "nonempty":
			mo.NonEmpty = true
		case "maxconns":
			err = parseUInt32(v, 10, "maxconns", &mo.MaxConns)
		case "maxidleconns":
			err = parseUInt32(v, 10, "maxidleconns", &mo.MaxIdleConns)
		default:
			if !sloppy {
				err = errors.New(a[0] + ": unknown option")
			}
		}
		if err != nil {
			return
		}
	}
	return
}
