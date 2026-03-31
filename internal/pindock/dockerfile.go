package pindock

import "strings"

// logicalLine is a Dockerfile instruction with continuations joined and byte offsets into the original.
type logicalLine struct {
	text  string
	start int // byte offset of first source line
	end   int // byte offset past last char of last source line
}

// ParseDockerfile extracts image references from Dockerfile content.
func ParseDockerfile(content string) []ImageRef {
	logical := joinLogicalLines(content)
	stageNames := collectStageNames(logical)

	var refs []ImageRef
	for _, ll := range logical {
		lineRefs := parseInstruction(ll.text, stageNames)

		// Resolve byte offsets by finding each ref in its source region.
		region := content[ll.start:ll.end]
		searchFrom := 0
		for i := range lineRefs {
			idx := strings.Index(region[searchFrom:], lineRefs[i].Original)
			if idx >= 0 {
				lineRefs[i].Start = ll.start + searchFrom + idx
				searchFrom += idx + len(lineRefs[i].Original)
			}
		}

		refs = append(refs, lineRefs...)
	}
	return refs
}

// joinLogicalLines merges backslash-continued lines, tracking byte ranges in the original.
func joinLogicalLines(content string) []logicalLine {
	var result []logicalLine
	var buf strings.Builder

	offset := 0
	groupStart := 0
	inGroup := false

	for line := range strings.SplitSeq(content, "\n") {
		if !inGroup {
			groupStart = offset
			inGroup = true
		}
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasSuffix(trimmed, `\`) {
			buf.WriteString(trimmed[:len(trimmed)-1])
			buf.WriteByte(' ')
		} else {
			buf.WriteString(trimmed)
			result = append(result, logicalLine{
				text:  buf.String(),
				start: groupStart,
				end:   offset + len(line),
			})
			buf.Reset()
			inGroup = false
		}
		offset += len(line) + 1
	}
	if buf.Len() > 0 {
		result = append(result, logicalLine{
			text:  buf.String(),
			start: groupStart,
			end:   offset - 1,
		})
	}
	return result
}

// collectStageNames gathers FROM ... AS names so --from can distinguish stages from images.
func collectStageNames(lines []logicalLine) map[string]bool {
	names := make(map[string]bool)
	for _, ll := range lines {
		fields := strings.Fields(ll.text)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		for i := 1; i < len(fields)-1; i++ {
			if strings.EqualFold(fields[i], "AS") {
				names[fields[i+1]] = true
				break
			}
		}
	}
	return names
}

func parseInstruction(line string, stageNames map[string]bool) []ImageRef {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil
	}
	switch strings.ToUpper(fields[0]) {
	case "FROM":
		return parseFromArgs(fields[1:], stageNames)
	case "COPY":
		return parseCopyFrom(fields[1:], stageNames)
	case "RUN":
		return parseRunMountFrom(fields[1:], stageNames)
	default:
		return nil
	}
}

// parseFromArgs extracts the image from FROM [--platform=...] <image> [AS <name>].
func parseFromArgs(args []string, stageNames map[string]bool) []ImageRef {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			continue
		}
		if strings.EqualFold(arg, "AS") {
			break
		}
		if isStageRef(arg, stageNames) {
			return nil
		}
		return []ImageRef{ParseImageRef(arg)}
	}
	return nil
}

// parseCopyFrom extracts the image from COPY --from=<image>.
func parseCopyFrom(args []string, stageNames map[string]bool) []ImageRef {
	for _, arg := range args {
		if ref, ok := strings.CutPrefix(arg, "--from="); ok {
			if isStageRef(ref, stageNames) {
				return nil
			}
			return []ImageRef{ParseImageRef(ref)}
		}
		if !strings.HasPrefix(arg, "--") {
			break
		}
	}
	return nil
}

// parseRunMountFrom extracts images from RUN --mount=from=<image>.
func parseRunMountFrom(args []string, stageNames map[string]bool) []ImageRef {
	var refs []ImageRef
	for _, arg := range args {
		mount, ok := strings.CutPrefix(arg, "--mount=")
		if !ok {
			if !strings.HasPrefix(arg, "--") {
				break
			}
			continue
		}
		for kv := range strings.SplitSeq(mount, ",") {
			if ref, ok := strings.CutPrefix(kv, "from="); ok {
				if isStageRef(ref, stageNames) {
					continue
				}
				refs = append(refs, ParseImageRef(ref))
			}
		}
	}
	return refs
}

// isStageRef reports whether ref is a build stage name or numeric index.
func isStageRef(ref string, stageNames map[string]bool) bool {
	if stageNames[ref] {
		return true
	}
	for _, c := range ref {
		if c < '0' || c > '9' {
			return false
		}
	}
	return ref != ""
}
