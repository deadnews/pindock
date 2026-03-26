package pindock

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// FileType distinguishes Dockerfile from compose file.
type FileType int

// Supported file types.
const (
	FileTypeDockerfile FileType = iota
	FileTypeCompose
)

// Patterns aligned with Renovate file-matching conventions.
var (
	dockerfileRe = regexp.MustCompile(`(?i)^(?:(.+)\.)?(dockerfile|containerfile)(?:\.([^.]+))?$`)
	composeRe    = regexp.MustCompile(`(?i)^(?:docker-)?compose.*\.ya?ml$`)

	// Reject source extensions: "dockerfile.py" vs "dockerfile.dev".
	sourceExts = map[string]bool{
		"go": true, "py": true, "js": true, "ts": true, "rs": true,
		"rb": true, "java": true, "sh": true, "c": true, "cpp": true,
		"cs": true, "php": true, "md": true, "txt": true,
	}
)

// ClassifyFile returns the file type if the basename matches a known pattern.
func ClassifyFile(path string) (FileType, bool) {
	base := filepath.Base(path)
	if m := dockerfileRe.FindStringSubmatch(base); m != nil {
		if ext := strings.ToLower(m[3]); ext != "" && sourceExts[ext] {
			return 0, false
		}
		return FileTypeDockerfile, true
	}
	if composeRe.MatchString(base) {
		return FileTypeCompose, true
	}
	return 0, false
}

// DiscoverFiles walks root recursively, skipping dot-directories, and returns matching files.
func DiscoverFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := ClassifyFile(path); ok {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}
	return files, nil
}
