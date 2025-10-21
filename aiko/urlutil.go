package aiko

import (
	"net/url"
	"strings"
)

func EndpointFromURL(raw string) string {
	if raw == "" {
		return ""
	}

	path := raw

	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		if u, err := url.Parse(raw); err == nil {
			path = preferredPath(u)
		}
	} else if strings.HasPrefix(raw, "/") {
		path = raw
	} else if u, err := url.Parse(raw); err == nil {
		candidate := preferredPath(u)
		if candidate != "" {
			path = candidate
		}
	}

	path = trimQuery(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func preferredPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.EscapedPath() != "" {
		return u.EscapedPath()
	}
	if u.Path != "" {
		return u.Path
	}
	return ""
}

func trimQuery(raw string) string {
	if idx := strings.IndexByte(raw, '?'); idx >= 0 {
		return raw[:idx]
	}
	return raw
}
