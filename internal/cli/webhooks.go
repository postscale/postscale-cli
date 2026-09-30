package cli

import (
	"context"

	postscale "github.com/postscale/postscale-go"
	"github.com/spf13/cobra"
)

func (a *app) webhooksCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "webhooks", Short: "Inspect webhook endpoints and delivery history"}
	// The server returns all endpoints and ignores pagination parameters. A
	// paginated wrapper would truncate or repeat this unpaginated collection.
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List all webhook endpoints (unpaginated API)", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withClient(cmd, func(ctx context.Context, client *postscale.Client) error {
				p, err := client.Webhooks.List(ctx, nil)
				if err != nil {
					return err
				}
				if p.Webhooks == nil {
					p.Webhooks = []postscale.Webhook{}
				}
				return a.respond(p.Webhooks, p.ResponseMetadata)
			})
		}})
	var status, endpoint string
	var days int
	deliveries := &cobra.Command{Use: "deliveries", Short: "Inspect webhook delivery attempts"}
	list := listCommand(a, "list", "List delivery attempts; partial history exits nonzero", func(ctx context.Context, client *postscale.Client, offset, limit int) (page[postscale.WebhookDelivery], error) {
		if days < 1 || days > 90 {
			return page[postscale.WebhookDelivery]{}, usageError("days must be between 1 and 90")
		}
		if endpoint != "" {
			if err := validID(endpoint); err != nil {
				return page[postscale.WebhookDelivery]{}, err
			}
		}
		p, err := client.Webhooks.Deliveries(ctx, &postscale.ListWebhookDeliveriesParams{Limit: &limit, Offset: &offset,
			Status: optionalString(status), EndpointID: optionalString(endpoint), Days: &days})
		if err != nil {
			return page[postscale.WebhookDelivery]{}, err
		}
		return page[postscale.WebhookDelivery]{items: p.Deliveries, total: &p.Total, requestID: p.ResponseMetadata.RequestID, warnings: p.Warnings}, nil
	})
	list.Flags().StringVar(&status, "status", "", "Filter by delivery status")
	list.Flags().StringVar(&endpoint, "endpoint-id", "", "Filter by webhook endpoint UUID")
	list.Flags().IntVar(&days, "days", 7, "History window in days (1–90)")
	deliveries.AddCommand(list)
	cmd.AddCommand(deliveries)
	return cmd
}
