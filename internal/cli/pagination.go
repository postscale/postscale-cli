package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	postscale "github.com/postscale/postscale-go"
	"github.com/spf13/cobra"
)

type page[T any] struct {
	items     []T
	total     *int
	requestID string
	warnings  []postscale.WebhookDeliveryWarning
}

type fetchPage[T any] func(context.Context, *postscale.Client, int, int) (page[T], error)

func listCommand[T any](a *app, use, short string, fetch fetchPage[T]) *cobra.Command {
	var offset, limit int
	var all bool
	cmd := &cobra.Command{Use: use, Short: short, Args: noArgs}
	cmd.Flags().IntVar(&limit, "limit", 50, "Page size (1–100)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Starting offset (zero-based)")
	cmd.Flags().BoolVar(&all, "all", false, "Fetch all remaining pages within the command timeout")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if limit < 1 || limit > 100 || offset < 0 {
			return usageError("limit must be between 1 and 100; offset must be nonnegative")
		}
		return a.withClient(cmd, func(ctx context.Context, client *postscale.Client) error {
			value, err := collectPages(ctx, client, offset, limit, all, fetch)
			if err != nil {
				return err
			}
			if err := a.printResult(value); err != nil {
				return err
			}
			if len(value.Warnings) > 0 {
				return resultError("partial_result", "delivery history is incomplete; inspect warnings in stdout")
			}
			return nil
		})
	}
	return cmd
}

func collectPages[T any](ctx context.Context, client *postscale.Client, offset, limit int, all bool, fetch fetchPage[T]) (result, error) {
	items := make([]T, 0)
	position := offset
	seen := make(map[[32]byte]bool)
	value := result{}
	for {
		if err := ctx.Err(); err != nil {
			return result{}, err
		}
		p, err := fetch(ctx, client, position, limit)
		if err != nil {
			return result{}, err
		}
		count := len(p.items)
		if count > limit || (p.total != nil && (*p.total < 0 || (count > 0 && position+count > *p.total))) {
			return result{}, resultError("invalid_pagination", "API returned inconsistent page counts")
		}
		if count == 0 && p.total != nil && position < *p.total {
			return result{}, resultError("invalid_pagination", "API returned an empty page before the reported total")
		}
		if all && count > 0 {
			data, err := json.Marshal(p.items)
			if err != nil {
				return result{}, err
			}
			hash := sha256.Sum256(data)
			if seen[hash] {
				return result{}, resultError("invalid_pagination", "API repeated a page; refusing to loop or duplicate output")
			}
			seen[hash] = true
		}
		items = append(items, p.items...)
		position += count
		hasMore := count == limit
		if p.total != nil {
			hasMore = position < *p.total
		}
		pagination := &paginationInfo{Offset: offset, Limit: limit, Returned: len(items), Total: p.total, HasMore: hasMore}
		if hasMore {
			next := position
			pagination.NextOffset = &next
		}
		value = result{Data: items, Pagination: pagination, RequestID: p.requestID, Warnings: append(value.Warnings, p.warnings...)}
		if !all || !hasMore || len(p.warnings) > 0 {
			return value, nil
		}
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
