package builder

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SourceType represents the type of source code.
type SourceType string

const (
	SourceTypeRust   SourceType = "rust"   // .rs files
	SourceTypeGo     SourceType = "go"     // .go files
	SourceTypeC      SourceType = "c"      // .c files
	SourceTypeUnknown SourceType = "unknown"
)

// BuildResult contains the result of a WASM build.
type BuildResult struct {
	Success  bool
	Error    error
	WASMFile string
}

// Builder automatically compiles source code to WebAssembly.
type Builder struct {
	cacheDir string
}

// NewBuilder creates a new Builder instance.
func NewBuilder(cacheDir ...string) *Builder {
	var dir string
	if len(cacheDir) > 0 && cacheDir[0] != "" {
		dir = cacheDir[0]
	} else {
		dir = filepath.Join(".", ".sonic", "wasm-cache")
	}
	_ = os.MkdirAll(dir, 0755)
	return &Builder{cacheDir: dir}
}

// DetectSourceType detects the type of source code from a file extension.
func DetectSourceType(filename string) SourceType {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".rs":
		return SourceTypeRust
	case ".go":
		return SourceTypeGo
	case ".c":
		return SourceTypeC
	default:
		return SourceTypeUnknown
	}
}

// Build compiles a source file to WASM automatically.
func (b *Builder) Build(sourceFile string) (*BuildResult, error) {
	sourceType := DetectSourceType(sourceFile)
	
	switch sourceType {
	case SourceTypeRust:
		return b.buildRust(sourceFile)
	case SourceTypeGo:
		return b.buildGo(sourceFile)
	case SourceTypeC:
		return b.buildC(sourceFile)
	default:
		return nil, fmt.Errorf("unknown source type: %s", sourceFile)
	}
}

// buildRust compiles Rust code to WASM (requires cargo and wasm-pack).
func (b *Builder) buildRust(sourceFile string) (*BuildResult, error) {
	// Check if cargo is available
	if _, err := exec.LookPath("cargo"); err != nil {
		return &BuildResult{
			Success: false,
			Error:   errors.New("cargo not found - install Rust from https://rustup.rs/"),
		}, nil
	}

	// For now, return placeholder (full implementation coming soon)
	return &BuildResult{
		Success: false,
		Error:   errors.New("Rust WASM compilation is not fully implemented yet"),
	}, nil
}

// buildGo compiles Go code to WASM.
func (b *Builder) buildGo(sourceFile string) (*BuildResult, error) {
	return &BuildResult{
		Success: false,
		Error:   errors.New("Go WASM build coming soon"),
	}, nil
}

// buildC compiles C code to WASM.
func (b *Builder) buildC(sourceFile string) (*BuildResult, error) {
	return &BuildResult{
		Success: false,
		Error:   errors.New("C WASM build coming soon"),
	}, nil
}
