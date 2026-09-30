package cli

import (
	"errors"
	"io"
	"sort"
	"strings"

	postscale "github.com/postscale/postscale-go"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

func (a *app) authCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage local credentials and profiles"}
	var stdin bool
	login := &cobra.Command{Use: "login", Short: "Save an API key in the OS keychain (from environment or --key-stdin)", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.login(stdin) }}
	login.Flags().BoolVar(&stdin, "key-stdin", false, "Read the API key from stdin instead of POSTSCALE_API_KEY")
	cmd.AddCommand(login)
	cmd.AddCommand(&cobra.Command{Use: "status", Short: "Show local credential selection without contacting the API", Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cred, err := a.resolveCredentials()
			if err != nil {
				return err
			}
			a.credentials = cred
			return a.print(map[string]any{"configured": true, "verified": false})
		}})
	cmd.AddCommand(&cobra.Command{Use: "profiles", Short: "List configured profiles without reading secrets", Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := a.readConfig()
			if err != nil {
				return err
			}
			type item struct {
				Name    string `json:"name"`
				BaseURL string `json:"base_url"`
				Default bool   `json:"default"`
			}
			items := make([]item, 0, len(cfg.Profiles))
			for name, p := range cfg.Profiles {
				items = append(items, item{name, p.BaseURL, name == cfg.DefaultProfile})
			}
			sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
			return a.print(items)
		}})
	cmd.AddCommand(&cobra.Command{Use: "use NAME", Short: "Choose the default keychain profile", Args: exactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := a.readConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return localError("profile does not exist")
			}
			cfg.DefaultProfile = args[0]
			if err := a.writeConfig(cfg); err != nil {
				return err
			}
			return a.print(map[string]string{"default_profile": args[0]})
		}})
	cmd.AddCommand(&cobra.Command{Use: "logout", Short: "Remove a local profile and its keychain credential; does not revoke the API key", Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error { return a.logout() }})
	return cmd
}

func (a *app) login(stdin bool) error {
	name := a.selectedProfile()
	if name == "" {
		name = "default"
	}
	if err := validProfile(name); err != nil {
		return err
	}
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	baseURL := a.baseURL
	if baseURL == "" {
		baseURL = strings.TrimSpace(a.getenv("POSTSCALE_BASE_URL"))
	}
	if baseURL == "" {
		if p, ok := cfg.Profiles[name]; ok {
			baseURL = p.BaseURL
		} else {
			baseURL = postscale.DefaultBaseURL
		}
	}
	baseURL, err = normalizeBaseURL(baseURL)
	if err != nil {
		return err
	}
	if p, ok := cfg.Profiles[name]; ok && p.BaseURL != baseURL {
		return localError("profile already uses a different endpoint; choose another profile name")
	}
	key := strings.TrimSpace(a.getenv("POSTSCALE_API_KEY"))
	if stdin {
		data, err := io.ReadAll(io.LimitReader(a.in, 4098))
		if err != nil {
			return localError("cannot read API key from stdin")
		}
		if len(data) > 4097 {
			return localError("API key input is too large")
		}
		key = strings.TrimSpace(string(data))
	}
	if err := validateKey(key); err != nil {
		return err
	}
	id := credentialID(name, baseURL)
	oldKey, oldErr := a.keys.Get(keyringService, id)
	if oldErr != nil && !errors.Is(oldErr, keyring.ErrNotFound) {
		return localError("OS keychain unavailable; unlock it or use POSTSCALE_API_KEY directly")
	}
	if err := a.keys.Set(keyringService, id, key); err != nil {
		return localError("cannot save API key in the OS keychain; use POSTSCALE_API_KEY directly")
	}
	cfg.Profiles[name] = profile{BaseURL: baseURL}
	if cfg.DefaultProfile == "" {
		cfg.DefaultProfile = name
	}
	if err := a.writeConfig(cfg); err != nil {
		var rollback error
		if oldErr == nil {
			rollback = a.keys.Set(keyringService, id, oldKey)
		} else {
			rollback = a.keys.Delete(keyringService, id)
		}
		if rollback != nil {
			return localError("configuration save and keychain rollback failed; inspect the io.postscale.cli keychain entry")
		}
		return err
	}
	a.credentials = &credentials{executionContext{name, baseURL, "keychain", keyEnvironment(key)}, key}
	return a.print(map[string]any{"saved": true, "verified": false})
}

func (a *app) logout() error {
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	name := a.selectedProfile()
	if name == "" {
		name = cfg.DefaultProfile
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return localError("profile does not exist; select it with --profile")
	}
	id := credentialID(name, p.BaseURL)
	oldKey, keyErr := a.keys.Get(keyringService, id)
	if keyErr != nil && !errors.Is(keyErr, keyring.ErrNotFound) {
		return localError("cannot read OS keychain credential for logout")
	}
	if err := a.keys.Delete(keyringService, id); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return localError("cannot remove OS keychain credential")
	}
	delete(cfg.Profiles, name)
	if cfg.DefaultProfile == name {
		cfg.DefaultProfile = ""
	}
	if err := a.writeConfig(cfg); err != nil {
		if keyErr == nil {
			if rollback := a.keys.Set(keyringService, id, oldKey); rollback != nil {
				return localError("configuration save and keychain rollback failed; log in again to restore the profile")
			}
		}
		return err
	}
	return a.print(map[string]string{"removed_profile": name})
}
