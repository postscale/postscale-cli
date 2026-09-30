package cli

import (
	"context"

	postscale "github.com/postscale/postscale-go"
	"github.com/spf13/cobra"
)

func (a *app) inboundCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "inbound", Short: "Inspect received emails"}
	var status, from, to, query string
	list := listCommand(a, "list", "List received emails", func(ctx context.Context, client *postscale.Client, offset, limit int) (page[postscale.InboundEmail], error) {
		p, err := client.Inbound.List(ctx, &postscale.ListInboundParams{Limit: &limit, Offset: &offset,
			Status: optionalString(status), From: optionalString(from), To: optionalString(to), Query: optionalString(query)})
		if err != nil {
			return page[postscale.InboundEmail]{}, err
		}
		return page[postscale.InboundEmail]{items: p.Emails, total: &p.Total, requestID: p.ResponseMetadata.RequestID}, nil
	})
	list.Flags().StringVar(&status, "status", "", "Filter by inbound status")
	list.Flags().StringVar(&from, "from", "", "Filter by sender")
	list.Flags().StringVar(&to, "to", "", "Filter by recipient")
	list.Flags().StringVar(&query, "query", "", "Search received emails")
	cmd.AddCommand(list)
	cmd.AddCommand(&cobra.Command{Use: "get EMAIL_ID", Short: "Inspect a received email by resource UUID", Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0]); err != nil {
				return err
			}
			return a.withClient(cmd, func(ctx context.Context, client *postscale.Client) error {
				v, err := client.Inbound.Get(ctx, args[0])
				if err != nil {
					return err
				}
				return a.respond(v, v.ResponseMetadata)
			})
		}})
	return cmd
}
