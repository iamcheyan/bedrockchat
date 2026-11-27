package main

import (
	"github.com/iamcheyan/bedrockchat/internal/store"
)

// rememberStartup saves only explicit, validated choices, not config defaults
// or transient actions (--help, --current-model, --check-clipboard, update).
func (a *app) rememberStartup(opt store.Options) {
	if opt.Provider != "" || opt.Endpoint != "" || opt.Region != "" || opt.Profile != "" ||
		opt.Thinking != "" || opt.ResponseTimeout != "" || opt.MaxInput != 0 || opt.MaxOutput != 0 ||
		opt.NoSaveImages || opt.SaveImages != nil || opt.Stream != nil {
		a.problem(a.prefs.UpdateRuntimePreferences(func(p *store.RuntimePreferences) error {
			if opt.Provider != "" {
				p.Provider = a.cfg.Provider
			}
			if opt.Endpoint != "" {
				p.Endpoint = a.cfg.Endpoint
			}
			if opt.Region != "" {
				p.Region = a.cfg.Region
			}
			if opt.Profile != "" {
				p.Profile = a.cfg.Profile
			}
			if opt.Thinking != "" {
				p.Thinking = string(a.cfg.Thinking)
			}
			if opt.ResponseTimeout != "" {
				p.ResponseTimeout = a.cfg.ResponseTimeout.String()
			}
			if opt.MaxInput != 0 {
				p.MaxInput = a.cfg.MaxInput
			}
			if opt.MaxOutput != 0 {
				p.MaxOutput = a.cfg.MaxOutput
			}
			if opt.NoSaveImages || opt.SaveImages != nil {
				value := a.cfg.SaveImages
				p.SaveImages = &value
			}
			if opt.Stream != nil {
				value := a.cfg.Stream
				p.Stream = &value
			}
			return nil
		}))
	}
	if opt.Language != "" {
		a.problem(a.prefs.SaveLanguage(a.lang))
	}
	if opt.Model != "" {
		a.problem(a.history.SaveModel(a.session.Model))
	}
}
