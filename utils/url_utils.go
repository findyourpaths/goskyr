package utils

import (
	"strings"

	"github.com/gosimple/slug"
)

func TrimURLScheme(u string) string {
	u = strings.TrimPrefix(u, "file://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "www.")
	u = strings.ToLower(u)
	return u
}

func MakeURLStringSlug(u string) string {
	return slug.Make(TrimURLScheme(u))
}
