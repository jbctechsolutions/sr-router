package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jbctechsolutions/sr-router/config"
	mcpserver "github.com/jbctechsolutions/sr-router/mcp"
	"github.com/jbctechsolutions/sr-router/proxy"
	"github.com/jbctechsolutions/sr-router/router"
	"github.com/jbctechsolutions/sr-router/telemetry"
)

func main() {
	var configDir string

	rootCmd := &cobra.Command{
		Use:   "sr-router",
		Short: "Intelligent LLM request router",
		Long:  "Routes LLM requests to the cheapest model that meets quality requirements.",
	}

	// --config is persistent so all subcommands inherit it.
	rootCmd.PersistentFlags().StringVar(&configDir, "config", "", "Config directory (default: ./config, then ~/.config/sr-router/config)")

	// resolveConfig returns configDir if set, otherwise searches well-known paths.
	resolveConfig := func() string {
		if configDir != "" {
			return configDir
		}
		if _, err := os.Stat("config"); err == nil {
			return "config"
		}
		home, err := os.UserHomeDir()
		if err == nil {
			candidate := filepath.Join(home, ".config", "sr-router", "config")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		return "config" // fall through to default; Load will surface a useful error
	}

	// -------------------------------------------------------------------------
	// route — classify + route, print decision
	// -------------------------------------------------------------------------
	routeCmd := &cobra.Command{
		Use:   "route [prompt]",
		Short: "Route a prompt to the best model",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")

			cfg, err := config.Load(resolveConfig())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			classifier := router.NewClassifier(cfg)
			rtr := router.NewRouter(cfg)

			headers := make(map[string]string)
			if bg, _ := cmd.Flags().GetBool("background"); bg {
				headers["x-request-type"] = "background"
			}
			if interactive, _ := cmd.Flags().GetBool("interactive"); interactive {
				headers["x-request-type"] = "chat"
			}

			classification := classifier.Classify(prompt, headers)
			decision := rtr.Route(classification)

			fmt.Printf("Route Class:  %s\n", classification.RouteClass)
			fmt.Printf("Task Type:    %s\n", classification.TaskType)
			fmt.Printf("Tier:         %s\n", decision.Tier)
			fmt.Printf("Model:        %s\n", decision.Model)
			fmt.Printf("Score:        %.2f\n", decision.Score)
			fmt.Printf("Est. Cost:    $%.4f/1k tokens\n", decision.EstCost)
			fmt.Printf("Reasoning:    %s\n", decision.Reasoning)
			if len(decision.Alternatives) > 0 {
				fmt.Printf("Alternatives: ")
				for i, alt := range decision.Alternatives {
					if i > 0 {
						fmt.Print(", ")
					}
					fmt.Printf("%s (%.2f)", alt.Model, alt.Score)
				}
				fmt.Println()
			}
			return nil
		},
	}
	routeCmd.Flags().Bool("background", false, "Force background route class")
	routeCmd.Flags().Bool("interactive", false, "Force interactive route class")

	// -------------------------------------------------------------------------
	// classify — classify only, no routing
	// -------------------------------------------------------------------------
	classifyCmd := &cobra.Command{
		Use:   "classify [prompt]",
		Short: "Classify a prompt without routing",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")

			cfg, err := config.Load(resolveConfig())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			classifier := router.NewClassifier(cfg)
			classification := classifier.Classify(prompt, nil)

			fmt.Printf("Route Class:       %s\n", classification.RouteClass)
			fmt.Printf("Task Type:         %s\n", classification.TaskType)
			fmt.Printf("Tier:              %s\n", classification.Tier)
			fmt.Printf("Min Quality:       %.2f\n", classification.MinQuality)
			fmt.Printf("Latency Budget:    %dms\n", classification.LatencyBudgetMs)
			fmt.Printf("Confidence:        %.2f\n", classification.Confidence)
			if len(classification.RequiredStrengths) > 0 {
				fmt.Printf("Required Strengths: %s\n", strings.Join(classification.RequiredStrengths, ", "))
			}
			return nil
		},
	}

	// -------------------------------------------------------------------------
	// models — list configured models
	// -------------------------------------------------------------------------
	modelsCmd := &cobra.Command{
		Use:   "models",
		Short: "List configured models",
		RunE: func(cmd *cobra.Command, args []string) error {
			tierFilter, _ := cmd.Flags().GetString("tier")

			cfg, err := config.Load(resolveConfig())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Determine the set of model names to display.
			var names []string
			if tierFilter != "" {
				names = cfg.GetTierModels(tierFilter)
				if len(names) == 0 {
					return fmt.Errorf("unknown tier: %q", tierFilter)
				}
			} else {
				for name := range cfg.Models {
					names = append(names, name)
				}
				sort.Strings(names)
			}

			fmt.Printf("%-30s %-14s %-10s %-8s %s\n", "NAME", "PROVIDER", "COST/1K", "QUALITY", "STRENGTHS")
			fmt.Println(strings.Repeat("-", 90))
			for _, name := range names {
				m, ok := cfg.Models[name]
				if !ok {
					continue
				}
				fmt.Printf("%-30s %-14s $%-9.4f %-8.2f %s\n",
					name,
					m.Provider,
					m.CostPer1kTok,
					m.QualityCeiling,
					strings.Join(m.Strengths, ", "),
				)
			}
			return nil
		},
	}
	modelsCmd.Flags().String("tier", "", "Filter by tier name (e.g. premium, budget, speed)")

	// -------------------------------------------------------------------------
	// proxy — start transparent HTTP proxy
	// -------------------------------------------------------------------------
	proxyCmd := &cobra.Command{
		Use:   "proxy",
		Short: "Start transparent HTTP proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			port, _ := cmd.Flags().GetString("port")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			cfg, err := config.Load(resolveConfig())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			srv, err := proxy.NewProxyServer(cfg, port, dryRun)
			if err != nil {
				return fmt.Errorf("creating proxy server: %w", err)
			}
			return srv.Start()
		},
	}
	proxyCmd.Flags().String("port", "8889", "Port to listen on")
	proxyCmd.Flags().Bool("dry-run", false, "Return mock responses with routing decisions instead of calling providers")
	proxyCmd.Flags().Bool("dashboard", false, "Open dashboard in browser on startup")

	// -------------------------------------------------------------------------
	// mcp — start MCP server (stdio transport)
	// -------------------------------------------------------------------------
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP server (stdio transport)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(resolveConfig())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			classifier := router.NewClassifier(cfg)
			rtr := router.NewRouter(cfg)

			// Telemetry is optional; if it fails the MCP server continues without it.
			tel, _ := telemetry.NewCollector(filepath.Join(os.TempDir(), "sr-router-telemetry.db"))

			srv := mcpserver.NewMCPServer(cfg, classifier, rtr, tel)
			return srv.Start()
		},
	}

	// -------------------------------------------------------------------------
	// stats — show routing statistics
	// -------------------------------------------------------------------------
	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show routing statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			modelFilter, _ := cmd.Flags().GetString("model")

			dbPath := filepath.Join(os.TempDir(), "sr-router-telemetry.db")
			col, err := telemetry.NewCollector(dbPath)
			if err != nil {
				return fmt.Errorf("opening telemetry database: %w", err)
			}
			defer col.Close()

			stats, err := col.GetStats(modelFilter)
			if err != nil {
				return fmt.Errorf("retrieving stats: %w", err)
			}

			fmt.Printf("Total Requests: %d\n", stats.TotalRequests)
			fmt.Printf("Total Cost:     $%.6f\n", stats.TotalCost)
			fmt.Printf("Failovers:      %d\n", stats.FailoverCount)

			if len(stats.ByModel) > 0 {
				fmt.Println("\nBy Model:")
				modelNames := make([]string, 0, len(stats.ByModel))
				for name := range stats.ByModel {
					modelNames = append(modelNames, name)
				}
				sort.Strings(modelNames)
				for _, name := range modelNames {
					fmt.Printf("  %-30s %d\n", name, stats.ByModel[name])
				}
			}

			if len(stats.ByTier) > 0 {
				fmt.Println("\nBy Tier:")
				tierNames := make([]string, 0, len(stats.ByTier))
				for name := range stats.ByTier {
					tierNames = append(tierNames, name)
				}
				sort.Strings(tierNames)
				for _, name := range tierNames {
					fmt.Printf("  %-20s %d\n", name, stats.ByTier[name])
				}
			}
			return nil
		},
	}
	statsCmd.Flags().String("model", "", "Filter stats by model name")

	// -------------------------------------------------------------------------
	// feedback — record user feedback for a routing event
	// -------------------------------------------------------------------------
	feedbackCmd := &cobra.Command{
		Use:   "feedback <event_id>",
		Short: "Record feedback for a routing event",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eventID := args[0]
			rating, _ := cmd.Flags().GetInt("rating")
			override, _ := cmd.Flags().GetString("override")

			if rating < 1 || rating > 5 {
				return fmt.Errorf("--rating must be between 1 and 5")
			}

			dbPath := filepath.Join(os.TempDir(), "sr-router-telemetry.db")
			col, err := telemetry.NewCollector(dbPath)
			if err != nil {
				return fmt.Errorf("opening telemetry database: %w", err)
			}
			defer col.Close()

			if err := col.RecordFeedback(eventID, rating, override); err != nil {
				return fmt.Errorf("recording feedback: %w", err)
			}

			fmt.Printf("Feedback recorded for event %s (rating: %d", eventID, rating)
			if override != "" {
				fmt.Printf(", override: %s", override)
			}
			fmt.Println(")")
			return nil
		},
	}
	feedbackCmd.Flags().Int("rating", 0, "Rating from 1 (poor) to 5 (excellent)")
	feedbackCmd.Flags().String("override", "", "Model the user would have preferred")
	_ = feedbackCmd.MarkFlagRequired("rating")

	// -------------------------------------------------------------------------
	// config — configuration management subcommand group
	// -------------------------------------------------------------------------
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management",
	}

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate YAML configs",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := config.Load(resolveConfig())
			if err != nil {
				return fmt.Errorf("config validation failed: %w", err)
			}
			fmt.Println("Config is valid!")
			return nil
		},
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Show the config directory being used",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := resolveConfig()
			abs, err := filepath.Abs(dir)
			if err != nil {
				abs = dir
			}
			fmt.Printf("Config directory: %s\n", abs)
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				fmt.Println("Warning: directory does not exist.")
			} else {
				fmt.Println("Directory exists.")
			}
			return nil
		},
	}

	configCmd.AddCommand(validateCmd, initCmd)

	// -------------------------------------------------------------------------
	// Wire all top-level subcommands into root.
	// -------------------------------------------------------------------------
	rootCmd.AddCommand(
		routeCmd,
		classifyCmd,
		modelsCmd,
		proxyCmd,
		mcpCmd,
		statsCmd,
		feedbackCmd,
		configCmd,
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
