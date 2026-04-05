package script

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Runner is the common interface for all language backends.
// Each language runtime implements this to participate in kar98k's script engine.
type Runner interface {
	// Load parses a script file and prepares for execution.
	Load(path string) error

	// Setup runs the setup() function once before VUs start.
	// Returns shared data accessible in each iteration.
	Setup() (interface{}, error)

	// Iterate runs one VU iteration (the default() function).
	// Called repeatedly by the VU scheduler.
	Iterate(vuID int, data interface{}) error

	// Teardown runs cleanup after all VUs finish.
	Teardown(data interface{}) error

	// Scenario returns the parsed scenario configuration.
	Scenario() *ScenarioConfig

	// Metrics returns collected metrics.
	Metrics() *Metrics

	// Close releases any resources held by the runner.
	Close() error
}

// Language represents a supported scripting language.
type Language string

const (
	LangStarlark Language = "starlark"
	LangJS       Language = "javascript"
	LangExternal Language = "external"
)

// DetectLanguage determines the language from a file extension.
func DetectLanguage(path string) Language {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".star":
		return LangStarlark
	case ".js":
		return LangJS
	default:
		return LangExternal
	}
}

// NewRunner creates a Runner for the given script file.
func NewRunner(path string) (Runner, error) {
	lang := DetectLanguage(path)

	switch lang {
	case LangStarlark:
		return NewStarlarkRunner(), nil
	case LangJS:
		return NewJSRunner(), nil
	case LangExternal:
		return NewExternalRunner(path)
	default:
		return nil, fmt.Errorf("unsupported script language for %q", path)
	}
}

// ExternalInterpreter maps file extensions to interpreters.
var ExternalInterpreter = map[string]string{
	".py":  "python3",
	".rb":  "ruby",
	".lua": "lua",
	".sh":  "bash",
	".ts":  "npx ts-node",
}
