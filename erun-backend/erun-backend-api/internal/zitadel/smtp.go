package zitadel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SMTPConfig is the non-secret half of the platform's outbound-mail
// configuration -- everything Zitadel's Admin API returns from a read. The
// password is never included: Zitadel does not return it either, and this
// client only ever writes it.
type SMTPConfig struct {
	Host           string `json:"host"`
	User           string `json:"user"`
	SenderAddress  string `json:"senderAddress"`
	SenderName     string `json:"senderName"`
	ReplyToAddress string `json:"replyToAddress,omitempty"`
	TLS            bool   `json:"tls"`
}

// SMTPStatus is the platform's honest answer to "can this instance send mail
// at all" (issue #1168). Every flow that reaches a user out of band --
// signup verification, password reset, invitation -- depends on it, and
// today the only signal is a 404 nobody checks. Configured reports whether
// an ACTIVE Zitadel SMTP config exists; Config is the zero value otherwise.
type SMTPStatus struct {
	Configured bool       `json:"configured"`
	Config     SMTPConfig `json:"config"`
}

type smtpConfigResponse struct {
	SMTPConfig struct {
		ID             string `json:"id"`
		Host           string `json:"host"`
		User           string `json:"user"`
		SenderAddress  string `json:"senderAddress"`
		SenderName     string `json:"senderName"`
		ReplyToAddress string `json:"replyToAddress"`
		TLS            bool   `json:"tls"`
		State          string `json:"state"`
	} `json:"smtpConfig"`
}

// GetSMTPStatus reads the platform's active outbound-mail configuration.
// Zitadel answers 404 ("SMTP configuration not found") when no config is
// active -- verified live against a real instance -- which this reports as
// SMTPStatus{Configured: false}, not as an error.
func (c *Client) GetSMTPStatus(ctx context.Context) (SMTPStatus, error) {
	var resp smtpConfigResponse
	if err := c.call(ctx, http.MethodGet, "/admin/v1/smtp", nil, &resp); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.NotFound() {
			return SMTPStatus{}, nil
		}
		return SMTPStatus{}, err
	}
	return SMTPStatus{
		Configured: true,
		Config: SMTPConfig{
			Host:           resp.SMTPConfig.Host,
			User:           resp.SMTPConfig.User,
			SenderAddress:  resp.SMTPConfig.SenderAddress,
			SenderName:     resp.SMTPConfig.SenderName,
			ReplyToAddress: resp.SMTPConfig.ReplyToAddress,
			TLS:            resp.SMTPConfig.TLS,
		},
	}, nil
}

// SetSMTPConfigParams is the declarative desired state for the platform's
// outbound mail (issue #1168): provider-agnostic host (including port,
// e.g. "smtp.example.com:587"), username, sender identity, and TLS, plus
// Password -- sourced by the caller from wherever it holds the credential
// out of band, never generated or invented here. Password empty on an
// update leaves Zitadel's existing password untouched; it is required the
// first time a config is created, since there is nothing yet to leave
// untouched.
type SetSMTPConfigParams struct {
	Host           string
	User           string
	Password       string
	SenderAddress  string
	SenderName     string
	ReplyToAddress string
	TLS            bool
}

type smtpConfigSearchResult struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type smtpConfigSearchResponse struct {
	Result []smtpConfigSearchResult `json:"result"`
}

// UpdateSMTPConfig converges the platform's outbound-mail configuration to
// params. Zitadel's SMTP API is id-addressed and a freshly created config
// defaults to SMTP_CONFIG_INACTIVE (invisible to GetSMTPStatus until
// activated) -- confirmed live -- so this creates-and-activates the
// platform's first config when none exists, or read-modify-writes the
// existing one otherwise, the same create-vs-update split UpdateOrgSettings
// uses for org policy. Unlike the login/password policies, Zitadel accepts a
// no-op SMTP PUT without a "NotChanged" error (also confirmed live), so this
// does not need that convergence guard.
func (c *Client) UpdateSMTPConfig(ctx context.Context, params SetSMTPConfigParams) (SMTPStatus, error) {
	host := strings.TrimSpace(params.Host)
	senderAddress := strings.TrimSpace(params.SenderAddress)
	if host == "" || senderAddress == "" {
		return SMTPStatus{}, fmt.Errorf("smtp host and sender address are required")
	}

	existing, err := c.searchSMTPConfigs(ctx)
	if err != nil {
		return SMTPStatus{}, fmt.Errorf("search zitadel smtp configs: %w", err)
	}

	if len(existing) == 0 {
		if strings.TrimSpace(params.Password) == "" {
			return SMTPStatus{}, fmt.Errorf("smtp password is required to configure mail delivery for the first time")
		}
		id, err := c.createSMTPConfig(ctx, params)
		if err != nil {
			return SMTPStatus{}, fmt.Errorf("create zitadel smtp config: %w", err)
		}
		if err := c.activateSMTPConfig(ctx, id); err != nil {
			return SMTPStatus{}, fmt.Errorf("activate zitadel smtp config: %w", err)
		}
		return smtpStatusFromParams(params), nil
	}

	current := existing[0]
	if err := c.updateSMTPConfigFields(ctx, current.ID, params); err != nil {
		return SMTPStatus{}, fmt.Errorf("update zitadel smtp config: %w", err)
	}
	if strings.TrimSpace(params.Password) != "" {
		if err := c.updateSMTPConfigPassword(ctx, current.ID, params.Password); err != nil {
			return SMTPStatus{}, fmt.Errorf("update zitadel smtp password: %w", err)
		}
	}
	if current.State != "SMTP_CONFIG_ACTIVE" {
		if err := c.activateSMTPConfig(ctx, current.ID); err != nil {
			return SMTPStatus{}, fmt.Errorf("activate zitadel smtp config: %w", err)
		}
	}
	return smtpStatusFromParams(params), nil
}

func smtpStatusFromParams(params SetSMTPConfigParams) SMTPStatus {
	return SMTPStatus{
		Configured: true,
		Config: SMTPConfig{
			Host:           strings.TrimSpace(params.Host),
			User:           strings.TrimSpace(params.User),
			SenderAddress:  strings.TrimSpace(params.SenderAddress),
			SenderName:     strings.TrimSpace(params.SenderName),
			ReplyToAddress: strings.TrimSpace(params.ReplyToAddress),
			TLS:            params.TLS,
		},
	}
}

func (c *Client) searchSMTPConfigs(ctx context.Context) ([]smtpConfigSearchResult, error) {
	var resp smtpConfigSearchResponse
	if err := c.call(ctx, http.MethodPost, "/admin/v1/smtp/_search", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *Client) createSMTPConfig(ctx context.Context, params SetSMTPConfigParams) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/admin/v1/smtp", smtpConfigBody(params, true), &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *Client) updateSMTPConfigFields(ctx context.Context, id string, params SetSMTPConfigParams) error {
	return c.call(ctx, http.MethodPut, fmt.Sprintf("/admin/v1/smtp/%s", url.PathEscape(id)), smtpConfigBody(params, false), nil)
}

func (c *Client) updateSMTPConfigPassword(ctx context.Context, id string, password string) error {
	return c.call(ctx, http.MethodPut, fmt.Sprintf("/admin/v1/smtp/%s/password", url.PathEscape(id)), map[string]any{"password": password}, nil)
}

func (c *Client) activateSMTPConfig(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, fmt.Sprintf("/admin/v1/smtp/%s/_activate", url.PathEscape(id)), map[string]any{}, nil)
}

// smtpConfigBody builds the AddSMTPConfig/UpdateSMTPConfig request body.
// includePassword is false for the update-fields PUT: Zitadel exposes
// password changes through its own dedicated endpoint
// (updateSMTPConfigPassword), so the update-fields body never carries one.
func smtpConfigBody(params SetSMTPConfigParams, includePassword bool) map[string]any {
	body := map[string]any{
		"senderAddress":  strings.TrimSpace(params.SenderAddress),
		"senderName":     strings.TrimSpace(params.SenderName),
		"tls":            params.TLS,
		"host":           strings.TrimSpace(params.Host),
		"user":           strings.TrimSpace(params.User),
		"replyToAddress": strings.TrimSpace(params.ReplyToAddress),
	}
	if includePassword {
		body["password"] = params.Password
	}
	return body
}
