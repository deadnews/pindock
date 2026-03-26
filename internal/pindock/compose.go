package pindock

import (
	"regexp"
	"strings"
)

var composeImageRe = regexp.MustCompile(`^\s*image:\s*["']?([^\s"'#]+)`)

// ParseCompose extracts image references from compose YAML content.
func ParseCompose(content string) []ImageRef {
	var refs []ImageRef
	for line := range strings.SplitSeq(content, "\n") {
		m := composeImageRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		refs = append(refs, ParseImageRef(m[1]))
	}
	return refs
}
