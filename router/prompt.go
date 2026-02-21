package router

import "strings"

// InjectSuffix appends the model-specific prompt suffix to systemPrompt,
// separated by a blank line. If the model has no suffix configured, or the
// suffix is blank after trimming, systemPrompt is returned unchanged.
func (r *Router) InjectSuffix(modelName string, systemPrompt string) string {
	m, ok := r.cfg.Models[modelName]
	if !ok || m.PromptSuffix == nil {
		return systemPrompt
	}

	suffix := strings.TrimSpace(*m.PromptSuffix)
	if suffix == "" {
		return systemPrompt
	}

	if systemPrompt == "" {
		return suffix
	}

	return systemPrompt + "\n\n" + suffix
}
