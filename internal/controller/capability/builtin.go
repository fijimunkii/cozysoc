package capability

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed manifests/*.json
var builtinFS embed.FS

func Builtins() (*Registry, error) {
	entries, err := fs.ReadDir(builtinFS, "manifests")
	if err != nil {
		return nil, fmt.Errorf("read builtin capability manifests: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		file, err := builtinFS.Open("manifests/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("open builtin capability %s: %w", entry.Name(), err)
		}
		manifest, decodeErr := Decode(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("builtin capability %s: %w", entry.Name(), decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close builtin capability %s: %w", entry.Name(), closeErr)
		}
		manifests = append(manifests, manifest)
	}
	return NewRegistry(manifests...)
}
