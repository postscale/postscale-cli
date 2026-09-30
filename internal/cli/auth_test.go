package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

type memoryKeys struct {
	values map[string]string
	err    error
}

func (k *memoryKeys) Get(service, user string) (string, error) {
	if k.err != nil {
		return "", k.err
	}
	if v, ok := k.values[service+user]; ok {
		return v, nil
	}
	return "", keyring.ErrNotFound
}
func (k *memoryKeys) Set(service, user, value string) error {
	if k.err != nil {
		return k.err
	}
	k.values[service+user] = value
	return nil
}
func (k *memoryKeys) Delete(service, user string) error {
	if k.err != nil {
		return k.err
	}
	delete(k.values, service+user)
	return nil
}

type harness struct {
	app            *app
	env            map[string]string
	keys           *memoryKeys
	stdout, stderr bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{env: map[string]string{"POSTSCALE_CONFIG_DIR": t.TempDir()}, keys: &memoryKeys{values: map[string]string{}}}
	h.app = &app{out: &h.stdout, errOut: &h.stderr, getenv: func(name string) string { return h.env[name] }, keys: h.keys, version: "test"}
	return h
}

func (h *harness) run(input string, args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	h.app.in = strings.NewReader(input)
	return h.app.execute(context.Background(), args)
}

func TestProfileLifecycleAndIsolation(t *testing.T) {
	h := newHarness(t)
	key := "ps_test_saved-secret"
	if code := h.run(key+"\n", "auth", "login", "--profile", "test", "--key-stdin"); code != 0 {
		t.Fatalf("login: %d %s", code, &h.stderr)
	}
	path, _ := h.app.configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), key) || strings.Contains(h.stdout.String(), key) {
		t.Fatal("credential exposed")
	}
	info, _ := os.Stat(path)
	// Windows exposes read/write attributes rather than Unix permission bits.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v", info.Mode())
	}
	h.env["POSTSCALE_API_KEY"] = "ps_live_ambient-secret"
	h.env["POSTSCALE_BASE_URL"] = "https://ambient.invalid"
	if code := h.run("", "auth", "status", "--profile", "test", "--json"); code != 0 {
		t.Fatalf("status: %d %s", code, &h.stderr)
	}
	if !strings.Contains(h.stdout.String(), `"credential_environment":"test"`) || strings.Contains(h.stdout.String(), "ambient") {
		t.Fatalf("profile not isolated: %s", &h.stdout)
	}
	if code := h.run("", "auth", "status", "--profile", "test", "--base-url", "https://other.invalid"); code != 2 {
		t.Fatalf("endpoint override: %d", code)
	}
	if code := h.run("", "auth", "status", "--json"); code != 0 || !strings.Contains(h.stdout.String(), "ambient.invalid") {
		t.Fatalf("environment auth: %d %s", code, &h.stdout)
	}
	if code := h.run("", "auth", "profiles"); code != 0 || strings.Contains(h.stdout.String(), key) {
		t.Fatalf("profiles: %d %s", code, &h.stdout)
	}
	if code := h.run("", "auth", "logout", "--profile", "test"); code != 0 {
		t.Fatalf("logout: %d %s", code, &h.stderr)
	}
	if len(h.keys.values) != 0 {
		t.Fatal("credential retained")
	}
	if code := h.run("", "auth", "status", "--profile", "test"); code != 2 {
		t.Fatalf("missing profile must not fall back to ambient key: %d", code)
	}
}

func TestKeychainFailureDoesNotSavePlaintext(t *testing.T) {
	h := newHarness(t)
	h.keys.err = errors.New("backend unavailable")
	if code := h.run("ps_test_secret", "auth", "login", "--key-stdin"); code != 2 {
		t.Fatalf("exit: %d", code)
	}
	path, _ := h.app.configPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected config: %v", err)
	}
	if strings.Contains(h.stderr.String(), "ps_test_secret") {
		t.Fatal("credential leaked")
	}
	h.env["POSTSCALE_API_KEY"] = "ps_test_ci"
	if code := h.run("", "auth", "status"); code != 0 {
		t.Fatalf("CI requires keychain: %s", &h.stderr)
	}
}

func TestAuthInputAndEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"http://remote.invalid", "https://user:secret@api.postscale.io", "https://api.postscale.io/v1", "https://api.postscale.io?token=secret", "file:///tmp/socket"} {
		t.Run(endpoint, func(t *testing.T) {
			h := newHarness(t)
			h.env["POSTSCALE_API_KEY"] = "ps_test_ci"
			if code := h.run("", "auth", "status", "--base-url", endpoint); code != 2 {
				t.Fatalf("exit %d", code)
			}
			if strings.Contains(h.stderr.String(), "secret") {
				t.Fatal("endpoint secret exposed")
			}
		})
	}
	for _, key := range []string{"", "one\ntwo", strings.Repeat("x", 4098)} {
		h := newHarness(t)
		if code := h.run(key, "auth", "login", "--key-stdin"); code != 2 {
			t.Fatalf("invalid key accepted: %d", code)
		}
	}
}

func TestHelpAndErrorsWorkWithoutCredentials(t *testing.T) {
	h := newHarness(t)
	if code := h.run("", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "Postscale CLI") {
		t.Fatalf("help: %d %s", code, &h.stdout)
	}
	if code := h.run("", "--version"); code != 0 || !strings.Contains(h.stdout.String(), "test") {
		t.Fatalf("version: %d %s", code, &h.stdout)
	}
	for _, args := range [][]string{{"bogus"}, {"auth", "status", "--unknown"}, {"auth", "status", "extra"}, {"auth", "use"}} {
		if code := h.run("", args...); code != 2 {
			t.Fatalf("%v exit %d: %s", args, code, &h.stderr)
		}
		var envelope struct {
			Error errorDetail `json:"error"`
		}
		if err := json.Unmarshal(h.stderr.Bytes(), &envelope); err != nil || envelope.Error.Code != "usage_error" {
			t.Fatalf("invalid error: %s (%v)", &h.stderr, err)
		}
		if h.stdout.Len() != 0 {
			t.Fatal("error polluted stdout")
		}
	}
}

func TestCorruptConfigurationAndFailedReplacement(t *testing.T) {
	h := newHarness(t)
	path, _ := h.app.configPath()
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := h.run("", "auth", "profiles"); code != 2 {
		t.Fatalf("invalid config exit %d", code)
	}
	// A directory at config.json must not be overwritten by a configuration save.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := &configuration{Version: 1, Profiles: map[string]profile{}}
	if err := h.app.writeConfig(cfg); err == nil {
		t.Fatal("expected write failure")
	}
}
