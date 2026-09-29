package pindock

import "strings"

// token is a whitespace-separated word of a Dockerfile instruction.
type token struct {
	text  string
	start int // byte offset in file content
}

// ParseDockerfile extracts image references from Dockerfile content.
func ParseDockerfile(content string) []ImageRef {
	instructions := splitInstructions(content)
	stageNames := collectStageNames(instructions)

	refs := make([]ImageRef, 0, len(instructions))
	for _, tokens := range instructions {
		refs = append(refs, parseInstruction(tokens, stageNames)...)
	}
	return refs
}

// splitInstructions tokenizes content per instruction, joining continued lines.
func splitInstructions(content string) [][]token {
	var result [][]token
	var tokens []token
	offset := 0

	for line := range strings.SplitSeq(content, "\n") {
		lineStart := offset
		offset += len(line) + 1
		// Per Dockerfile spec, comments are removed before continuation handling.
		if isCommentLine(line) {
			continue
		}
		body, continued := strings.CutSuffix(strings.TrimRight(line, " \t\r"), `\`)
		pos := 0
		for field := range strings.FieldsSeq(body) {
			i := pos + strings.Index(body[pos:], field)
			tokens = append(tokens, token{text: field, start: lineStart + i})
			pos = i + len(field)
		}
		if !continued && len(tokens) > 0 {
			result = append(result, tokens)
			tokens = nil
		}
	}
	if len(tokens) > 0 {
		result = append(result, tokens)
	}
	return result
}

func isCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "#")
}

// collectStageNames gathers FROM ... AS names so --from can distinguish stages from images.
func collectStageNames(instructions [][]token) map[string]bool {
	names := make(map[string]bool)
	for _, tokens := range instructions {
		if !strings.EqualFold(tokens[0].text, "FROM") {
			continue
		}
		for i := 1; i < len(tokens)-1; i++ {
			if strings.EqualFold(tokens[i].text, "AS") {
				names[strings.ToLower(tokens[i+1].text)] = true
				break
			}
		}
	}
	return names
}

func parseInstruction(tokens []token, stageNames map[string]bool) []ImageRef {
	switch strings.ToUpper(tokens[0].text) {
	case "FROM":
		return parseFromArgs(tokens[1:], stageNames)
	case "COPY":
		return parseCopyFrom(tokens[1:], stageNames)
	case "RUN":
		return parseRunMountFrom(tokens[1:], stageNames)
	default:
		return nil
	}
}

// parseFromArgs extracts the image from FROM [--platform=...] <image> [AS <name>].
func parseFromArgs(args []token, stageNames map[string]bool) []ImageRef {
	for _, arg := range args {
		if strings.HasPrefix(arg.text, "--") {
			continue
		}
		if strings.EqualFold(arg.text, "AS") || isStageRef(arg.text, stageNames) {
			return nil
		}
		return []ImageRef{imageRefAt(arg.text, arg.start)}
	}
	return nil
}

// parseCopyFrom extracts the image from COPY --from=<image>.
func parseCopyFrom(args []token, stageNames map[string]bool) []ImageRef {
	const prefix = "--from="
	for _, arg := range args {
		if ref, ok := strings.CutPrefix(arg.text, prefix); ok {
			if isStageRef(ref, stageNames) {
				return nil
			}
			return []ImageRef{imageRefAt(ref, arg.start+len(prefix))}
		}
		if !strings.HasPrefix(arg.text, "--") {
			break
		}
	}
	return nil
}

// parseRunMountFrom extracts images from RUN --mount=from=<image>.
func parseRunMountFrom(args []token, stageNames map[string]bool) []ImageRef {
	const prefix = "--mount="
	var refs []ImageRef
	for _, arg := range args {
		mount, ok := strings.CutPrefix(arg.text, prefix)
		if !ok {
			if !strings.HasPrefix(arg.text, "--") {
				break
			}
			continue
		}
		start := arg.start + len(prefix)
		for kv := range strings.SplitSeq(mount, ",") {
			if ref, ok := strings.CutPrefix(kv, "from="); ok && !isStageRef(ref, stageNames) {
				refs = append(refs, imageRefAt(ref, start+len("from=")))
			}
			start += len(kv) + 1
		}
	}
	return refs
}

func imageRefAt(s string, start int) ImageRef {
	ref := ParseImageRef(s)
	ref.Start = start
	return ref
}

// isStageRef reports whether ref is a build stage name or numeric index.
func isStageRef(ref string, stageNames map[string]bool) bool {
	if stageNames[strings.ToLower(ref)] {
		return true
	}
	for _, c := range ref {
		if c < '0' || c > '9' {
			return false
		}
	}
	return ref != ""
}
