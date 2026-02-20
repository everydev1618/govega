package tools

import (
	"context"
	"fmt"
	"net/smtp"
	"os"
	"strconv"
	"strings"
)

// RegisterEmailTool registers the send_email built-in tool.
// SMTP configuration is read from environment variables at call time:
//   - SMTP_HOST (required)
//   - SMTP_PORT (default 587)
//   - SMTP_USER (required)
//   - SMTP_PASS (required)
//   - SMTP_FROM (defaults to SMTP_USER)
func RegisterEmailTool(t *Tools) {
	t.Register("send_email", ToolDef{
		Description: "Send an email via SMTP. Requires SMTP_HOST, SMTP_USER, and SMTP_PASS environment variables.",
		Fn: ToolFunc(func(ctx context.Context, params map[string]any) (string, error) {
			to, _ := params["to"].(string)
			if to == "" {
				return "", fmt.Errorf("to is required")
			}
			subject, _ := params["subject"].(string)
			if subject == "" {
				return "", fmt.Errorf("subject is required")
			}
			body, _ := params["body"].(string)
			if body == "" {
				return "", fmt.Errorf("body is required")
			}
			isHTML, _ := params["is_html"].(bool)

			host := os.Getenv("SMTP_HOST")
			if host == "" {
				return "", fmt.Errorf("SMTP_HOST environment variable is not set")
			}
			portStr := os.Getenv("SMTP_PORT")
			if portStr == "" {
				portStr = "587"
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return "", fmt.Errorf("invalid SMTP_PORT %q: %w", portStr, err)
			}
			user := os.Getenv("SMTP_USER")
			if user == "" {
				return "", fmt.Errorf("SMTP_USER environment variable is not set")
			}
			pass := os.Getenv("SMTP_PASS")
			if pass == "" {
				return "", fmt.Errorf("SMTP_PASS environment variable is not set")
			}
			from := os.Getenv("SMTP_FROM")
			if from == "" {
				from = user
			}

			// Build message.
			contentType := "text/plain"
			if isHTML {
				contentType = "text/html"
			}
			msg := strings.Join([]string{
				fmt.Sprintf("From: %s", from),
				fmt.Sprintf("To: %s", to),
				fmt.Sprintf("Subject: %s", subject),
				fmt.Sprintf("MIME-Version: 1.0"),
				fmt.Sprintf("Content-Type: %s; charset=utf-8", contentType),
				"",
				body,
			}, "\r\n")

			addr := fmt.Sprintf("%s:%d", host, port)
			auth := smtp.PlainAuth("", user, pass, host)

			if err := smtp.SendMail(addr, auth, from, []string{to}, []byte(msg)); err != nil {
				return "", fmt.Errorf("send email: %w", err)
			}

			return fmt.Sprintf("Email sent to %s", to), nil
		}),
		Params: map[string]ParamDef{
			"to": {
				Type:        "string",
				Description: "Recipient email address",
				Required:    true,
			},
			"subject": {
				Type:        "string",
				Description: "Email subject line",
				Required:    true,
			},
			"body": {
				Type:        "string",
				Description: "Email body content",
				Required:    true,
			},
			"is_html": {
				Type:        "boolean",
				Description: "Set to true to send HTML email instead of plain text (default: false)",
				Required:    false,
			},
		},
	})
}
