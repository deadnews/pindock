package pindock

import (
	"regexp"
	"strings"
)

var composeImageRe = regexp.MustCompile(`^\s*image:\s*["']?([^\s"'#]+)`)

// ParseCompose extracts image references from compose YAML content.
func ParseCompose(content string) []ImageRef {
	var refs []ImageRef
	offset := 0
	for line := range strings.SplitSeq(content, "\n") {
		if loc := composeImageRe.FindStringSubmatchIndex(line); loc != nil {
			ref := ParseImageRef(line[loc[2]:loc[3]])
			ref.Start = offset + loc[2]
			refs = append(refs, ref)
		}
		offset += len(line) + 1
	}
	return refs
}
