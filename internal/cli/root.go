package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	postscale "github.com/postscale/postscale-go"
	"github.com/spf13/cobra"
)

type app struct {
	in            io.Reader
	out, errOut   io.Writer
	getenv        func(string) string
	userConfigDir func() (string, error)
	keys          keyStore
	version       string
	profile       string
	baseURL       string
	compact       bool
	timeout       time.Duration
	retries       int
	credentials   *credentials
	started       bool
}

// Run executes one CLI invocation. API commands write JSON to stdout and errors
// to stderr; no command prompts or reads local application .env files.
func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer, version string) int {
	a := &app{in: in, out: out, errOut: errOut, getenv: os.Getenv,
		userConfigDir: os.UserConfigDir, keys: systemKeyStore{}, version: version}
	return a.execute(ctx, args)
}

func (a *app) execute(ctx context.Context, args []string) int {
	a.credentials, a.started = nil, false
	cmd := a.command()
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if !a.started {
		err = usageError(err.Error())
	}
	return a.writeError(err)
}

func (a *app) command() *cobra.Command {
	root := &cobra.Command{
		Use: "postscale", Short: "Send, inspect, and troubleshoot Postscale email",
		Long:    "Postscale CLI: domain setup, sending, and delivery diagnostics.\nResults are JSON. Use --json for compact output and --help on any command.",
		Version: a.version, SilenceUsage: true, SilenceErrors: true,
		PersistentPreRun: func(_ *cobra.Command, _ []string) { a.started = true },
		Args:             noArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetIn(a.in)
	root.SetOut(a.out)
	root.SetErr(a.errOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err.Error()) })
	f := root.PersistentFlags()
	f.StringVar(&a.profile, "profile", "", "Named keychain profile (overrides ambient API key/base URL)")
	f.StringVar(&a.baseURL, "base-url", "", "API origin without /v1 (HTTPS, or loopback HTTP)")
	f.BoolVar(&a.compact, "json", false, "Emit compact JSON instead of indented JSON")
	f.DurationVar(&a.timeout, "timeout", 30*time.Second, "Total API command timeout, including pagination and retries")
	f.IntVar(&a.retries, "retries", 2, "Maximum retries for reads; mutations are never retried")
	root.AddCommand(a.authCommand())
	root.AddCommand(a.domainsCommand(), a.emailsCommand(), a.inboundCommand(), a.webhooksCommand())
	return root
}

func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return usageError(err.Error())
	}
	return nil
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return usageError(err.Error())
		}
		return nil
	}
}

func (a *app) withClient(cmd *cobra.Command, fn func(context.Context, *postscale.Client) error) error {
	if a.timeout <= 0 || a.retries < 0 || a.retries > 10 {
		return usageError("timeout must be positive and retries must be between 0 and 10")
	}
	cred, err := a.resolveCredentials()
	if err != nil {
		return err
	}
	a.credentials = cred
	transport := &userAgentTransport{base: http.DefaultTransport, version: a.version}
	httpClient := &http.Client{
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	client, err := postscale.NewClient(cred.key, postscale.WithBaseURL(cred.BaseURL),
		postscale.WithTimeout(a.timeout), postscale.WithRetries(a.retries), postscale.WithHTTPClient(httpClient))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), a.timeout)
	defer cancel()
	return fn(ctx, client)
}

type userAgentTransport struct {
	base    http.RoundTripper
	version string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header.Set("User-Agent", "postscale-cli/"+t.version+" "+req.Header.Get("User-Agent"))
	return t.base.RoundTrip(copy)
}

type commandError struct {
	code    string
	message string
	exit    int
}

func (e *commandError) Error() string        { return e.message }
func usageError(message string) error        { return &commandError{"usage_error", message, 2} }
func localError(message string) error        { return &commandError{"configuration_error", message, 2} }
func resultError(code, message string) error { return &commandError{code, message, 1} }

func isCanceled(err error) bool { return errors.Is(err, context.Canceled) }
