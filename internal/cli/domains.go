package cli

import (
	"context"
	"regexp"
	"strings"

	postscale "github.com/postscale/postscale-go"
	"github.com/spf13/cobra"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validID(id string) error {
	if !uuidPattern.MatchString(id) {
		return usageError("expected a resource UUID (use the id from the corresponding list command)")
	}
	return nil
}

func domainPage(ctx context.Context, client *postscale.Client, offset, limit int) (page[postscale.Domain], error) {
	p, err := client.Domains.List(ctx, &postscale.PaginationParams{Limit: &limit, Offset: &offset})
	if err != nil {
		return page[postscale.Domain]{}, err
	}
	// ListDomains returns limit/offset but no total, despite the SDK model.
	return page[postscale.Domain]{items: p.Domains, requestID: p.ResponseMetadata.RequestID}, nil
}

func resolveDomain(ctx context.Context, client *postscale.Client, name string) (string, error) {
	if uuidPattern.MatchString(name) {
		return name, nil
	}
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" || strings.ContainsAny(name, "/\\ \t\r\n") {
		return "", usageError("expected a domain name or resource UUID")
	}
	value, err := collectPages(ctx, client, 0, 100, true, domainPage)
	if err != nil {
		return "", err
	}
	match := ""
	for _, domain := range value.Data.([]postscale.Domain) {
		if strings.EqualFold(strings.TrimSuffix(domain.Domain, "."), name) {
			if match != "" {
				return "", resultError("ambiguous_domain", "multiple domains match; select a domain UUID")
			}
			if validID(domain.ID) != nil {
				return "", resultError("invalid_response", "API returned an invalid domain UUID")
			}
			match = domain.ID
		}
	}
	if match == "" {
		return "", resultError("domain_not_found", "domain was not found in the selected account")
	}
	return match, nil
}

func (a *app) domainsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "domains", Short: "Configure domains and inspect DNS authentication"}
	cmd.AddCommand(listCommand(a, "list", "List domains", domainPage))
	for _, operation := range []string{"get", "dns", "verify"} {
		op := operation
		short := map[string]string{"get": "Inspect a domain", "dns": "Show required and recommended DNS records", "verify": "Check domain DNS; exit nonzero if not verified"}[op]
		cmd.AddCommand(&cobra.Command{Use: op + " DOMAIN_OR_ID", Short: short, Args: exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.withClient(cmd, func(ctx context.Context, client *postscale.Client) error {
					id, err := resolveDomain(ctx, client, args[0])
					if err != nil {
						return err
					}
					switch op {
					case "get":
						v, err := client.Domains.Get(ctx, id)
						if err != nil {
							return err
						}
						return a.respond(v, v.ResponseMetadata)
					case "dns":
						v, err := client.Domains.GetDNSRecords(ctx, id)
						if err != nil {
							return err
						}
						return a.respond(v, v.ResponseMetadata)
					default:
						v, err := client.Domains.Verify(ctx, id)
						if err != nil {
							return err
						}
						if err := a.respond(v, v.ResponseMetadata); err != nil {
							return err
						}
						if !v.Verified {
							return resultError("domain_not_verified", "domain is not verified; inspect DNS checks in stdout")
						}
						return nil
					}
				})
			}})
	}
	var kind string
	var acknowledgeMX bool
	create := &cobra.Command{Use: "create DOMAIN", Short: "Add a domain (defaults to outbound sending)", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch kind {
			case "outbound", "inbound", "both", "alias":
			default:
				return usageError("type must be outbound, inbound, both or alias")
			}
			return a.withClient(cmd, func(ctx context.Context, client *postscale.Client) error {
				params := &postscale.CreateDomainParams{Domain: strings.TrimSuffix(strings.ToLower(strings.TrimSpace(args[0])), "."), Type: kind}
				if params.Domain == "" {
					return usageError("domain is required")
				}
				if acknowledgeMX {
					params.InboundMXAcknowledged = &acknowledgeMX
				}
				v, err := client.Domains.Create(ctx, params)
				if err != nil {
					return err
				}
				return a.respond(v, v.ResponseMetadata)
			})
		}}
	create.Flags().StringVar(&kind, "type", "outbound", "Domain purpose: outbound, inbound, both or alias")
	create.Flags().BoolVar(&acknowledgeMX, "acknowledge-inbound-mx", false, "Acknowledge the inbound MX requirements")
	cmd.AddCommand(create)
	return cmd
}
