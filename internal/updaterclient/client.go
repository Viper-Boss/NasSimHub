package updaterclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

const (
	defaultTimeout       = 8 * time.Second
	maximumResponseBytes = 1 << 20
)

var (
	ErrHardwareConfirmationRequired = errors.New("hardware update confirmation is required")
	ErrOperationRunning             = errors.New("a software update is already running")
	ErrUnavailable                  = errors.New("software updater is unavailable")
	// ErrNothingToDownload means there is no installable release to fetch.
	ErrNothingToDownload = errors.New("no installable release is on offer")
	// ErrDownloadUnsupported means this updater cannot fetch ahead of
	// installing -- an older updater, or a runtime without that ability.
	ErrDownloadUnsupported = errors.New("this updater cannot download ahead of installing")
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	BaseURL        string
	CurrentVersion string
	Token          string
	Client         HTTPClient
}

type Client struct {
	baseURL        *url.URL
	currentVersion string
	token          string
	client         HTTPClient
}

func New(options Options) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || baseURL.Scheme != "http" || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid updater URL")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	currentVersion := strings.TrimSpace(options.CurrentVersion)
	if currentVersion == "" {
		currentVersion = "dev"
	}
	return &Client{
		baseURL:        baseURL,
		currentVersion: currentVersion,
		token:          strings.TrimSpace(options.Token),
		client:         client,
	}, nil
}

func (client *Client) Check(ctx context.Context) updatecheck.Result {
	var result updatecheck.Result
	path := "/v1/updates/check"
	if updatecheck.RefreshRequested(ctx) {
		path += "?refresh=1"
	}
	if err := client.request(ctx, http.MethodGet, path, nil, &result); err != nil {
		return updatecheck.Result{
			Status:         updatecheck.StatusUnavailable,
			CurrentVersion: client.currentVersion,
			CheckedAt:      time.Now().UTC().Format(time.RFC3339),
			ErrorCode:      "updater_unavailable",
		}
	}
	return result
}

func (client *Client) Apply(
	ctx context.Context,
	request updatecheck.ApplyRequest,
) (updatecheck.Operation, error) {
	var operation updatecheck.Operation
	err := client.request(ctx, http.MethodPost, "/v1/updates/apply", request, &operation)
	return operation, err
}

func (client *Client) Status(ctx context.Context) (updatecheck.Operation, error) {
	var operation updatecheck.Operation
	err := client.request(ctx, http.MethodGet, "/v1/updates/status", nil, &operation)
	return operation, err
}

// Download starts a background download of the release on offer.
func (client *Client) Download(ctx context.Context) (updatecheck.Download, error) {
	var download updatecheck.Download
	err := client.request(ctx, http.MethodPost, "/v1/updates/download", nil, &download)
	return download, err
}

// SetPreferences saves the operator's update preferences.
func (client *Client) SetPreferences(
	ctx context.Context,
	preferences updatecheck.Preferences,
) (updatecheck.Preferences, error) {
	var saved updatecheck.Preferences
	err := client.request(ctx, http.MethodPut, "/v1/updates/preferences", preferences, &saved)
	return saved, err
}

// RunningImages reports what every component is running.
func (client *Client) RunningImages(ctx context.Context) ([]updatecheck.RunningImage, error) {
	var response struct {
		Images []updatecheck.RunningImage `json:"images"`
	}
	if err := client.request(ctx, http.MethodGet, "/v1/updates/running", nil, &response); err != nil {
		return nil, err
	}
	return response.Images, nil
}

func (client *Client) request(
	ctx context.Context,
	method, path string,
	input, output any,
) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	requestURL := *client.baseURL
	requestPath, rawQuery, _ := strings.Cut(path, "?")
	requestURL.Path += requestPath
	requestURL.RawQuery = rawQuery
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var remote struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, maximumResponseBytes)).Decode(&remote)
		switch remote.Code {
		case "hardware_confirmation_required":
			return ErrHardwareConfirmationRequired
		case "update_in_progress":
			return ErrOperationRunning
		case "update_not_available":
			return ErrNothingToDownload
		case "download_unsupported", "not_found":
			return ErrDownloadUnsupported
		default:
			return ErrUnavailable
		}
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maximumResponseBytes)).Decode(output); err != nil {
		return ErrUnavailable
	}
	return nil
}
