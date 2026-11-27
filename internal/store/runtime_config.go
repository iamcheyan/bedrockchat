package store

// applyRuntimePreferences overlays only stored choices. TOML remains the
// canonical fallback and is never rewritten. Explicit options are applied by
// the final parseConfig call after this overlay.
func applyRuntimePreferences(raw map[string]any, p RuntimePreferences) {
	set := func(section, key string, value any) {
		table, _ := raw[section].(map[string]any)
		if table == nil {
			table = make(map[string]any)
			raw[section] = table
		}
		table[key] = value
	}
	if p.Provider != "" {
		set("chat", "provider", p.Provider)
	}
	if p.Thinking != "" {
		set("chat", "thinking", p.Thinking)
	}
	if p.Endpoint != "" {
		set("openai", "endpoint", p.Endpoint)
	}
	if p.Region != "" {
		set("aws", "region", p.Region)
	}
	if p.Profile != "" {
		set("aws", "profile", p.Profile)
	}
	if p.ResponseTimeout != "" {
		set("openai", "response_timeout", p.ResponseTimeout)
	}
	if p.MaxInput != 0 {
		set("chat", "max_input_tokens", int64(p.MaxInput))
	}
	if p.MaxOutput != 0 {
		set("chat", "max_output_tokens", int64(p.MaxOutput))
	}
	if p.MaxEdge != 0 {
		set("images", "max_edge", int64(p.MaxEdge))
	}
	if p.MaxBytes != 0 {
		set("images", "max_bytes", int64(p.MaxBytes))
	}
	if p.Stream != nil {
		set("openai", "stream", *p.Stream)
	}
	if p.SaveImages != nil {
		set("history", "save_images", *p.SaveImages)
	}
}
