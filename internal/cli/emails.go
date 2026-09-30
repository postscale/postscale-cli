package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	postscale "github.com/postscale/postscale-go"
	"github.com/spf13/cobra"
)

func (a *app) emailsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "emails", Short: "Send email and inspect outbound delivery"}
	cmd.AddCommand(a.sendCommand())
	var status, from, to, subject string
	list := listCommand(a, "list", "List outbound emails (get/events use the resource id, not message_id)",
		func(ctx context.Context, client *postscale.Client, offset, limit int) (page[postscale.Email], error) {
			p, err := client.Emails.List(ctx, &postscale.ListEmailsParams{Limit: &limit, Offset: &offset,
				Status: optionalString(status), From: optionalString(from), To: optionalString(to), Subject: optionalString(subject)})
			if err != nil {
				return page[postscale.Email]{}, err
			}
			return page[postscale.Email]{items: p.Emails, total: &p.Total, requestID: p.ResponseMetadata.RequestID}, nil
		})
	list.Flags().StringVar(&status, "status", "", "Filter by delivery status")
	list.Flags().StringVar(&from, "from", "", "Filter by sender")
	list.Flags().StringVar(&to, "to", "", "Filter by recipient")
	list.Flags().StringVar(&subject, "subject", "", "Filter by subject")
	cmd.AddCommand(list)
	for _, operation := range []string{"get", "events"} {
		op := operation
		cmd.AddCommand(&cobra.Command{Use: op + " EMAIL_ID", Short: "Inspect an email resource by UUID from emails list", Args: exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := validID(args[0]); err != nil {
					return err
				}
				return a.withClient(cmd, func(ctx context.Context, client *postscale.Client) error {
					if op == "events" {
						v, err := client.Emails.ListEvents(ctx, args[0])
						if err != nil {
							return err
						}
						return a.respond(v, v.ResponseMetadata)
					}
					v, err := client.Emails.Get(ctx, args[0])
					if err != nil {
						return err
					}
					return a.respond(v, v.ResponseMetadata)
				})
			}})
	}
	return cmd
}

func (a *app) sendCommand() *cobra.Command {
	var file string
	var dryRun bool
	cmd := &cobra.Command{Use: "send", Short: "Submit one email from a JSON file or stdin; never automatically retried", Args: noArgs,
		Long: "Submit one email using API-native JSON fields. --file - reads stdin.\nA ps_test_ key records a simulation; a ps_live_ key submits live mail.\nThe API does not support Idempotency-Key. An uncertain result must be inspected before resubmitting.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return usageError("--file is required (use --file - for stdin)")
			}
			params, err := a.readEmail(file)
			if err != nil {
				return err
			}
			if dryRun {
				return a.print(map[string]any{"valid": true, "submitted": false, "validation": "local_only"})
			}
			return a.withClient(cmd, func(ctx context.Context, client *postscale.Client) error {
				v, err := client.Emails.Send(ctx, params)
				if err != nil {
					return err
				}
				return a.respond(v, v.ResponseMetadata)
			})
		}}
	cmd.Flags().StringVar(&file, "file", "", "JSON request file, or - to read stdin (maximum 80 MiB)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate input locally without contacting Postscale; does not check domain or sending eligibility")
	return cmd
}

func (a *app) readEmail(path string) (*postscale.SendEmailParams, error) {
	reader := a.in
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, usageError("cannot open message file")
		}
		defer f.Close()
		reader = f
	}
	const maxMessageBytes = 80 << 20
	data, err := io.ReadAll(io.LimitReader(reader, maxMessageBytes+1))
	if err != nil {
		return nil, usageError("cannot read message JSON")
	}
	if len(data) > maxMessageBytes {
		return nil, usageError("message JSON exceeds 80 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var params *postscale.SendEmailParams
	if err := decoder.Decode(&params); err != nil {
		return nil, usageError("invalid message JSON: " + err.Error())
	}
	if params == nil {
		return nil, usageError("message must be a JSON object")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, usageError("message file must contain exactly one JSON object")
	}
	if strings.TrimSpace(params.From) == "" || len(params.To) == 0 {
		return nil, usageError("from and at least one to recipient are required")
	}
	for _, recipients := range [][]string{params.To, params.CC, params.BCC} {
		for _, recipient := range recipients {
			if strings.TrimSpace(recipient) == "" {
				return nil, usageError("recipient addresses must not be empty")
			}
		}
	}
	if params.Template == "" && params.TemplateID == "" {
		if strings.TrimSpace(params.Subject) == "" {
			return nil, usageError("subject is required without a template")
		}
		if params.HTMLBody == "" && params.TextBody == "" {
			return nil, usageError("html_body or text_body is required without a template")
		}
	}
	if err := postscale.ValidateAttachments(params.Attachments); err != nil {
		return nil, err
	}
	return params, nil
}
