package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/kar98k/internal/config"
	"github.com/kar98k/internal/tui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	validateNoReach bool
	validateJSON    bool
	validateTimeout time.Duration
)

var validateCmd = &cobra.Command{
	Use:   "validate <config-file>",
	Short: "Validate a config file (structural + semantic + reachability)",
	Long: `Run a layered validation pass over a kar98k config file:

  structural   — YAML parses cleanly into the Config schema
  semantic     — values make sense (max_tps >= base_tps, sane lambda, etc.)
  reachability — every HTTP target responds (skip with --no-reach)

Each issue carries a severity (error / warning / info) and an
optional suggestion. Exit code is non-zero when any error is reported.

Examples:
  kar validate configs/kar98k.yaml
  kar validate configs/kar98k.yaml --no-reach
  kar validate configs/kar98k.yaml --json`,
	Args: cobra.ExactArgs(1),
	RunE: runValidate,
}

func init() {
	validateCmd.Flags().BoolVar(&validateNoReach, "no-reach", false,
		"skip reachability checks (don't make any HTTP requests)")
	validateCmd.Flags().BoolVar(&validateJSON, "json", false,
		"emit machine-readable JSON instead of the human-friendly punch list")
	validateCmd.Flags().DurationVar(&validateTimeout, "reach-timeout", 5*time.Second,
		"per-target HTTP timeout for reachability checks")
	rootCmd.AddCommand(validateCmd)
}

func runValidate(cmd *cobra.Command, args []string) error {
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	cfg := config.DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		// Structural failure short-circuits — there's nothing to
		// semantically validate against.
		return reportStructural(path, err)
	}

	issues := config.ValidateConfig(cfg)
	if !validateNoReach {
		ctx := context.Background()
		issues = append(issues, config.CheckReachability(ctx, cfg, validateTimeout)...)
	}

	if validateJSON {
		out := struct {
			File   string          `json:"file"`
			Issues []config.Issue  `json:"issues"`
			OK     bool            `json:"ok"`
		}{
			File:   path,
			Issues: issues,
			OK:     !config.HasErrors(issues),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
		if !out.OK {
			os.Exit(1)
		}
		return nil
	}

	renderIssues(path, issues)
	if config.HasErrors(issues) {
		os.Exit(1)
	}
	return nil
}

func reportStructural(path string, err error) error {
	if validateJSON {
		out := struct {
			File   string          `json:"file"`
			Issues []config.Issue  `json:"issues"`
			OK     bool            `json:"ok"`
		}{
			File: path,
			Issues: []config.Issue{{
				Path:     "(yaml)",
				Severity: config.SeverityError,
				Message:  err.Error(),
			}},
			OK: false,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else {
		fmt.Println()
		fmt.Println(tui.ErrorStyle.Render("  ✗ structural: " + err.Error()))
		fmt.Println()
	}
	os.Exit(1)
	return nil // unreachable
}

func renderIssues(path string, issues []config.Issue) {
	fmt.Println()
	fmt.Println(tui.SubtitleStyle.Render("  Validating " + path))
	fmt.Println()

	if len(issues) == 0 {
		fmt.Println(tui.SuccessStyle.Render("  ✓ no issues found"))
		fmt.Println()
		return
	}

	for _, iss := range issues {
		var icon string
		var styled func(...string) string
		switch iss.Severity {
		case config.SeverityError:
			icon = "✗"
			styled = tui.ErrorStyle.Render
		case config.SeverityWarning:
			icon = "!"
			styled = tui.WarningStyle.Render
		default:
			icon = "✓"
			styled = tui.DimStyle.Render
		}
		fmt.Printf("  %s %s — %s\n",
			styled(icon),
			tui.LabelStyle.Render(iss.Path),
			iss.Message)
		if iss.Suggestion != "" {
			fmt.Println(tui.DimStyle.Render("      ↳ " + iss.Suggestion))
		}
	}
	fmt.Println()

	var errs, warns int
	for _, iss := range issues {
		switch iss.Severity {
		case config.SeverityError:
			errs++
		case config.SeverityWarning:
			warns++
		}
	}
	switch {
	case errs > 0:
		fmt.Println(tui.ErrorStyle.Render(fmt.Sprintf("  ✗ %d error(s), %d warning(s)", errs, warns)))
	case warns > 0:
		fmt.Println(tui.WarningStyle.Render(fmt.Sprintf("  ! %d warning(s)", warns)))
	default:
		fmt.Println(tui.SuccessStyle.Render("  ✓ no errors"))
	}
	fmt.Println()
}
