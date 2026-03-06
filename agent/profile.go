package agent

import (
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/llm"
)

func builtInToolsForConfig(cfg SessionConfig, env exec.ExecutionEnvironment) []tools.Tool {
	switch resolvedProvider(cfg) {
	case "openai":
		return []tools.Tool{
			tools.NewReadTool(env),
			tools.NewWriteTool(env),
			tools.NewApplyPatchTool(env),
			tools.NewGlobTool(env),
			tools.NewGrepSearchTool(env),
			tools.NewBashTool(env, cfg.CommandTimeout, cfg.MaxCommandTimeout),
		}
	default:
		return []tools.Tool{
			tools.NewReadTool(env),
			tools.NewWriteTool(env),
			tools.NewEditTool(env),
			tools.NewGlobTool(env),
			tools.NewGrepSearchTool(env),
			tools.NewBashTool(env, cfg.CommandTimeout, cfg.MaxCommandTimeout),
		}
	}
}

func resolvedProvider(cfg SessionConfig) string {
	if cfg.Provider != "" {
		return cfg.Provider
	}
	if cfg.Model == "" {
		return ""
	}
	info := llm.GetModelInfo(cfg.Model)
	if info == nil {
		return ""
	}
	return info.Provider
}
