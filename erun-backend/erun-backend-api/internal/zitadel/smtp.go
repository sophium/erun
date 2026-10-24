package zitadel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SMTPConfig is the non-secret outbound-mail configuration; Zitadel never returns
// the password.
type SMTPConfig struct {
	Host           string `json:"host"`
	User           string `json:"user"`
	SenderAddress  string `json:"senderAddress"`
	SenderName     string `json:"senderName"`
	ReplyToAddress string `json:"replyToAddress,omitempty"`
	TLS            bool   `json:"tls"`
}

// SMTPStatus reports whether an ACTIVE Zitadel SMTP config exists; Config is zero otherwise.
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

// GetSMTPStatus reads the active SMTP config; Zitadel's 404 when none is active
// is reported as Configured: false.
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

// SetSMTPConfigParams is the desired outbound-mail state. An empty Password keeps
// the existing one on update and is rejected on first create.
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

// UpdateSMTPConfig creates and activates the first config (Zitadel creates it
// INACTIVE) or updates the existing one. A no-op SMTP PUT is accepted, so no
// diff guard is needed.
func (c *Client) UpdateSMTPConfig(ctx context.Context, params SetSMTPConfigParams) (SMTPStatus, error) {
	if strings.TrimSpace(params.Host) == "" || strings.TrimSpace(params.SenderAddress) == "" {
		return SMTPStatus{}, fmt.Errorf("smtp host and sender address are required")
	}

	existing, err := c.searchSMTPConfigs(ctx)
	if err != nil {
		return SMTPStatus{}, fmt.Errorf("search zitadel smtp configs: %w", err)
	}

	if len(existing) == 0 {
		if err := c.createAndActivateSMTPConfig(ctx, params); err != nil {
			return SMTPStatus{}, err
		}
		return smtpStatusFromParams(params), nil
	}
	if err := c.convergeExistingSMTPConfig(ctx, existing[0], params); err != nil {
		return SMTPStatus{}, err
	}
	return smtpStatusFromParams(params), nil
}

func (c *Client) createAndActivateSMTPConfig(ctx context.Context, params SetSMTPConfigParams) error {
	if strings.TrimSpace(params.Password) == "" {
		return fmt.Errorf("smtp password is required to configure mail delivery for the first time")
	}
	id, err := c.createSMTPConfig(ctx, params)
	if err != nil {
		return fmt.Errorf("create zitadel smtp config: %w", err)
	}
	if err := c.activateSMTPConfig(ctx, id); err != nil {
		return fmt.Errorf("activate zitadel smtp config: %w", err)
	}
	return nil
}

// Zitadel's password update refuses an inactive config, so activation comes first.
func (c *Client) convergeExistingSMTPConfig(ctx context.Context, current smtpConfigSearchResult, params SetSMTPConfigParams) error {
	if current.State != "SMTP_CONFIG_ACTIVE" {
		if err := c.activateSMTPConfig(ctx, current.ID); err != nil {
			return fmt.Errorf("activate zitadel smtp config: %w", err)
		}
	}
	if err := c.updateSMTPConfigFields(ctx, current.ID, params); err != nil {
		return fmt.Errorf("update zitadel smtp config: %w", err)
	}
	if strings.TrimSpace(params.Password) != "" {
		if err := c.updateSMTPConfigPassword(ctx, current.ID, params.Password); err != nil {
			return fmt.Errorf("update zitadel smtp password: %w", err)
		}
	}
	return nil
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

// Password changes go through their own endpoint, so the update-fields PUT omits it.
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
